package exitpolicy

import (
	"strings"
	"testing"
	"time"
)

func thetaPlan(mode Mode, confirm int) Plan {
	p := thetaParams()
	p.ConfirmMin = confirm
	return Plan{Mode: mode, Rules: []Rule{ThetaRule{P: p}}}
}

func noArm(Position) bool { return false }

// crush feeds an index flat at entry and a premium crushed to ₹86.
func crush(a *Arbiter) {
	a.OnTick("26000", 2500000)
	a.OnTick("12345", 10000)
	a.OnTick("12345", 8600)
}

func TestArbiterDecidesOnceAndLatches(t *testing.T) {
	a := NewArbiter()
	a.Open(callPos(), thetaPlan(ModeAct, 1))
	crush(a)
	got := a.OnMinute(t0.Add(15*time.Minute), noArm)
	if len(got) != 1 || got[0].Reason != ReasonTheta || got[0].Shadow || got[0].Position.Key != "NIFTY50_SR|CALL" {
		t.Fatalf("got %+v", got)
	}
	if again := a.OnMinute(t0.Add(16*time.Minute), noArm); len(again) != 0 {
		t.Fatalf("latched position decided again: %+v", again)
	}
}

func TestArbiterConfirmNeedsConsecutiveMinutes(t *testing.T) {
	a := NewArbiter()
	a.Open(callPos(), thetaPlan(ModeAct, 2))
	crush(a)
	if got := a.OnMinute(t0.Add(15*time.Minute), noArm); len(got) != 0 {
		t.Fatalf("fired before confirm: %+v", got)
	}
	a.OnTick("12345", 10000) // recovers: streak resets
	if got := a.OnMinute(t0.Add(16*time.Minute), noArm); len(got) != 0 {
		t.Fatalf("fired while recovered: %+v", got)
	}
	a.OnTick("12345", 8600)
	if got := a.OnMinute(t0.Add(17*time.Minute), noArm); len(got) != 0 {
		t.Fatalf("streak not reset: %+v", got)
	}
	if got := a.OnMinute(t0.Add(18*time.Minute), noArm); len(got) != 1 {
		t.Fatalf("want fire on 2nd consecutive minute, got %+v", got)
	}
}

func TestArbiterShadowAndClose(t *testing.T) {
	a := NewArbiter()
	a.Open(callPos(), thetaPlan(ModeShadow, 1))
	crush(a)
	got := a.OnMinute(t0.Add(15*time.Minute), noArm)
	if len(got) != 1 || !got[0].Shadow {
		t.Fatalf("want shadow decision, got %+v", got)
	}

	b := NewArbiter()
	b.Open(callPos(), thetaPlan(ModeAct, 1))
	crush(b)
	b.Close("NIFTY50_SR|CALL")
	if got := b.OnMinute(t0.Add(15*time.Minute), noArm); len(got) != 0 {
		t.Fatalf("closed position decided: %+v", got)
	}
	if b.Open(callPos(), Plan{Mode: ModeOff}); b.Len() != 0 {
		t.Fatal("off plan must not track")
	}
}

func TestArbiterProtectArmedAndReopen(t *testing.T) {
	a := NewArbiter()
	a.Open(callPos(), thetaPlan(ModeAct, 1))
	crush(a)
	armed := func(p Position) bool { return p.Side == "CALL" }
	if got := a.OnMinute(t0.Add(15*time.Minute), armed); len(got) != 0 {
		t.Fatalf("armed trade cut: %+v", got)
	}
	// Re-open on the same key resets best/LTP and the latch.
	a.Open(callPos(), thetaPlan(ModeAct, 1))
	if got := a.OnMinute(t0.Add(15*time.Minute), noArm); len(got) != 0 {
		t.Fatalf("no premium seen after reopen yet: %+v", got)
	}
}

func TestArbiterPremBestTimeMovesOnlyOnNewBest(t *testing.T) {
	p := thetaParams()
	p.GivebackMin = 30
	a := NewArbiter()
	a.Open(callPos(), Plan{Mode: ModeAct, Rules: []Rule{ThetaRule{P: p}}})
	a.OnTick("26000", 2500000)
	a.OnTick("12345", 12000) // worked: +₹20 at minute 10
	if got := a.OnMinute(t0.Add(10*time.Minute), noArm); len(got) != 0 {
		t.Fatalf("decided while holding gain: %+v", got)
	}
	a.OnTick("12345", 11000) // lower, not a new best
	a.OnTick("12345", 9500)
	if got := a.OnMinute(t0.Add(39*time.Minute), noArm); len(got) != 0 {
		t.Fatalf("best is 29m old, decided early: %+v", got)
	}
	got := a.OnMinute(t0.Add(40*time.Minute), noArm)
	if len(got) != 1 || !strings.Contains(got[0].Text, "gave back") {
		t.Fatalf("want gave-back exit 30m after the best, got %+v", got)
	}
}
