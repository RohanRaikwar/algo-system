package orderexec

import (
	"strings"
	"testing"

	"trading-systemv1/internal/model"
)

const sensexSym, sensexTok = "SENSEX26N0564600CE", "1163095"

// sensexSig is a SENSEX_SR entry on a resolved BFO contract.
func sensexSig(action model.Action, side model.PositionSide) model.Signal {
	return model.Signal{StrategyName: "SENSEX_SR", Action: action, Side: side,
		Token: "99919000", Exchange: "BSE", FNOToken: sensexTok, FNOSymbol: sensexSym}
}

func sensexLive(t *testing.T, f *fakeAPI, live ...string) *OrderExecutor {
	t.Helper()
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	oe := liveExecutor(t, f)
	oe.lotSizeOf = func(sym string) (int64, error) {
		if strings.HasPrefix(sym, "SENSEX") {
			return 20, nil
		}
		return 75, nil
	}
	for _, s := range live {
		oe.liveStrategies[s] = true
	}
	return oe
}

// A real SENSEX entry goes to BFO, sized in SENSEX lots, and its stop too.
func TestSensex_RealEntryAndStopOnBFO(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 300.0}
	oe := sensexLive(t, f, "SENSEX_SR")
	oe.cfg.ProtectionMode = ProtectionStop
	oe.ExecuteSignal(sensexSig(model.ActionBuy, model.SideCall))
	if n := f.placedCount(); n < 1 {
		t.Fatalf("no real BUY placed")
	}
	buy := f.placedAt(0)
	if buy["exchange"] != "BFO" || buy["tradingsymbol"] != sensexSym || buy["quantity"] != "20" {
		t.Fatalf("BUY params = %v", buy)
	}
	waitFor(t, "protective stop", func() bool { return len(f.placedStops()) == 1 })
	if st := f.placedStops()[0]; st["exchange"] != "BFO" || st["quantity"] != "20" {
		t.Fatalf("stop params = %v", st)
	}
}

// NIFTY orders keep NFO when SENSEX is configured.
func TestSensex_NiftyOrdersUnchanged(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := sensexLive(t, f)
	oe.ExecuteSignal(realSig(model.ActionBuy))
	if n := f.placedCount(); n < 1 {
		t.Fatalf("no NIFTY BUY")
	}
	if p := f.placedAt(0); p["exchange"] != "NFO" || p["quantity"] != "75" {
		t.Fatalf("NIFTY BUY params = %v", p)
	}
}

// SENSEX_SR off the allowlist is paper, sized in SENSEX lots.
func TestSensex_PaperWhenNotAllowlisted(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 300.0}
	oe := sensexLive(t, f)
	s := sensexSig(model.ActionBuy, model.SideCall)
	oe.ExecuteSignal(s)
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d real orders for paper SENSEX_SR", n)
	}
	e, ok := entryFor(oe, s)
	if !ok || e.Real || !strings.HasPrefix(e.OrderID, "PAPER_") || e.Quantity != 20 {
		t.Fatalf("paper entry = %+v ok=%v", e, ok)
	}
}

// SENSEX's own freeze quantity applies.
func TestSensex_FreezeQty(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 300.0}
	oe := sensexLive(t, f, "SENSEX_SR")
	oe.cfg.LiveLots = 60 // 60 × 20 = 1200 > SENSEX freeze 1000 (< NIFTY's 1800)
	oe.ExecuteSignal(sensexSig(model.ActionBuy, model.SideCall))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d above SENSEX freeze qty", n)
	}
}

// A real NIFTY CALL does not block a real SENSEX PUT.
func TestSensex_OppositeGuardPerUnderlying(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := sensexLive(t, f, "SENSEX_SR")
	oe.ExecuteSignal(realSig(model.ActionBuy)) // NIFTY50_FNO CALL
	before := f.placedCount()
	oe.ExecuteSignal(sensexSig(model.ActionBuy, model.SidePut))
	waitFor(t, "SENSEX PUT", func() bool {
		for i := before; i < f.placedCount(); i++ {
			if p := f.placedAt(i); p["exchange"] == "BFO" && p["transactiontype"] == "BUY" {
				return true
			}
		}
		return false
	})
}

// The account-wide limit sums every live strategy.
func TestDayLossTotal_SumsLiveStrategies(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 300.0}
	oe := sensexLive(t, f, "SENSEX_SR")
	oe.cfg.MaxDayLossTotalPaise = 100000
	pnl := NewMockPnLTracker()
	pnl.SetStrategyPnL("NIFTY50_FNO", -70000)
	pnl.SetStrategyPnL("SENSEX_SR", -40000)
	oe.SetPnLTracker(pnl)
	alerts := alertSink(oe)
	oe.ExecuteSignal(sensexSig(model.ActionBuy, model.SideCall))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d with account loss -110000 over -100000", n)
	}
	if !strings.Contains(alerts(), "DAY LOSS LIMIT") {
		t.Fatalf("no alert: %q", alerts())
	}
}

// Reconcile keys BFO positions apart from NFO ones with the same token.
func TestSensex_ReconcileMixedSegments(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 300.0}
	oe := sensexLive(t, f, "SENSEX_SR")
	oe.ExecuteSignal(sensexSig(model.ActionBuy, model.SideCall))
	waitFor(t, "entry settled", func() bool {
		e, ok := entryFor(oe, sensexSig(model.ActionBuy, model.SideCall))
		return ok && e.Real && !e.Pending
	})
	f.mu.Lock()
	f.positions = []map[string]any{
		{"symboltoken": sensexTok, "tradingsymbol": sensexSym, "exchange": "BFO", "netqty": "20"},
	}
	f.mu.Unlock()
	mm, err := oe.ReconcilePositions()
	if err != nil || len(mm) != 0 {
		t.Fatalf("mismatches %+v err %v", mm, err)
	}
	// The same token on NFO is someone else's position, not ours.
	f.mu.Lock()
	f.positions[0]["exchange"] = "NFO"
	f.mu.Unlock()
	mm, _ = oe.ReconcilePositions()
	if len(mm) != 2 {
		t.Fatalf("want BFO missing + NFO unknown, got %+v", mm)
	}
}
