package orderexec

import (
	"strings"
	"testing"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// NIFTY50_SR must stay on the paper path even with live orders on and a
// broker session up: only NIFTY50_FNO may reach the broker.
func TestSRSignalsArePaperEvenWhenLive(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.UpdateLTP(model.Tick{Token: "111", Exchange: "NFO", Price: 10000})

	sig := strategy.Signal{StrategyName: "NIFTY50_SR", Action: strategy.ActionBuy, Side: strategy.SideCall,
		Token: "99926000", Exchange: "NSE", Qty: 65}
	oe.ExecuteSignal(sig)
	rec, ok := entryFor(oe, sig)
	if !ok {
		t.Fatal("no paper entry recorded")
	}
	if rec.Real || !strings.HasPrefix(rec.OrderID, "PAPER_") {
		t.Fatalf("entry real=%v id=%s, want paper", rec.Real, rec.OrderID)
	}
	sig.Action = strategy.ActionExit
	oe.ExecuteSignal(sig)
	if n := f.placedCount(); n != 0 {
		t.Fatalf("%d orders reached the broker for NIFTY50_SR", n)
	}
}
