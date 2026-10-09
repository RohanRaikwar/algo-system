package strategy

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func tfc(ts time.Time, b ohlcv) model.TFCandle {
	return model.TFCandle{Token: "NIFTY", Exchange: "NSE", TF: tf1m, TS: ts,
		Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume}
}

// rangeFeed generates the 23,900-24,100 triangle wave as directional bars
// (open = previous close) and remembers where it is.
type rangeFeed struct {
	idx  int
	prev int64
	day  time.Time
}

func newRangeFeed() *rangeFeed {
	return &rangeFeed{prev: pts(23900), day: time.Date(2026, 1, 5, 0, 0, 0, 0, ist)}
}

func (f *rangeFeed) next() int64 {
	p := rangePrice(f.idx, pts(23900), pts(24100), 120)
	f.idx++
	return p
}

func (f *rangeFeed) bar(p int64) ohlcv {
	b := ohlcv{Open: f.prev, Close: p, High: maxInt64(f.prev, p) + 200, Low: minInt64(f.prev, p) - 200}
	f.prev = p
	return b
}

// warmDays returns n days of wave candles.
func (f *rangeFeed) warmDays(n int) []model.TFCandle {
	var out []model.TFCandle
	for d := 0; d < n; d++ {
		for _, ts := range sessionMinutes(f.day) {
			out = append(out, tfc(ts, f.bar(f.next())))
		}
		f.day = f.day.AddDate(0, 0, 1)
	}
	return out
}

func TestMinuteVolume(t *testing.T) {
	var m minuteVolume
	base := time.Date(2026, 1, 8, 4, 30, 0, 0, time.UTC)
	if _, _, ok := m.observe(base.Add(5*time.Second), 1000); ok {
		t.Fatal("first tick only initialises")
	}
	m.observe(base.Add(40*time.Second), 1500)
	minute, vol, ok := m.observe(base.Add(62*time.Second), 1600)
	if !ok || !minute.Equal(base) || vol != 500 {
		t.Fatalf("minute=%v vol=%d ok=%v", minute, vol, ok)
	}
	// Day counter reset (new session) re-initialises without a bogus minute.
	if _, _, ok := m.observe(base.Add(24*time.Hour), 10); ok {
		t.Fatal("counter reset must not emit")
	}
}

func TestVolumeAtLeast5(t *testing.T) {
	rc := newRangeContext(DefaultRangeContextConfig())
	for i := 0; i < 5; i++ {
		rc.on5m(tfBar{ohlcv: ohlcv{Volume: 1000}})
	}
	rc.on5m(tfBar{ohlcv: ohlcv{Volume: 1300}})
	if !rc.volumeAtLeast5(120, true) || rc.volumeAtLeast5(150, true) {
		t.Fatal("5m volume vs average")
	}
}

func TestHTFTrendState(t *testing.T) {
	rc := newRangeContext(DefaultRangeContextConfig())
	day := time.Date(2026, 1, 5, 0, 0, 0, 0, ist)
	p := pts(23000)
	for d := 0; d < 4; d++ {
		for _, ts := range sessionMinutes(day.AddDate(0, 0, d)) {
			p += 30 // steady uptrend
			rc.update(ts, ohlcv{Open: p - 30, High: p + 10, Low: p - 40, Close: p})
		}
	}
	st := rc.State()
	if st.HTFEMA == 0 || st.HTFTrend != 1 {
		t.Fatalf("uptrend HTF state = ema %d trend %d", st.HTFEMA, st.HTFTrend)
	}
	if len(rc.bars60) == 0 {
		t.Fatal("no 1h bars")
	}
	r2 := restoreRangeContext(DefaultRangeContextConfig(), rc.snapshot())
	if r2.State().HTFTrend != 1 {
		t.Fatal("HTF state lost on restore")
	}
}

func TestHTFLevelsAddSwingPoints(t *testing.T) {
	base := DefaultRangeContextConfig()
	with := base
	with.HTFLevels = true
	a, b := newRangeContext(base), newRangeContext(with)
	feedRangeDays(4, func(ts time.Time, bar ohlcv) { a.update(ts, bar); b.update(ts, bar) })
	sa, sb := a.State(), b.State()
	if sb.SupTouches+sb.ResTouches <= sa.SupTouches+sa.ResTouches {
		t.Fatalf("HTF levels added no touches: %d+%d vs %d+%d", sb.SupTouches, sb.ResTouches, sa.SupTouches, sa.ResTouches)
	}
}
