package exitwatch

import (
	"testing"
	"time"
)

var ist = time.FixedZone("IST", 5*3600+30*60)

// t0 is 10:00 IST so time-to-exit pressure stays at zero.
var t0 = time.Date(2026, 10, 5, 10, 0, 0, 0, ist)

const (
	entryIdx  int64 = 2400000 // 24000.00
	entryPrem int64 = 10000   // 100.00
)

func callPos() Position {
	return Position{
		Strategy: "NIFTY50_SR", Side: "CALL",
		IndexEntry: entryIdx, TargetLevel: entryIdx + 4000, StopLevel: entryIdx - 2000,
		FNOToken: "OPT1", FNOEntryPrice: entryPrem, EntryTS: t0,
	}
}

func putPos() Position {
	return Position{
		Strategy: "NIFTY50_SR", Side: "PUT",
		IndexEntry: entryIdx, TargetLevel: entryIdx - 4000, StopLevel: entryIdx + 2000,
		FNOToken: "OPT2", FNOEntryPrice: entryPrem, EntryTS: t0,
	}
}

// pathFn returns the favourable index move in paise at elapsed time.
type pathFn func(elapsed time.Duration) int64

// run feeds index + option ticks every 250ms for dur and returns every
// assessment. Option premium follows a 0.5 delta on the favourable move.
func run(t *testing.T, pos Position, dur time.Duration, path pathFn, withQuote bool) []Assessment {
	t.Helper()
	tr, err := NewTracker(pos, DefaultParams().Compile())
	if err != nil {
		t.Fatal(err)
	}
	tr.SetATR(1500)
	sign := int64(1)
	if pos.Side == "PUT" {
		sign = -1
	}
	var out []Assessment
	for el := time.Duration(0); el <= dur; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		move := path(el)
		tr.OnIndexTick(pos.IndexEntry+sign*move, ts)
		prem := pos.FNOEntryPrice + move/2
		var bid, ask, oi int64
		if withQuote {
			bid, ask, oi = prem-5, prem+5, 100000
		}
		tr.OnOptionTick(prem, bid, ask, oi, ts)
		out = append(out, tr.Evaluate(ts))
	}
	return out
}

func firstExit(as []Assessment) (Assessment, bool) {
	for _, a := range as {
		if a.Decision == DecisionExit {
			return a, true
		}
	}
	return Assessment{}, false
}

// rise to 80% of target in 60s, stall 20s, then fall to -10pts by 140s.
func stallReverse(el time.Duration) int64 {
	s := el.Seconds()
	switch {
	case s <= 60:
		return int64(3200 * s / 60)
	case s <= 80:
		return 3200
	default:
		return 3200 - int64(4200*(s-80)/60)
	}
}

func TestStallThenReverseExitsBeforeLoss(t *testing.T) {
	for _, pos := range []Position{callPos(), putPos()} {
		t.Run(pos.Side, func(t *testing.T) {
			as := run(t, pos, 140*time.Second, stallReverse, true)
			ex, ok := firstExit(as)
			if !ok {
				t.Fatalf("expected EXIT, got none; last=%+v", as[len(as)-1])
			}
			if ex.PremiumLTP < pos.FNOEntryPrice {
				t.Fatalf("EXIT too late: premium %d below entry %d (reason %v)", ex.PremiumLTP, pos.FNOEntryPrice, ex.Reasons)
			}
			if !as[len(as)-1].Latched || as[len(as)-1].Decision != DecisionExit {
				t.Fatalf("EXIT must latch")
			}
		})
	}
}

// clean trend to target over 180s with a 3pt pullback every 20s.
func cleanTrend(el time.Duration) int64 {
	s := el.Seconds()
	base := int64(4000 * s / 180)
	if int(s)%20 >= 17 {
		base -= 300
	}
	return base
}

func TestCleanTrendHolds(t *testing.T) {
	as := run(t, callPos(), 180*time.Second, cleanTrend, true)
	if ex, ok := firstExit(as); ok {
		t.Fatalf("false EXIT on clean trend at %v p=%.2f reasons=%v", ex.TS.Sub(t0), ex.P, ex.Reasons)
	}
}

// chop ±5pts around entry: peak progress stays below MinPeakProgress.
func chop(el time.Duration) int64 {
	s := int64(el.Seconds())
	if (s/10)%2 == 0 {
		return 500
	}
	return -500
}

func TestChopNearEntryNoExit(t *testing.T) {
	as := run(t, callPos(), 300*time.Second, chop, true)
	if ex, ok := firstExit(as); ok {
		t.Fatalf("EXIT on chop at %v reasons=%v", ex.TS.Sub(t0), ex.Reasons)
	}
}

func TestMissingQuoteAndATRStillScores(t *testing.T) {
	tr, err := NewTracker(callPos(), DefaultParams().Compile())
	if err != nil {
		t.Fatal(err)
	}
	var a Assessment
	for el := time.Duration(0); el <= 90*time.Second; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		m := stallReverse(el)
		tr.OnIndexTick(entryIdx+m, ts)
		tr.OnOptionTick(entryPrem+m/2, 0, 0, 0, ts)
		a = tr.Evaluate(ts)
	}
	if a.P <= 0 || a.P >= 1 {
		t.Fatalf("p out of range: %v", a.P)
	}
	for _, f := range []Feature{FSpread, FOIChange, FPullbackATR} {
		if a.Snapshot.Present[f] {
			t.Fatalf("%s should be missing", featureNames[f])
		}
	}
}

func TestHysteresisNeedsConfirm(t *testing.T) {
	m := DefaultParams().Compile()
	var d decider
	s := Snapshot{PeakProgress: 0.8, Giveback: 0.1}
	base := t0.UnixNano()
	// 2s above ExitP, then drop: no EXIT.
	for i := 0; i < 8; i++ {
		if dec, _ := d.step(&m, &s, 0.9, true, base+int64(i)*int64(250*time.Millisecond)); dec == DecisionExit {
			t.Fatalf("EXIT before confirm window at step %d", i)
		}
	}
	if dec, _ := d.step(&m, &s, 0.3, true, base+int64(2*time.Second)); dec != DecisionHold {
		t.Fatalf("want HOLD after drop, got %s", dec)
	}
	// sustained above ExitP for > ConfirmSec: EXIT.
	var dec Decision
	for i := 0; i <= 16; i++ {
		dec, _ = d.step(&m, &s, 0.9, true, base+int64(3*time.Second)+int64(i)*int64(250*time.Millisecond))
	}
	if dec != DecisionExit {
		t.Fatalf("want EXIT after sustained score, got %s", dec)
	}
}

func TestExitBlockedWhenPremiumAlreadyLost(t *testing.T) {
	m := DefaultParams().Compile()
	var d decider
	s := Snapshot{PeakProgress: 0.8, Giveback: 0.2}
	base := t0.UnixNano()
	var dec Decision
	for i := 0; i <= 20; i++ {
		dec, _ = d.step(&m, &s, 0.95, false, base+int64(i)*int64(250*time.Millisecond))
	}
	if dec == DecisionExit {
		t.Fatalf("score EXIT must require premium near entry")
	}
}

func TestNewTrackerRejectsBadPosition(t *testing.T) {
	p := callPos()
	p.TargetLevel = p.IndexEntry - 100 // target on wrong side
	if _, err := NewTracker(p, DefaultParams().Compile()); err == nil {
		t.Fatal("expected error for target on wrong side")
	}
	p = callPos()
	p.Side = "NONE"
	if _, err := NewTracker(p, DefaultParams().Compile()); err == nil {
		t.Fatal("expected error for bad side")
	}
}
