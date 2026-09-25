package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"trading-systemv1/internal/marketdata/agg"
	"trading-systemv1/internal/marketdata/tfbuilder"
	"trading-systemv1/internal/model"
)

// Options controls one replay run.
type Options struct {
	TFs []int // timeframes in seconds for the TF builder

	// Speed scales the gaps between consecutive ticks' canonical timestamps.
	// 0 = as fast as possible, 1 = real time, 10 = 10x.
	Speed float64
	// MaxGap caps a single paced sleep (before scaling) so overnight gaps
	// in a recording don't stall a replay. 0 = no cap.
	MaxGap time.Duration

	// MarketCloseGate mirrors mdengine production (true) vs staging (false).
	MarketCloseGate bool
	// IncludeForming also emits forming TF snapshots (the live-preview stream).
	IncludeForming bool
	// Sort buffers all output and writes it in a canonical order at the end.
	// Emission order across tokens depends on goroutine scheduling; content
	// does not. Sort=true makes output byte-for-byte comparable across runs.
	Sort bool
}

// Stats summarises a replay run.
type Stats struct {
	Ticks        int
	Candles1s    int
	TFCandles    int
	Forming      int
	DroppedAgg   int // aggregator candleCh full (should be 0; nonzero = replay bug)
	LateTicks    int // ticks behind the event-time watermark (real behaviour)
	StaleRejects int // 1s candles rejected by TF builder staleness check
}

// Record is one JSONL output line.
type Record struct {
	Kind   string          `json:"kind"` // "1s" | "tf" | "tf_forming"
	Candle json.RawMessage `json:"candle"`

	sortTS  int64
	sortTF  int
	sortKey string
	seq     int
}

// ReadTicks parses a JSONL stream of model.Tick. Blank lines are skipped.
func ReadTicks(r io.Reader) ([]model.Tick, error) {
	var ticks []model.Tick
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(trimSpace(b)) == 0 {
			continue
		}
		var t model.Tick
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if t.Token == "" {
			return nil, fmt.Errorf("line %d: tick has no token", line)
		}
		ticks = append(ticks, t)
	}
	return ticks, sc.Err()
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// Replay pushes ticks through the real 1s aggregator (internal/marketdata/agg)
// and TF builder (internal/marketdata/tfbuilder), in process, and writes the
// resulting candles to w as JSONL.
//
// Differences from mdengine, by design:
//   - no Redis/SQLite writers, no fan-out bus;
//   - the TF builder runs with FlushGrace=0 and without RunWithTimer, so TF
//     candles are finalised by the next bucket's first 1s candle (event time)
//     or by the final session flush, never by the wall clock. The wall-clock
//     timer path cannot be replayed deterministically against historical data.
func Replay(ctx context.Context, ticks []model.Tick, w io.Writer, opt Options) (Stats, error) {
	var st Stats
	if len(opt.TFs) == 0 {
		return st, fmt.Errorf("no timeframes")
	}

	a := agg.New()
	a.MarketCloseGate = opt.MarketCloseGate
	a.OnDroppedTick = func() { st.DroppedAgg++ }
	a.OnLateTick = func() { st.LateTicks++ } // called from the aggregator goroutine only

	b := tfbuilder.New(opt.TFs)
	b.FlushGrace = 0
	b.OnStaleCandle = func() { st.StaleRejects++ }

	enc := json.NewEncoder(w)
	var buffered []Record
	seq := 0
	var writeErr error
	out := func(kind string, v any, ts time.Time, tf int, key string) {
		raw, err := json.Marshal(v)
		if err != nil {
			writeErr = err
			return
		}
		rec := Record{Kind: kind, Candle: raw, sortTS: ts.Unix(), sortTF: tf, sortKey: key, seq: seq}
		seq++
		if opt.Sort {
			buffered = append(buffered, rec)
			return
		}
		if err := enc.Encode(rec); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	emitTF := func(c model.TFCandle) {
		if c.Forming {
			st.Forming++
			if opt.IncludeForming {
				out("tf_forming", c, c.TS, c.TF, c.Key())
			}
			return
		}
		st.TFCandles++
		out("tf", c, c.TS, c.TF, c.Key())
	}

	tickCh := make(chan model.Tick)
	candleCh := make(chan model.Candle, 1<<16)
	tfCh := make(chan model.TFCandle, 2*len(opt.TFs)+16)

	aggDone := make(chan struct{})
	go func() {
		a.Run(context.Background(), tickCh, candleCh) // returns (after flushAll) when tickCh closes
		close(aggDone)
	}()

	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for c := range candleCh {
			st.Candles1s++
			out("1s", c, c.TS, 1, c.Key())
			b.Run1(c, tfCh)
			for drained := false; !drained; {
				select {
				case tc := <-tfCh:
					emitTF(tc)
				default:
					drained = true
				}
			}
		}
	}()

	var prev time.Time
	var ctxErr error
feed:
	for _, t := range ticks {
		if opt.Speed > 0 {
			ts := t.CanonicalTS()
			if !prev.IsZero() {
				if gap := ts.Sub(prev); gap > 0 {
					if opt.MaxGap > 0 && gap > opt.MaxGap {
						gap = opt.MaxGap
					}
					select {
					case <-ctx.Done():
						ctxErr = ctx.Err()
						break feed
					case <-time.After(time.Duration(float64(gap) / opt.Speed)):
					}
				}
			}
			prev = ts
		}
		select {
		case <-ctx.Done():
			ctxErr = ctx.Err()
			break feed
		case tickCh <- t:
			st.Ticks++
		}
	}
	close(tickCh)
	<-aggDone
	close(candleCh)
	<-consumerDone

	// End of recording == end of session: finalise forming TF candles.
	finalCh := make(chan model.TFCandle, 1<<16)
	b.FlushSession(finalCh)
	close(finalCh)
	var final []model.TFCandle
	for c := range finalCh {
		final = append(final, c)
	}
	// flushAll iterates maps; order it so unsorted output is still stable.
	sort.Slice(final, func(i, j int) bool {
		if final[i].TF != final[j].TF {
			return final[i].TF < final[j].TF
		}
		return final[i].Key() < final[j].Key()
	})
	for _, c := range final {
		emitTF(c)
	}

	if opt.Sort {
		sortRecords(buffered)
		for _, r := range buffered {
			if err := enc.Encode(r); err != nil {
				return st, err
			}
		}
	}
	if writeErr != nil {
		return st, writeErr
	}
	return st, ctxErr
}

// kindRank orders records sharing a timestamp: 1s first, then TF by size.
func kindRank(k string) int {
	switch k {
	case "1s":
		return 0
	case "tf_forming":
		return 1
	default:
		return 2
	}
}

// sortRecords puts records in canonical order: bucket start, kind, TF,
// instrument, then emission order (only forming snapshots can tie beyond that).
func sortRecords(rs []Record) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.sortTS != b.sortTS {
			return a.sortTS < b.sortTS
		}
		if ka, kb := kindRank(a.Kind), kindRank(b.Kind); ka != kb {
			return ka < kb
		}
		if a.sortTF != b.sortTF {
			return a.sortTF < b.sortTF
		}
		if a.sortKey != b.sortKey {
			return a.sortKey < b.sortKey
		}
		return a.seq < b.seq
	})
}
