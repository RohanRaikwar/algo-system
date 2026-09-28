package strategy

import (
	"testing"
	"time"
)

// pts converts NIFTY points to paise.
func pts(p int64) int64 { return p * 100 }

// sessionMinutes yields every 1m bucket start of an IST session day.
func sessionMinutes(day time.Time) []time.Time {
	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 15, 0, 0, ist)
	out := make([]time.Time, 0, 375)
	for i := 0; i < 375; i++ {
		out = append(out, start.Add(time.Duration(i)*time.Minute))
	}
	return out
}

// rangePrice is a triangle wave between lo and hi with the given period (minutes).
func rangePrice(i int, lo, hi int64, period int) int64 {
	half := period / 2
	pos := i % period
	span := hi - lo
	if pos < half {
		return lo + span*int64(pos)/int64(half)
	}
	return hi - span*int64(pos-half)/int64(half)
}

// feedRangeDays feeds n session days of a 23,900-24,100 range into fn.
func feedRangeDays(n int, fn func(ts time.Time, b ohlcv)) time.Time {
	day := time.Date(2026, 1, 5, 0, 0, 0, 0, ist)
	var last time.Time
	idx := 0
	for d := 0; d < n; d++ {
		for _, ts := range sessionMinutes(day.AddDate(0, 0, d)) {
			p := rangePrice(idx, pts(23900), pts(24100), 120)
			fn(ts, ohlcv{Open: p, High: p + 300, Low: p - 300, Close: p, Volume: 0})
			last = ts
			idx++
		}
	}
	return last
}

func TestTFAggregatorBoundaries(t *testing.T) {
	a := tfAggregator{Minutes: 15}
	start := time.Date(2026, 1, 5, 9, 15, 0, 0, ist)
	var closed []tfBar
	for i := 0; i < 30; i++ {
		p := int64(1000 + i)
		if bar, ok := a.push(start.Add(time.Duration(i)*time.Minute), ohlcv{Open: p, High: p + 1, Low: p - 1, Close: p, Volume: 2}); ok {
			closed = append(closed, bar)
		}
	}
	if len(closed) != 2 {
		t.Fatalf("closed %d bars, want 2", len(closed))
	}
	b := closed[0]
	if !b.TS.Equal(start) || b.Open != 1000 || b.Close != 1014 || b.High != 1015 || b.Low != 999 || b.Volume != 30 {
		t.Errorf("first bar wrong: %+v", b)
	}
	if !closed[1].TS.Equal(start.Add(15 * time.Minute)) {
		t.Errorf("second bar TS %v", closed[1].TS)
	}
}

func TestTFAggregatorGapClosesBar(t *testing.T) {
	a := tfAggregator{Minutes: 5}
	start := time.Date(2026, 1, 5, 9, 15, 0, 0, ist)
	a.push(start, ohlcv{Open: 1, High: 1, Low: 1, Close: 1})
	a.push(start.Add(time.Minute), ohlcv{Open: 2, High: 2, Low: 2, Close: 2})
	bar, ok := a.push(start.Add(7*time.Minute), ohlcv{Open: 3, High: 3, Low: 3, Close: 3})
	if !ok || bar.Close != 2 || !bar.TS.Equal(start) {
		t.Fatalf("gap should close first bar: ok=%v bar=%+v", ok, bar)
	}
}

func TestTFAggregatorIgnoresPreMarket(t *testing.T) {
	a := tfAggregator{Minutes: 5}
	if _, ok := a.push(time.Date(2026, 1, 5, 9, 10, 0, 0, ist), ohlcv{Close: 1}); ok || a.Has {
		t.Fatal("pre-market candle must be ignored")
	}
}

func TestRangeContextDetectsRange(t *testing.T) {
	rc := newRangeContext(DefaultRangeContextConfig())
	feedRangeDays(3, func(ts time.Time, b ohlcv) { rc.update(ts, b) })
	st := rc.State()
	if !st.ADXReady {
		t.Fatal("ADX should be ready after 3 sessions")
	}
	if !st.InRange {
		t.Fatalf("expected range regime, got %+v", st)
	}
	if absInt64(st.Support-pts(23897)) > pts(15) || absInt64(st.Resistance-pts(24103)) > pts(15) {
		t.Errorf("levels off: support=%d resistance=%d", st.Support, st.Resistance)
	}
	if st.Class != RangeWide && st.Class != RangeMedium {
		t.Errorf("class %q for ~200pt range", st.Class)
	}
	if st.SupTouches < 2 || st.ResTouches < 2 {
		t.Errorf("touches sup=%d res=%d", st.SupTouches, st.ResTouches)
	}
}

func TestRangeContextTrendIsNotRange(t *testing.T) {
	rc := newRangeContext(DefaultRangeContextConfig())
	day := time.Date(2026, 1, 5, 0, 0, 0, 0, ist)
	p := pts(23000)
	for d := 0; d < 3; d++ {
		for _, ts := range sessionMinutes(day.AddDate(0, 0, d)) {
			p += 50 // +0.5 pt per minute, steady trend
			rc.update(ts, ohlcv{Open: p - 50, High: p + 20, Low: p - 70, Close: p})
		}
	}
	st := rc.State()
	if st.InRange {
		t.Fatalf("steady trend must not be a range: %+v", st)
	}
	if st.ADX < 25 {
		t.Errorf("trend ADX %.1f, want >= 25", st.ADX)
	}
}

func TestRangeContextDedupAndPrevDay(t *testing.T) {
	rc := newRangeContext(DefaultRangeContextConfig())
	ts := time.Date(2026, 1, 5, 9, 15, 0, 0, ist)
	if !rc.update(ts, ohlcv{Open: 10, High: 20, Low: 5, Close: 10}) {
		t.Fatal("first update rejected")
	}
	if rc.update(ts, ohlcv{Open: 10, High: 99, Low: 1, Close: 10}) {
		t.Fatal("duplicate TS must be ignored")
	}
	rc.update(ts.AddDate(0, 0, 1), ohlcv{Open: 10, High: 12, Low: 8, Close: 10})
	if rc.prevDayHigh != 20 || rc.prevDayLow != 5 {
		t.Errorf("prev day H/L = %d/%d, want 20/5", rc.prevDayHigh, rc.prevDayLow)
	}
}

func TestRangeContextSnapshotReplay(t *testing.T) {
	cfg := DefaultRangeContextConfig()
	rc := newRangeContext(cfg)
	feedRangeDays(3, func(ts time.Time, b ohlcv) { rc.update(ts, b) })
	restored := restoreRangeContext(cfg, rc.snapshot())
	a, b := rc.State(), restored.State()
	if a.InRange != b.InRange || a.Support != b.Support || a.Resistance != b.Resistance {
		t.Fatalf("restored state differs:\n%+v\n%+v", a, b)
	}
	if !restored.lastTS.Equal(rc.lastTS) {
		t.Error("lastTS not restored")
	}
}

func TestVolumeAtLeast(t *testing.T) {
	rc := newRangeContext(DefaultRangeContextConfig())
	ts := time.Date(2026, 1, 5, 9, 15, 0, 0, ist)
	for i := 0; i < 5; i++ {
		rc.update(ts.Add(time.Duration(i)*time.Minute), ohlcv{Close: 1, High: 1, Low: 1, Open: 1, Volume: 100})
	}
	rc.update(ts.Add(5*time.Minute), ohlcv{Close: 1, High: 1, Low: 1, Open: 1, Volume: 160})
	if !rc.volumeAtLeast(150, true) {
		t.Error("160 vs avg 100 should pass 150%")
	}
	if rc.volumeAtLeast(200, true) {
		t.Error("160 vs avg 100 should fail 200%")
	}

	noVol := newRangeContext(DefaultRangeContextConfig())
	noVol.update(ts, ohlcv{Close: 1})
	noVol.update(ts.Add(time.Minute), ohlcv{Close: 1})
	if !noVol.volumeAtLeast(150, false) || noVol.volumeAtLeast(150, true) {
		t.Error("no-volume data: pass only when volume not required")
	}
}
