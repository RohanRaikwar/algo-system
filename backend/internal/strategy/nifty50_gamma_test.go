package strategy

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestNiftyExpiryDay(t *testing.T) {
	d := func(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 10, 0, 0, 0, ist) }
	if !NiftyExpiryDay(d(2026, 9, 29), nil) { // Tuesday
		t.Fatal("Tuesday is expiry")
	}
	if NiftyExpiryDay(d(2026, 9, 28), nil) || NiftyExpiryDay(d(2026, 10, 2), nil) {
		t.Fatal("Monday / Friday are not expiry in a normal week")
	}
	holidayTue := func(x time.Time) bool {
		return x.Weekday() != time.Saturday && x.Weekday() != time.Sunday && x.Format("2006-01-02") != "2026-09-29"
	}
	if !NiftyExpiryDay(d(2026, 9, 28), holidayTue) || NiftyExpiryDay(d(2026, 9, 29), holidayTue) {
		t.Fatal("holiday Tuesday moves expiry to Monday")
	}
}

func gammaTestConfig() Nifty50GammaConfig {
	c := DefaultNifty50GammaConfig()
	c.IndexToken = ""
	return c
}

// gammaDay warms 3 range days, then feeds expiry Tuesday 2026-01-13 with a
// quiet ±amp oscillation around 24,000 until 13:45 (exclusive).
func gammaDay(t *testing.T, s *Nifty50Gamma, amp int64) (*rangeFeed, time.Time) {
	t.Helper()
	f := newRangeFeed()
	s.Warmup(f.warmDays(3))
	f.day = time.Date(2026, 1, 13, 0, 0, 0, 0, ist)
	f.prev = pts(24000)
	ts := time.Date(2026, 1, 13, 9, 15, 0, 0, ist)
	i := 0
	for ts.Before(time.Date(2026, 1, 13, 13, 45, 0, 0, ist)) {
		p := pts(24000) + amp*int64(i%2*2-1)
		if sig := s.OnTFCandle(tfc(ts, f.bar(p))); sig != nil {
			t.Fatalf("signal before 13:45: %+v", sig)
		}
		ts = ts.Add(time.Minute)
		i++
	}
	return f, ts
}

func feedBars(s *Nifty50Gamma, start time.Time, bars []ohlcv) map[int]*Signal {
	out := map[int]*Signal{}
	for i, b := range bars {
		if sig := s.OnTFCandle(tfc(start.Add(time.Duration(i)*time.Minute), b)); sig != nil {
			out[i] = sig
		}
	}
	return out
}

func openGamma(t *testing.T) (*Nifty50Gamma, time.Time) {
	t.Helper()
	s := NewNifty50Gamma(1, gammaTestConfig())
	_, ts := gammaDay(t, s, 3000) // ±30 pts morning
	st := s.instruments["NSE:NIFTY"]
	hi := st.DayHigh
	if sigs := feedBars(s, ts, up5m(pts(24000), hi+2500)); len(sigs) != 0 {
		t.Fatalf("break bar must only arm: %+v", sigs)
	}
	if !st.SetupOK || st.Pending != SideCall {
		t.Fatalf("setup=%v pending=%s range=%d-%d adx=%.1f", st.SetupOK, st.Pending, st.RangeLo, st.RangeHi, st.ctx.State().ADX)
	}
	sigs := feedBars(s, ts.Add(5*time.Minute), up5m(hi+2500, hi+3500))
	sig := sigs[4]
	if sig == nil || sig.Action != ActionBuy || sig.Side != SideCall || !sig.SameDayExpiry || !strings.Contains(sig.Reason, "GAMMA") {
		t.Fatalf("expected gamma CALL on same-day expiry, got %+v", sigs)
	}
	s.SetPositionToken("NFO:G1")
	s.SetFNOEntryPrice(5000)
	return s, ts.Add(10 * time.Minute)
}

func gtick(p int64) model.Tick { return model.Tick{Token: "G1", Exchange: "NFO", Price: p} }

func TestGammaEntryAndPremiumStop(t *testing.T) {
	s, _ := openGamma(t)
	if sig := s.OnTick(gtick(3600)); sig != nil {
		t.Fatalf("−28%% must hold: %+v", sig)
	}
	if sig := s.OnTick(gtick(3400)); sig == nil || !strings.Contains(sig.Reason, "PREMIUM SL") || sig.Side != SideCall {
		t.Fatalf("expected premium SL, got %+v", sig)
	}
}

func TestGammaPremiumTargetAndTrail(t *testing.T) {
	s, _ := openGamma(t)
	if sig := s.OnTick(gtick(12600)); sig == nil || !strings.Contains(sig.Reason, "TARGET") {
		t.Fatalf("expected +150%% target, got %+v", sig)
	}
	s2, _ := openGamma(t)
	s2.OnTick(gtick(11000)) // +120%: trail armed
	if sig := s2.OnTick(gtick(8000)); sig != nil {
		t.Fatalf("−27%% from best must hold: %+v", sig)
	}
	if sig := s2.OnTick(gtick(7600)); sig == nil || !strings.Contains(sig.Reason, "TRAIL") {
		t.Fatalf("expected trail exit, got %+v", sig)
	}
}

func TestGammaTimeExitAndOneTradePerDay(t *testing.T) {
	s, ts := openGamma(t)
	p := s.instruments["NSE:NIFTY"].RangeHi + 3000
	sig := s.OnTFCandle(tfc(time.Date(2026, 1, 13, 14, 59, 0, 0, ist), ohlcv{Open: p, High: p, Low: p, Close: p}))
	if sig == nil || !strings.Contains(sig.Reason, "TIME EXIT") {
		t.Fatalf("expected 15:00 exit, got %+v", sig)
	}
	_ = ts
	if !s.instruments["NSE:NIFTY"].Traded {
		t.Fatal("day's trade must stay used")
	}
}

func TestGammaSkipsNonExpiryAndBusyMorning(t *testing.T) {
	s := NewNifty50Gamma(1, gammaTestConfig())
	s.isExpiryDay = func(time.Time) bool { return false }
	_, ts := gammaDay(t, s, 3000)
	st := s.instruments["NSE:NIFTY"]
	feedBars(s, ts, up5m(pts(24000), st.DayHigh+2500))
	if st.Pending != SideNone || st.SetupDone {
		t.Fatal("non-expiry day must not set up")
	}
	s2 := NewNifty50Gamma(1, gammaTestConfig())
	_, ts2 := gammaDay(t, s2, 16000) // ±160 pts: 320-pt range > 1%
	st2 := s2.instruments["NSE:NIFTY"]
	feedBars(s2, ts2, up5m(pts(24000), st2.DayHigh+2500))
	if st2.SetupOK || st2.Pending != SideNone {
		t.Fatalf("busy morning must not set up (range %d-%d)", st2.RangeLo, st2.RangeHi)
	}
}

func TestGammaFailedBreakExit(t *testing.T) {
	s, ts := openGamma(t)
	st := s.instruments["NSE:NIFTY"]
	p := st.RangeHi - 3000
	sig := s.OnTFCandle(tfc(ts, ohlcv{Open: st.RangeHi, High: st.RangeHi, Low: p, Close: p}))
	if sig == nil || !strings.Contains(sig.Reason, "FAILED BREAK") {
		t.Fatalf("expected failed-break exit, got %+v", sig)
	}
}

func TestGammaSnapshotRoundTrip(t *testing.T) {
	s, _ := openGamma(t)
	data, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r := NewNifty50Gamma(1, gammaTestConfig())
	if err := r.Restore(data); err != nil {
		t.Fatal(err)
	}
	a, b := s.instruments["NSE:NIFTY"], r.instruments["NSE:NIFTY"]
	if b == nil || a.Side != b.Side || a.FNOEntry != b.FNOEntry || a.RangeHi != b.RangeHi || !b.Traded {
		t.Fatalf("restore mismatch:\n%+v\n%+v", a, b)
	}
}
