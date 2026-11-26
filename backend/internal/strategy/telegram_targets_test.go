package strategy

import (
	"strings"
	"testing"
	"time"
)

// 3 lots, 2 sold at T1, the last lot rides to the last target.
func targetsTG(t *testing.T, targets []int64) *TelegramSignal {
	t.Helper()
	s := NewTelegramSignal("", 65, TelegramSignalConfig{MaxChasePct: 5, MaxPriceGapPct: 40, LadderTrailPct: 10,
		ArmFor: 30 * time.Minute, CallTargets: true, Lots: 3, T1ExitLots: 2})
	a := tgArm()
	a.Call.TriggerPaise, a.Call.SLPaise, a.Call.Targets = 17200, 16600, targets
	if _, err := s.Arm(a); err != nil {
		t.Fatal(err)
	}
	sig := s.OnTick(tgTick("45001", 17200, time.Minute))
	if sig == nil || sig.Action != ActionBuy || sig.Lots != 3 {
		t.Fatalf("entry %+v", sig)
	}
	return s
}

func TestTGPartialAtT1ThenLastTarget(t *testing.T) {
	s := targetsTG(t, []int64{18400, 19400, 20400})
	if p := s.Position(); p.Target != 18400 || p.Lots != 3 || p.LotsLeft != 3 {
		t.Fatalf("entry position %+v", p)
	}
	var events []string
	s.SetOnEvent(func(_, e, _ string) { events = append(events, e) })

	sig := s.OnTick(tgTick("45001", 18450, 2*time.Minute))
	if sig == nil || sig.Action != ActionExit || sig.ExitLots != 2 || !strings.HasPrefix(sig.Reason, "TG T1") {
		t.Fatalf("T1 exit %+v", sig)
	}
	p := s.Position()
	if p == nil || p.LotsLeft != 1 || !p.T1Done || p.Target != 20400 || p.SL != 17200 {
		t.Fatalf("after T1 %+v", p)
	}
	if strings.Join(events, ",") != "SL_RAISED,PARTIAL_EXIT" {
		t.Fatalf("events %v", events)
	}
	// T2 sells nothing; the ladder raises the stop to T1.
	if sig := s.OnTick(tgTick("45001", 19400, 3*time.Minute)); sig != nil {
		t.Fatalf("exit at T2: %+v", sig)
	}
	if s.Position().SL != 18400 {
		t.Fatalf("stop after T2 %d", s.Position().SL)
	}
	sig = s.OnTick(tgTick("45001", 20400, 4*time.Minute))
	if sig == nil || sig.ExitLots != 0 || !strings.HasPrefix(sig.Reason, "TG TARGET") || s.Position() != nil {
		t.Fatalf("last target exit %+v", sig)
	}
}

func TestTGStopAfterT1SellsTheRest(t *testing.T) {
	s := targetsTG(t, []int64{18400, 19400})
	if sig := s.OnTick(tgTick("45001", 18400, 2*time.Minute)); sig == nil || sig.ExitLots != 2 {
		t.Fatalf("T1 %+v", sig)
	}
	// Back to cost: the runner exits whole.
	sig := s.OnTick(tgTick("45001", 17200, 3*time.Minute))
	if sig == nil || sig.ExitLots != 0 || !strings.HasPrefix(sig.Reason, "TG RAISED STOP") || s.Position() != nil {
		t.Fatalf("stop exit %+v", sig)
	}
}

func TestTGOneTargetExitsAll(t *testing.T) {
	s := targetsTG(t, []int64{18400})
	sig := s.OnTick(tgTick("45001", 18400, 2*time.Minute))
	if sig == nil || sig.ExitLots != 0 || !strings.HasPrefix(sig.Reason, "TG TARGET") || s.Position() != nil {
		t.Fatalf("single target %+v", sig)
	}
}

// A restored trade from before lots (Lots 0) exits whole at its target.
func TestTGRestoredOldTradeNoPartial(t *testing.T) {
	s := targetsTG(t, []int64{18400, 19400})
	p := *s.Position()
	p.Lots, p.LotsLeft = 0, 0
	s.Restore(p)
	sig := s.OnTick(tgTick("45001", 18400, 2*time.Minute))
	if sig == nil || sig.ExitLots != 0 || s.Position() != nil {
		t.Fatalf("old trade %+v", sig)
	}
}

// A target set by hand stays when T1's lots are sold.
func TestTGHandTargetKeptAfterT1(t *testing.T) {
	s := targetsTG(t, []int64{18400, 19400})
	s.OnTick(tgTick("45001", 18000, 2*time.Minute))
	if err := s.SetLevels(SideCall, "", LevelEdit{FNOTarget: 25000}); err != nil {
		t.Fatal(err)
	}
	if sig := s.OnTick(tgTick("45001", 18400, 3*time.Minute)); sig == nil || sig.ExitLots != 2 {
		t.Fatalf("T1 %+v", sig)
	}
	if s.Position().Target != 25000 {
		t.Fatalf("hand target %d", s.Position().Target)
	}
}
