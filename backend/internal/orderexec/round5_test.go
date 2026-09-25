package orderexec

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

// A GTT that can't be cancelled after the position closed can later sell a
// flat position (naked short): retry once, then alert.
func TestExit_GTTCancelFails_RetriesThenAlerts(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	alerts := alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	down := errors.New("gateway timeout")
	f.cancelErr = []error{down, down, nil, nil}
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	waitFor(t, "cancel attempts", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.cancelCalls >= 3 })
	if !strings.Contains(alerts(), "not cancelled") {
		t.Fatalf("want alert for GTT that could not be cancelled, got %q", alerts())
	}
}

// A rule that already triggered can't be cancelled; that is expected, not an alert.
func TestExit_GTTAlreadyTriggered_NoAlert(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	alerts := alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.cancelErr = []error{errors.New("AB4021: rule already triggered"), nil}
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	time.Sleep(20 * time.Millisecond)
	if strings.Contains(alerts(), "not cancelled") {
		t.Fatalf("already-triggered rule must not alert, got %q", alerts())
	}
}

// Background settlement changes must be persisted at once (listener fires).
func TestStateListener_FiresAfterBackgroundSettle(t *testing.T) {
	f := &fakeAPI{autoStatus: "open"}
	oe := liveExecutor(t, f)
	var n atomic.Int32
	oe.SetStateListener(func() { n.Add(1) })
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	before := n.Load()
	f.setRow(map[string]any{"orderid": "OID-1", "orderstatus": "complete", "averageprice": 100.0})
	waitFor(t, "listener after settle", func() bool { return n.Load() > before })
}

// Re-adopting an entry that already has GTT IDs (crash before snapshot)
// must not place a second pair.
func TestCommitEntry_ExistingGTTs_NotDuplicated(t *testing.T) {
	f := &fakeAPI{}
	oe := liveExecutor(t, f)
	s := realSig(strategy.ActionBuy)
	oe.storeEntry(s, OrderRecord{OrderID: "OID-9", Symbol: "NIFTY_CE", Token: "111", Quantity: 75, Price: 10000, Real: true, Pending: true,
		GttRuleID: "GTT-OLD-T", GttSLRuleID: "GTT-OLD-SL", Timestamp: time.Now(), StrategyName: s.StrategyName, PositionSide: string(s.Side)})
	oe.onBuySettled(s, "OID-9", fillResult{Status: "complete", AvgPricePaise: 10000})
	f.mu.Lock()
	made := len(f.gttMade)
	f.mu.Unlock()
	if made != 0 {
		t.Fatalf("placed %d new GTTs for an entry that already had both", made)
	}
}

// When fast polling ends without a terminal status, keep polling slowly
// (and alert) instead of leaving state stuck.
func TestPoll_ContinuesSlowlyAfterFastWindow(t *testing.T) {
	f := &fakeAPI{autoStatus: "open"}
	oe := liveExecutor(t, f)
	old1, old2 := asyncSlowEvery, asyncSlowTries
	asyncSlowEvery, asyncSlowTries = 2*time.Millisecond, 500
	defer func() { asyncSlowEvery, asyncSlowTries = old1, old2 }()
	alerts := alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	time.Sleep(120 * time.Millisecond) // fast window (50×1ms) is over
	f.setRow(map[string]any{"orderid": "OID-1", "orderstatus": "complete", "averageprice": 100.0})
	waitFor(t, "settled in slow phase", func() bool {
		e, _ := entryFor(oe, realSig(strategy.ActionBuy))
		return !e.Pending && e.GttRuleID != ""
	})
	if !strings.Contains(alerts(), "slowly") {
		t.Fatalf("want alert on switch to slow polling, got %q", alerts())
	}
}
