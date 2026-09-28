package strategy

import (
	"strings"
	"testing"
	"time"
)

// flagDay feeds day 4 of a warmed strategy: open at start, a 300-pt fall
// to ~23,810 by 10:00, a tight box (boxLo..boxHi) until 11:05, and returns
// the minute cursor at 11:05.
func flagDay(t *testing.T, s *Nifty50Range, f *rangeFeed, boxLo, boxHi int64) time.Time {
	t.Helper()
	ts := day4(f, 9, 15)
	p := pts(24100)
	prev := p
	emit := func(close int64) {
		b := ohlcv{Open: prev, Close: close, High: maxInt64(prev, close) + 100, Low: minInt64(prev, close) - 100}
		if sig := s.OnTFCandle(tfc(ts, b)); sig != nil {
			t.Fatalf("unexpected signal at %s: %+v", ts.In(ist).Format("15:04"), sig)
		}
		prev = close
		ts = ts.Add(time.Minute)
	}
	for ts.Before(day4(f, 10, 0)) { // fall ≈ 6.7 pts/min
		p -= 667
		if p < boxHi {
			p = boxHi
		}
		emit(p)
	}
	i := 0
	for ts.Before(day4(f, 11, 5)) { // box
		if i%2 == 0 {
			emit(boxLo + 500)
		} else {
			emit(boxHi - 500)
		}
		i++
	}
	return ts
}

func flagConfig() Nifty50RangeConfig {
	cfg := range5mConfig()
	cfg.MeanReversionEnabled = false
	cfg.Range.MaxADX = 0.1 // regime never "range": isolate the flag path
	return cfg
}

func down5m(open, close int64) []ohlcv {
	out := make([]ohlcv, 5)
	step := (close - open) / 5
	p := open
	for i := range out {
		n := p + step
		if i == 4 {
			n = close
		}
		out[i] = ohlcv{Open: p, High: p + 100, Low: n - 100, Close: n}
		p = n
	}
	return out
}

func TestFlagPutOnTrendDayBreakdown(t *testing.T) {
	s, f := newWarmRange(t, flagConfig())
	lo, hi := pts(23790), pts(23830)
	flagDay(t, s, f, lo, hi)
	if sigs := feed1m(s, f, 11, 5, down5m(pts(23820), lo-2000)); len(sigs) != 0 {
		t.Fatalf("first breakdown bar must only arm: %+v", sigs)
	}
	st := s.instruments["NSE:NIFTY"]
	if st.PendingBreak != SidePut || st.PendingKind != RangeKindFlag {
		t.Fatalf("flag not armed: pending=%s kind=%s", st.PendingBreak, st.PendingKind)
	}
	sigs := feed1m(s, f, 11, 10, down5m(lo-2000, lo-2400))
	sig := sigs[4]
	if sig == nil || sig.Action != ActionBuy || sig.Side != SidePut || !strings.Contains(sig.Reason, RangeKindFlag) {
		t.Fatalf("expected FLAG PUT at the 11:14 close, got %+v", sigs)
	}
	// Target: half the pole (day open 24,100 → box mid 23,810) below the box low.
	wantTarget := lo - (pts(24100)-(lo+hi)/2)*50/100
	// Box edges include 1m wicks, so allow a few points either way.
	if absInt64(st.TargetLevel-wantTarget) > 1000 || st.TargetLevel >= lo || st.Kind != RangeKindFlag {
		t.Fatalf("target=%d want %d kind=%s", st.TargetLevel, wantTarget, st.Kind)
	}
}

func TestFlagIgnoresBreakAgainstTrend(t *testing.T) {
	s, f := newWarmRange(t, flagConfig())
	lo, hi := pts(23790), pts(23830)
	flagDay(t, s, f, lo, hi)
	feed1m(s, f, 11, 5, up5m(pts(23800), hi+2000)) // up-break on a down day
	if st := s.instruments["NSE:NIFTY"]; st.PendingBreak != SideNone {
		t.Fatalf("counter-trend break armed: %s", st.PendingBreak)
	}
}

func TestFlagNeedsTightBox(t *testing.T) {
	cfg := flagConfig()
	cfg.FlagMaxWidthBps = 10 // 0.10% ≈ 24 pts; the 40-pt box is too wide
	s, f := newWarmRange(t, cfg)
	lo, hi := pts(23790), pts(23830)
	flagDay(t, s, f, lo, hi)
	feed1m(s, f, 11, 5, down5m(pts(23820), lo-2000))
	if st := s.instruments["NSE:NIFTY"]; st.PendingBreak != SideNone {
		t.Fatal("wide box armed a flag")
	}
}

func TestFlagOff(t *testing.T) {
	cfg := flagConfig()
	cfg.FlagEnabled = false
	s, f := newWarmRange(t, cfg)
	lo, hi := pts(23790), pts(23830)
	flagDay(t, s, f, lo, hi)
	feed1m(s, f, 11, 5, down5m(pts(23820), lo-2000))
	if st := s.instruments["NSE:NIFTY"]; st.PendingBreak != SideNone {
		t.Fatal("flag armed while disabled")
	}
}
