package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

// base is Monday 2026-09-21 09:15:00 IST (a normal session minute).
var base = time.Date(2026, 9, 21, 3, 45, 0, 0, time.UTC)

// syntheticTicks builds an in-order two-instrument stream: 150s, 4 ticks/s
// for the index, 1 tick/s for the option, deterministic prices in paise.
func syntheticTicks() []model.Tick {
	var ticks []model.Tick
	for ms := 0; ms < 150_000; ms += 250 {
		ts := base.Add(time.Duration(ms) * time.Millisecond)
		i := int64(ms / 250)
		ticks = append(ticks, model.Tick{
			Token: "99926000", Exchange: "NSE",
			Price:   2_500_000 + (i*37)%900 - 450,
			TickTS:  ts.Add(40 * time.Millisecond),
			EventTS: ts,
		})
		if ms%1000 == 500 {
			ticks = append(ticks, model.Tick{
				Token: "57791", Exchange: "NFO",
				Price:   12_000 + (i*13)%400,
				Qty:     75,
				TickTS:  ts.Add(55 * time.Millisecond),
				EventTS: ts,
			})
		}
	}
	return ticks
}

func writeJSONL(t *testing.T, path string, ticks []model.Tick) {
	t.Helper()
	var buf bytes.Buffer
	for _, tk := range ticks {
		buf.Write(tk.JSON())
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runReplay(t *testing.T, ticks []model.Tick, opt Options) (string, Stats) {
	t.Helper()
	var out bytes.Buffer
	st, err := Replay(context.Background(), ticks, &out, opt)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return out.String(), st
}

type refKey struct {
	key    string
	bucket int64
}

// reference builds the expected 1s and TF candles with a trivially correct
// batch algorithm, independent of the streaming code under test.
func reference(ticks []model.Tick, tf int64) (map[refKey]model.Candle, map[refKey]model.TFCandle) {
	ones := map[refKey]model.Candle{}
	for _, tk := range ticks {
		k := refKey{tk.Exchange + ":" + tk.Token, tk.CanonicalTS().Unix()}
		c, ok := ones[k]
		if !ok {
			c = model.Candle{Token: tk.Token, Exchange: tk.Exchange, TS: time.Unix(k.bucket, 0).UTC(),
				Open: tk.Price, High: tk.Price, Low: tk.Price}
		}
		c.High = max(c.High, tk.Price)
		c.Low = min(c.Low, tk.Price)
		c.Close = tk.Price
		c.Volume += tk.Qty
		c.TicksCount++
		ones[k] = c
	}
	tfs := map[refKey]model.TFCandle{}
	for s := base.Unix(); s < base.Unix()+150; s++ { // time order
		for _, key := range []string{"NFO:57791", "NSE:99926000"} {
			c, ok := ones[refKey{key, s}]
			if !ok {
				continue
			}
			k := refKey{key, s - s%tf}
			fc, ok := tfs[k]
			if !ok {
				fc = model.TFCandle{Token: c.Token, Exchange: c.Exchange, TF: int(tf), TS: time.Unix(k.bucket, 0).UTC(),
					Open: c.Open, High: c.High, Low: c.Low}
			}
			fc.High = max(fc.High, c.High)
			fc.Low = min(fc.Low, c.Low)
			fc.Close = c.Close
			fc.Volume += c.Volume
			fc.Count++
			tfs[k] = fc
		}
	}
	return ones, tfs
}

func TestReplay_DeterministicAndMatchesReference(t *testing.T) {
	ticks := syntheticTicks()

	// Round-trip through a JSONL file, as the CLI does.
	path := filepath.Join(t.TempDir(), "ticks.jsonl")
	writeJSONL(t, path, ticks)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := ReadTicks(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(fromFile) != len(ticks) {
		t.Fatalf("read %d ticks, wrote %d", len(fromFile), len(ticks))
	}

	opt := Options{TFs: []int{60}, Sort: true}
	out1, st := runReplay(t, fromFile, opt)
	for i := 0; i < 3; i++ {
		if again, _ := runReplay(t, fromFile, opt); again != out1 {
			t.Fatalf("run %d output differs from run 0", i+1)
		}
	}
	if st.DroppedAgg != 0 || st.LateTicks != 0 || st.StaleRejects != 0 {
		t.Fatalf("unexpected loss on an in-order stream: %+v", st)
	}

	want1s, wantTF := reference(ticks, 60)
	got1s, gotTF := map[refKey]model.Candle{}, map[refKey]model.TFCandle{}
	for _, line := range strings.Split(strings.TrimSpace(out1), "\n") {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		switch r.Kind {
		case "1s":
			var c model.Candle
			_ = json.Unmarshal(r.Candle, &c)
			got1s[refKey{c.Key(), c.TS.Unix()}] = c
		case "tf":
			var c model.TFCandle
			_ = json.Unmarshal(r.Candle, &c)
			if _, dup := gotTF[refKey{c.Key(), c.TS.Unix()}]; dup {
				t.Fatalf("TF candle emitted twice: %s %v", c.Key(), c.TS)
			}
			gotTF[refKey{c.Key(), c.TS.Unix()}] = c
		default:
			t.Fatalf("unexpected kind %q with forming disabled", r.Kind)
		}
	}

	if len(got1s) != len(want1s) {
		t.Fatalf("1s candles: got %d want %d", len(got1s), len(want1s))
	}
	for k, w := range want1s {
		if g := got1s[k]; g != w {
			t.Fatalf("1s %v:\n got  %+v\n want %+v", k, g, w)
		}
	}
	// 150s from :00 → buckets 09:15, 09:16, 09:17(partial) per instrument.
	if len(gotTF) != 6 || len(wantTF) != 6 {
		t.Fatalf("TF candles: got %d want %d (expected 6)", len(gotTF), len(wantTF))
	}
	for k, w := range wantTF {
		if g := gotTF[k]; g != w {
			t.Fatalf("TF %v:\n got  %+v\n want %+v", k, g, w)
		}
	}
	if st.Candles1s != 300 || st.TFCandles != 6 {
		t.Fatalf("stats: %+v (want 300 1s candles, 6 TF candles)", st)
	}
}

func TestReplay_LateTickDroppedBehindWatermark(t *testing.T) {
	ts := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }
	tk := func(s int, p int64) model.Tick {
		return model.Tick{Token: "99926000", Exchange: "NSE", Price: p, EventTS: ts(s), TickTS: ts(s)}
	}
	ticks := []model.Tick{tk(0, 100), tk(1, 101), tk(5, 105), tk(1, 999) /* 4s behind: late */, tk(6, 106)}
	out, st := runReplay(t, ticks, Options{TFs: []int{60}, Sort: true})
	if st.LateTicks != 1 {
		t.Fatalf("late ticks = %d, want 1", st.LateTicks)
	}
	if strings.Contains(out, `"high":999`) {
		t.Fatal("late tick leaked into a finalised candle")
	}
}

// TestReplay_Golden pins exact output for a checked-in tick file. When a
// change to agg/tfbuilder alters candles on purpose, rerun with -update and
// review the diff of testdata/sample_golden.jsonl.
func TestReplay_Golden(t *testing.T) {
	in := filepath.Join("testdata", "sample_ticks.jsonl")
	golden := filepath.Join("testdata", "sample_golden.jsonl")
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		writeJSONL(t, in, syntheticTicks())
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatalf("%v (run: go test ./cmd/replay -run Golden -update)", err)
	}
	ticks, err := ReadTicks(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := runReplay(t, ticks, Options{TFs: []int{60, 120}, Sort: true, IncludeForming: true})
	if *update {
		if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Fatalf("output differs from %s; if intended, rerun with -update and review the diff", golden)
	}
}

func TestParseTFs(t *testing.T) {
	got, err := parseTFs("60, 300,3600")
	if err != nil || len(got) != 3 || got[0] != 60 || got[2] != 3600 {
		t.Fatalf("parseTFs = %v, %v", got, err)
	}
	if _, err := parseTFs("60,x"); err == nil {
		t.Fatal("expected error for bad TF")
	}
}
