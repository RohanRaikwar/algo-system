package strategy

import (
	"testing"
	"time"
)

func TestRange3mEntrySeries(t *testing.T) {
	cfg := range5mConfig()
	cfg.EntryTFMinutes = 3
	s, f := newWarmRange(t, cfg)
	st := s.instruments["NSE:NIFTY"]
	if st.ctx.aggE.Minutes != 3 || len(st.ctx.barsE) == 0 {
		t.Fatalf("3m series not built: minutes=%d bars=%d", st.ctx.aggE.Minutes, len(st.ctx.barsE))
	}
	// 3m buckets from 09:15: 09:15-09:17, 09:18-09:20, … → closes at minute index 2, 5, 8 …
	start := day4(f, 9, 15)
	var closes []int
	for i := 0; i < 9; i++ {
		s.OnTFCandle(tfc(start.Add(time.Duration(i)*time.Minute), f.bar(f.next())))
		if s.entryBarClosed(st) {
			closes = append(closes, i)
		}
	}
	if len(closes) != 3 || closes[0] != 2 || closes[1] != 5 || closes[2] != 8 {
		t.Fatalf("3m closes at %v, want [2 5 8]", closes)
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

func TestHTFFilterBlocksCounterTrendBreakout(t *testing.T) {
	cfg := range5mConfig()
	cfg.MeanReversionEnabled = false
	cfg.HTFTrendFilter = true
	s, f := newWarmRange(t, cfg)
	quiet(t, runUntil(s, f, 11, 15))
	st := s.instruments["NSE:NIFTY"]
	// Force the 1h view against the CALL breakout.
	blocked := !s.htfAllows(st, SideCall) || !s.htfAllows(st, SidePut)
	if !blocked {
		t.Fatal("filter must refuse at least one direction")
	}
	if s.htfAllows(st, SideCall) && s.htfAllows(st, SidePut) {
		t.Fatal("both directions allowed with the filter on")
	}
	cfg.HTFTrendFilter = false
	off := NewNifty50RangeWithConfig(1, cfg)
	if !off.htfAllows(st, SideCall) || !off.htfAllows(st, SidePut) {
		t.Fatal("filter off must allow both directions")
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
