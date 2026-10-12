package orderexec

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

func stopExecutor(t *testing.T, f *fakeAPI) *OrderExecutor {
	t.Helper()
	oe := liveExecutor(t, f)
	oe.cfg.ProtectionMode = ProtectionStop
	old := stopCancelDelays
	stopCancelDelays = []time.Duration{0}
	oldRetry := exitRetryDelays
	exitRetryDelays = []time.Duration{time.Hour} // retries must not fire inside a test
	t.Cleanup(func() { stopCancelDelays, exitRetryDelays = old, oldRetry })
	return oe
}

func (f *fakeAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seq...)
}

func (f *fakeAPI) rowFor(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r["orderid"] == id {
			return r
		}
	}
	return nil
}

func TestStop_PlacedOnFillInsteadOfGTT(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.03}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || e.SLOrderID == "" || e.GttRuleID != "" || e.GttSLRuleID != "" {
		t.Fatalf("entry %+v, want a stop order and no GTT", e)
	}
	stops := f.placedStops()
	if len(stops) != 1 {
		t.Fatalf("placed %d stops, want 1", len(stops))
	}
	st := stops[0]
	// entry 10003 − 1000 = 9003 → trigger ceil to 9005, limit 50 below → 8955
	if st["transactiontype"] != "SELL" || st["ordertype"] != "STOPLOSS_LIMIT" || st["quantity"] != "75" ||
		st["triggerprice"] != "90.05" || st["price"] != "89.55" {
		t.Fatalf("stop params %v", st)
	}
	f.mu.Lock()
	gtts := len(f.gttMade)
	f.mu.Unlock()
	if gtts != 0 {
		t.Fatalf("created %d GTT rules in stop mode", gtts)
	}
}

// The exit cancels the stop and confirms it before the SELL goes out.
func TestStop_ExitCancelsStopBeforeSelling(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	want := []string{"place:NORMAL:BUY", "place:STOPLOSS:SELL", "cancel:" + e.SLOrderID, "place:NORMAL:SELL"}
	if got := f.calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("broker calls %v, want %v", got, want)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("position still open after exit")
	}
}

// A stop that already filled closed the position: the exit sells nothing.
func TestStop_AlreadyFilled_NoSell(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	var fills []FillReport
	oe.SetFillListener(func(r FillReport) { fills = append(fills, r) })
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	f.setRow(map[string]any{"orderid": e.SLOrderID, "orderstatus": "complete", "averageprice": 89.80, "filledshares": "75"})
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	for _, c := range f.calls() {
		if c == "place:NORMAL:SELL" {
			t.Fatalf("SELL sent after the stop filled: %v", f.calls())
		}
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("position not closed after stop fill")
	}
	if last := fills[len(fills)-1]; last.Direction != "SELL" || last.FillPricePaise != 8980 || last.Qty != 75 {
		t.Fatalf("exit fill %+v, want SELL 75 @ 8980 (stop price)", last)
	}
}

// If the cancel cannot be confirmed the exit is held back: selling now
// could sell twice.
func TestStop_CancelUnconfirmed_HoldsExit(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0, cancelLeavesOpen: true}
	oe := stopExecutor(t, f)
	alerts := alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	for _, c := range f.calls() {
		if c == "place:NORMAL:SELL" {
			t.Fatalf("SELL sent with the stop still live: %v", f.calls())
		}
	}
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || e.SLOrderID == "" {
		t.Fatalf("entry %+v ok=%v, want position and stop kept", e, ok)
	}
	if !strings.Contains(alerts(), "not confirmed cancelled") {
		t.Fatalf("no alert: %q", alerts())
	}
}

// A stop that sold part before the cancel: the exit sells only the rest.
func TestStop_PartialStopFill_SellsRest(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	f.setRow(map[string]any{"orderid": e.SLOrderID, "orderstatus": "open", "filledshares": "25", "averageprice": 89.9})
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	n := f.placedCount()
	if q := f.placedAt(n - 1); q["variety"] != "NORMAL" || q["quantity"] != "50" {
		t.Fatalf("last order %v, want SELL 50", q)
	}
}

// A rejected exit leaves the position open: it gets a new stop.
func TestStop_RejectedExitRearmsStop(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.autoStatus = "rejected"
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || e.SLOrderID == "" {
		t.Fatalf("entry %+v ok=%v, want open with a new stop", e, ok)
	}
	if n := len(f.placedStops()); n != 2 {
		t.Fatalf("placed %d stops, want 2 (original + re-armed)", n)
	}
}

func TestSweep_StopFilledClosesPosition(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	f.setRow(map[string]any{"orderid": e.SLOrderID, "orderstatus": "complete", "averageprice": 89.5, "filledshares": "75"})
	oe.SweepProtection()
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("position open after its stop filled")
	}
}

func TestSweep_RearmsMissingAndExpiredStop(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	key := oe.positionKey(realSig(strategy.ActionBuy))

	// Missing: e.g. the stop placement failed at fill time.
	oe.updateEntry(key, func(r *OrderRecord) { r.SLOrderID = "" })
	oe.SweepProtection()
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	if e.SLOrderID == "" || len(f.placedStops()) != 2 {
		t.Fatalf("missing stop not re-armed: %+v", e)
	}

	// Expired: yesterday's DAY stop is not in today's book.
	oe.updateEntry(key, func(r *OrderRecord) { r.SLOrderID, r.SLPlacedAt = "OLD-STOP", time.Now().Add(-36*time.Hour) })
	oe.SweepProtection()
	e, _ = entryFor(oe, realSig(strategy.ActionBuy))
	if e.SLOrderID == "OLD-STOP" || len(f.placedStops()) != 3 {
		t.Fatalf("expired stop not replaced: %+v", e)
	}

	// A stop placed today but not yet visible in the book is left alone.
	oe.updateEntry(key, func(r *OrderRecord) { r.SLOrderID, r.SLPlacedAt = "LAGGING", time.Now() })
	oe.SweepProtection()
	if e, _ = entryFor(oe, realSig(strategy.ActionBuy)); e.SLOrderID != "LAGGING" {
		t.Fatalf("fresh stop replaced: %+v", e)
	}
}

func TestSweep_GTTModeIsNoop(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	before := f.placedCount()
	oe.SweepProtection()
	if f.placedCount() != before {
		t.Fatal("sweep placed orders in GTT mode")
	}
}
