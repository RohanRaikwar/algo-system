package strategy

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func icTestConfig() Nifty50RangeICConfig {
	cfg := DefaultNifty50RangeICConfig()
	cfg.IndexToken = ""
	cfg.RequireSqueeze = false // the synthetic 200-pt wave is too wide for a squeeze
	cfg.MaxDTE = 5             // fixtures use a 4-DTE expiry
	cfg.EntryWindow = timeWindow{From: hhmm(9, 45), To: hhmm(13, 0)}
	return cfg
}

// newWarmIC warms a condor with 3 range days; day 4 is Friday 2026-01-09,
// expiry Tuesday 2026-01-13 (4 DTE).
func newWarmIC(t *testing.T, cfg Nifty50RangeICConfig) (*Nifty50RangeIC, *rangeFeed) {
	t.Helper()
	s := NewNifty50RangeIC(1, cfg)
	f := newRangeFeed()
	s.Warmup(f.warmDays(3))
	f.day = time.Date(2026, 1, 9, 0, 0, 0, 0, ist)
	s.SetExpiry(time.Date(2026, 1, 13, 0, 0, 0, 0, ist))
	return s, f
}

func runICUntil(s *Nifty50RangeIC, f *rangeFeed, h, m int) []Signal {
	var out []Signal
	for _, ts := range sessionMinutes(f.day) {
		if minutesIST(ts) >= hhmm(h, m) {
			break
		}
		if sig := s.OnTFCandle(tfc(ts, f.bar(f.next()))); sig != nil {
			out = append(out, *sig)
		}
	}
	return out
}

func openCondor(t *testing.T, s *Nifty50RangeIC, f *rangeFeed) Signal {
	t.Helper()
	sigs := runICUntil(s, f, 10, 30)
	if len(sigs) != 1 {
		t.Fatalf("want exactly one entry by 10:30, got %+v", sigs)
	}
	return sigs[0]
}

func TestCondorStrikesMatchGuide(t *testing.T) {
	s := NewNifty50RangeIC(1, icTestConfig())
	sc, lc, sp, lp := s.condorStrikes(pts(23900), pts(24100))
	if sc != 24200 || lc != 24300 || sp != 23800 || lp != 23700 {
		t.Fatalf("strikes %d/%d %d/%d", sc, lc, sp, lp)
	}
	// Levels off the 50-grid round outward.
	sc, _, sp, _ = s.condorStrikes(pts(23897), pts(24103))
	if sc != 24250 || sp != 23750 {
		t.Fatalf("rounded strikes %d %d", sc, sp)
	}
}

func TestCondorEntry(t *testing.T) {
	s, f := newWarmIC(t, icTestConfig())
	sig := openCondor(t, s, f)
	if sig.Action != ActionBuy || sig.StrategyName != "NIFTY50_RANGE_IC" || len(sig.Legs) != 4 {
		t.Fatalf("entry = %+v", sig)
	}
	shorts := 0
	for _, l := range sig.Legs {
		if l.Short {
			shorts++
		}
	}
	if shorts != 2 {
		t.Errorf("shorts = %d", shorts)
	}
	// One condor per day, even after it closes.
	s.CancelBasket("test")
	if more := runICUntil(s, f, 12, 59); len(more) != 0 {
		t.Fatalf("second condor same day: %+v", more)
	}
}

func TestCondorGatedByDTE(t *testing.T) {
	s, f := newWarmIC(t, icTestConfig())
	s.SetExpiry(time.Date(2026, 1, 20, 0, 0, 0, 0, ist)) // 11 DTE
	if sigs := runICUntil(s, f, 12, 59); len(sigs) != 0 {
		t.Fatalf("entry outside DTE window: %+v", sigs)
	}
	s2, f2 := newWarmIC(t, icTestConfig())
	s2.SetExpiry(time.Time{}) // unknown expiry
	if sigs := runICUntil(s2, f2, 12, 59); len(sigs) != 0 {
		t.Fatalf("entry without expiry: %+v", sigs)
	}
}

func TestCondorGatedByADXAndSqueeze(t *testing.T) {
	cfg := icTestConfig()
	cfg.MaxADX = 1
	s, f := newWarmIC(t, cfg)
	if sigs := runICUntil(s, f, 12, 59); len(sigs) != 0 {
		t.Fatalf("entry above ADX cap: %+v", sigs)
	}
	cfg = icTestConfig()
	cfg.RequireSqueeze = true
	s, f = newWarmIC(t, cfg)
	if sigs := runICUntil(s, f, 12, 59); len(sigs) != 0 {
		t.Fatalf("entry without squeeze: %+v", sigs)
	}
}

func legTokens() map[string]string {
	return map[string]string{LegLongCE: "NFO:1", LegShortCE: "NFO:2", LegLongPE: "NFO:3", LegShortPE: "NFO:4"}
}

func tick(tok string, p int64) model.Tick { return model.Tick{Token: tok, Exchange: "NFO", Price: p} }

// priceLegs opens at short 100/100, long 40/40 → credit 120 per unit.
func priceLegs(s *Nifty50RangeIC) {
	s.OnTick(tick("1", 4000))
	s.OnTick(tick("2", 10000))
	s.OnTick(tick("3", 4000))
	s.OnTick(tick("4", 10000))
}

func TestCondorProfitTarget(t *testing.T) {
	s, f := newWarmIC(t, icTestConfig())
	openCondor(t, s, f)
	s.SetLegTokens(legTokens())
	priceLegs(s)
	// Shorts decay to 70 each, longs to 30: debit 80 > 60 → hold.
	s.OnTick(tick("1", 3000))
	s.OnTick(tick("3", 3000))
	if sig := s.OnTick(tick("2", 7000)); sig != nil {
		t.Fatalf("early exit: %+v", sig)
	}
	if sig := s.OnTick(tick("4", 7000)); sig != nil {
		t.Fatalf("early exit at debit 80: %+v", sig)
	}
	// Shorts to 60: debit 60 = 50% of credit 120.
	s.OnTick(tick("2", 6000))
	sig := s.OnTick(tick("4", 6000))
	if sig == nil || sig.Action != ActionExit || len(sig.Legs) != 4 || !strings.Contains(sig.Reason, "PROFIT") {
		t.Fatalf("expected profit exit, got %+v", sig)
	}
	for _, l := range sig.Legs {
		if l.Token == "" {
			t.Errorf("exit leg %s has no token", l.Leg)
		}
	}
	if s.OpenLegs() != nil {
		t.Error("condor still open")
	}
}

func TestCondorStopLoss(t *testing.T) {
	s, f := newWarmIC(t, icTestConfig())
	openCondor(t, s, f)
	s.SetLegTokens(legTokens())
	priceLegs(s)
	// CE side rallies. Short CE to 200: debit 200+100-40-40 = 220 < 240.
	if sig := s.OnTick(tick("2", 20000)); sig != nil {
		t.Fatalf("stop too early at debit 220: %+v", sig)
	}
	// Long CE wing to 100: debit 200+100-100-40 = 160.
	if sig := s.OnTick(tick("1", 10000)); sig != nil {
		t.Fatalf("stop too early at debit 160: %+v", sig)
	}
	// Short CE to 280: debit 280+100-100-40 = 240 = 2× credit.
	sig := s.OnTick(tick("2", 28000))
	if sig == nil || !strings.Contains(sig.Reason, "STOP") {
		t.Fatalf("expected stop, got %+v", sig)
	}
}

func TestCondorBreachAndTimeExit(t *testing.T) {
	s, f := newWarmIC(t, icTestConfig())
	entry := openCondor(t, s, f)
	var shortCE int64
	for _, l := range entry.Legs {
		if l.Leg == LegShortCE {
			shortCE = l.Strike
		}
	}
	p := shortCE*100 + 500
	sig := s.OnTFCandle(tfc(day4(f, 10, 30), ohlcv{Open: p, High: p, Low: p, Close: p}))
	if sig == nil || !strings.Contains(sig.Reason, "BREACH") || len(sig.Legs) != 4 {
		t.Fatalf("expected breach exit, got %+v", sig)
	}

	s2, f2 := newWarmIC(t, icTestConfig())
	openCondor(t, s2, f2)
	mid := pts(24000)
	sig = s2.OnTFCandle(tfc(day4(f2, 14, 59), ohlcv{Open: mid, High: mid, Low: mid, Close: mid}))
	if sig == nil || !strings.Contains(sig.Reason, "TIME EXIT") {
		t.Fatalf("expected time exit, got %+v", sig)
	}
	s3, f3 := newWarmIC(t, icTestConfig())
	openCondor(t, s3, f3)
	if out := s3.ForceExitAll("EOD"); len(out) != 1 || len(out[0].Legs) != 4 || out[0].Action != ActionExit {
		t.Fatalf("ForceExitAll = %+v", out)
	}
}

func TestCondorSnapshotRoundTrip(t *testing.T) {
	s, f := newWarmIC(t, icTestConfig())
	openCondor(t, s, f)
	s.SetLegTokens(legTokens())
	priceLegs(s)
	data, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r := NewNifty50RangeIC(1, icTestConfig())
	if err := r.Restore(data); err != nil {
		t.Fatal(err)
	}
	legs := r.OpenLegs()
	if len(legs) != 4 || legs[1].Token != "2" {
		t.Fatalf("restored legs = %+v", legs)
	}
	st := r.instruments["NSE:NIFTY"]
	if c, _, ok := st.creditDebit(); !ok || c != 12000 {
		t.Fatalf("restored credit = %d ok=%v", c, ok)
	}
}

func TestDaysToExpiry(t *testing.T) {
	ts := time.Date(2026, 1, 9, 15, 0, 0, 0, ist)
	if d := daysToExpiry(ts, time.Date(2026, 1, 13, 0, 0, 0, 0, ist)); d != 4 {
		t.Fatalf("dte = %d", d)
	}
	if d := daysToExpiry(ts, time.Time{}); d != -1 {
		t.Fatalf("zero expiry dte = %d", d)
	}
}

func TestCondorMinCredit(t *testing.T) {
	s := NewNifty50RangeIC(1, icTestConfig())
	// 100-pt wings × 20% = 20 pts = 2000 paise per unit.
	if got := s.MinCredit(); got != 2000 {
		t.Fatalf("MinCredit = %d", got)
	}
}

func TestCondorDefaultsTradeDayBeforeExpiry(t *testing.T) {
	cfg := DefaultNifty50RangeICConfig()
	if cfg.MinDTE != 1 || cfg.MaxDTE != 1 || !cfg.Range.RequireFlatEMA {
		t.Fatalf("defaults: dte %d-%d flatEMA=%v", cfg.MinDTE, cfg.MaxDTE, cfg.Range.RequireFlatEMA)
	}
	cfg.IndexToken, cfg.RequireSqueeze = "", false
	s, f := newWarmIC(t, cfg) // expiry 4 DTE
	if sigs := runICUntil(s, f, 12, 59); len(sigs) != 0 {
		t.Fatalf("4-DTE condor opened with 1-DTE default: %+v", sigs)
	}
	s2, f2 := newWarmIC(t, cfg)
	s2.SetExpiry(time.Date(2026, 1, 10, 0, 0, 0, 0, ist)) // 1 DTE
	if sigs := runICUntil(s2, f2, 11, 0); len(sigs) != 1 {
		t.Fatalf("1-DTE condor: %+v", sigs)
	}
}
