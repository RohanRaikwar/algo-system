package stratengine

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/exitpolicy"
	"trading-systemv1/internal/strategy"
)

type fakeOwner struct {
	fakeExiter
	armed bool
	calls int
}

func (f *fakeOwner) ExitRequested(side strategy.PositionSide, fnoToken, reason string) *strategy.Signal {
	f.calls++
	return f.fakeExiter.ExitRequested(side, fnoToken, reason)
}

func (f *fakeOwner) ProtectArmed(strategy.PositionSide) bool { return f.armed }

var epT0 = time.Date(2026, 10, 7, 10, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))

// policySvc: SR acts, RANGE shadows, confirm 1 minute; sent signals are
// captured instead of going to the engine.
func policySvc(t *testing.T, owners map[string]*fakeOwner) (*Service, *[]strategy.Signal) {
	t.Helper()
	cfg := exitpolicy.DefaultConfig()
	for name, sc := range cfg.Strategies {
		sc.Theta.ConfirmMin = 1
		cfg.Strategies[name] = sc
	}
	var sent []strategy.Signal
	svc := &Service{}
	svc.exitPol.init(cfg, nil)
	svc.exitPol.sendHook = func(s strategy.Signal) { sent = append(sent, s) }
	svc.exitPol.testOwners = map[string]exitPolicyOwner{}
	for name, o := range owners {
		svc.exitPol.testOwners[name] = o
	}
	return svc, &sent
}

func srEntry(name string, side strategy.PositionSide) strategy.Signal {
	return strategy.Signal{StrategyName: name, Action: strategy.ActionBuy, Side: side,
		Token: "99926000", Exchange: "NSE", TargetMove: 10000}
}

// openFlatCrushed opens a CALL at ₹100 (delta 0.5, target 100 pts) with the
// index flat and the premium crushed to ₹86 — a theta exit 15 minutes on.
func openFlatCrushed(svc *Service, name string) {
	sig := srEntry(name, strategy.SideCall)
	svc.noteEntryGreeks(name, 0.5, -15)
	svc.openExitPolicy(sig, name+"|CALL", "NFO:45001", 10000, 2500000, epT0)
	svc.exitPol.arbiter.OnTick("99926000", 2500000)
	svc.exitPol.arbiter.OnTick("45001", 8600)
}

func TestExitPolicyActsThroughOwner(t *testing.T) {
	sr := &fakeOwner{fakeExiter: fakeExiter{open: true}}
	svc, sent := policySvc(t, map[string]*fakeOwner{"NIFTY50_SR": sr})
	openFlatCrushed(svc, "NIFTY50_SR")

	svc.evaluateExitPolicy(epT0.Add(15 * time.Minute))
	if sr.calls != 1 || sr.fnoToken != "45001" || !strings.HasPrefix(sr.why, "THETA CALL flat 15m") {
		t.Fatalf("owner call: %+v", sr)
	}
	if len(*sent) != 1 || (*sent)[0].Action != strategy.ActionExit || !strings.HasPrefix((*sent)[0].Reason, "THETA") {
		t.Fatalf("sent %+v", *sent)
	}
	svc.evaluateExitPolicy(epT0.Add(16 * time.Minute))
	if sr.calls != 1 || len(*sent) != 1 {
		t.Fatal("decided twice")
	}
}

func TestExitPolicyShadowDoesNotExit(t *testing.T) {
	rg := &fakeOwner{fakeExiter: fakeExiter{open: true}}
	svc, sent := policySvc(t, map[string]*fakeOwner{"PAPER_OTHER": rg})
	openFlatCrushed(svc, "PAPER_OTHER")
	svc.evaluateExitPolicy(epT0.Add(15 * time.Minute))
	if rg.calls != 0 || len(*sent) != 0 {
		t.Fatalf("shadow acted: calls=%d sent=%+v", rg.calls, *sent)
	}
}

func TestExitPolicyStrategyExitFirstCloses(t *testing.T) {
	sr := &fakeOwner{fakeExiter: fakeExiter{open: true}}
	svc, sent := policySvc(t, map[string]*fakeOwner{"NIFTY50_SR": sr})
	openFlatCrushed(svc, "NIFTY50_SR")
	svc.closeExitPolicy("NIFTY50_SR|CALL")
	svc.evaluateExitPolicy(epT0.Add(15 * time.Minute))
	if sr.calls != 0 || len(*sent) != 0 {
		t.Fatal("closed position exited again")
	}
}

func TestExitPolicyArmedOwnerKeepsTrade(t *testing.T) {
	sr := &fakeOwner{fakeExiter: fakeExiter{open: true}, armed: true}
	svc, sent := policySvc(t, map[string]*fakeOwner{"NIFTY50_SR": sr})
	openFlatCrushed(svc, "NIFTY50_SR")
	svc.evaluateExitPolicy(epT0.Add(15 * time.Minute))
	if len(*sent) != 0 {
		t.Fatalf("armed trade cut: %+v", *sent)
	}
}

func TestExitPolicyOwnerAlreadyFlat(t *testing.T) {
	sr := &fakeOwner{fakeExiter: fakeExiter{open: false}}
	svc, sent := policySvc(t, map[string]*fakeOwner{"NIFTY50_SR": sr})
	openFlatCrushed(svc, "NIFTY50_SR")
	svc.evaluateExitPolicy(epT0.Add(15 * time.Minute))
	if sr.calls != 1 || len(*sent) != 0 {
		t.Fatalf("flat owner: calls=%d sent=%+v", sr.calls, *sent)
	}
}

func TestExitPolicyGreeksAreConsumedOnce(t *testing.T) {
	svc, _ := policySvc(t, nil)
	svc.noteEntryGreeks("NIFTY50_SR", 0.5, -15)
	if g := svc.takeEntryGreeks("NIFTY50_SR", 10000); g == nil || g.ExpGainPaise != 5000 {
		t.Fatalf("greeks %+v", g)
	}
	if g := svc.takeEntryGreeks("NIFTY50_SR", 10000); g != nil {
		t.Fatal("greeks reused for a later entry")
	}
}

func TestExitPolicyUnlistedAndLegsUntracked(t *testing.T) {
	svc, _ := policySvc(t, nil)
	svc.openExitPolicy(srEntry("PAPER_BASKET", strategy.SideCall), "PAPER_BASKET|CALL", "NFO:1", 10000, 2500000, epT0)
	leg := srEntry("NIFTY50_SR", strategy.SideCall)
	leg.Leg = "CE_SHORT"
	svc.openExitPolicy(leg, "NIFTY50_SR|CALL", "NFO:1", 10000, 2500000, epT0)
	if n := svc.exitPol.arbiter.Len(); n != 0 {
		t.Fatalf("tracked %d", n)
	}
}
