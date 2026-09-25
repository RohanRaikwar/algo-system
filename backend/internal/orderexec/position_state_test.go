package orderexec

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"
)

// #1 — a same-side BUY while a position is open must not double it.
func TestRealBuy_SameSideWhileOpen_Refused(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("placed %d orders, want 1 — same-side BUY doubled the position", n)
	}
}

// #2 — a real SELL must only go out against a real tracked entry.
func TestRealSell_WithoutRealEntry_NotSent(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("sent %d real SELLs with no real entry — naked short risk", n)
	}
}

// #3 — an exit that is not yet complete must not close the position or
// cancel its GTT protection; it closes when the broker says complete.
func TestRealExit_NotComplete_KeepsProtectionUntilFilled(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	waitFor(t, "entry GTTs", func() bool {
		e, _ := entryFor(oe, realSig(strategy.ActionBuy))
		return e.GttRuleID != "" && e.GttSLRuleID != ""
	})

	f.mu.Lock()
	f.autoStatus = "open"
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))

	if _, ok := entryFor(oe, realSig(strategy.ActionExit)); !ok {
		t.Fatal("entry removed while exit still open")
	}
	if c := f.cancelled(); len(c) != 0 {
		t.Fatalf("GTT protection cancelled before exit filled: %v", c)
	}
	if !inCall(oe) {
		t.Fatal("position flag cleared before exit filled")
	}

	f.setRow(map[string]any{"orderid": "OID-2", "orderstatus": "complete", "averageprice": 110.0})
	waitFor(t, "exit finalised", func() bool {
		_, ok := entryFor(oe, realSig(strategy.ActionExit))
		return !ok && !inCall(oe) && len(f.cancelled()) == 2
	})
}

// #3b — a second exit while one is pending must not send another SELL.
func TestRealExit_PendingExit_NoSecondSell(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.autoStatus = "open"
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d orders, want 2 (BUY + one SELL)", n)
	}
}

// #4 — a SELL arriving while its BUY is still in flight must wait and then
// target the contract the BUY bought.
func TestRealSell_DuringInFlightBuy_Serialised(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0, placeGate: make(chan struct{})}
	oe := liveExecutor(t, f)

	buyDone := make(chan struct{})
	go func() { oe.ExecuteSignal(realSig(strategy.ActionBuy)); close(buyDone) }()
	time.Sleep(10 * time.Millisecond) // BUY now blocked inside PlaceOrder

	sellDone := make(chan struct{})
	go func() { oe.ExecuteSignal(realSig(strategy.ActionExit)); close(sellDone) }()
	time.Sleep(10 * time.Millisecond)

	close(f.placeGate)
	<-buyDone
	<-sellDone
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want BUY then SELL", n)
	}
	if got := f.placedAt(1); got["transactiontype"] != "SELL" || got["tradingsymbol"] != "NIFTY_CE" {
		t.Fatalf("SELL must follow BUY on the bought contract NIFTY_CE, got %v %v", got["transactiontype"], got["tradingsymbol"])
	}
}

// #7 — a BUY in unknown state is tracked provisionally and adopted once the
// book shows it filled (entry + GTTs), so the position is never unprotected.
func TestRealBuy_UnknownState_AdoptedFromBook(t *testing.T) {
	unknown := fmt.Errorf("%w: timeout", smartconnect.ErrOutcomeUnknown)
	f := &fakeAPI{placeErr: []error{unknown, unknown}, bookErr: errors.New("down")}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))

	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || !e.Real {
		t.Fatalf("unknown BUY must leave a provisional real entry, got %+v ok=%v", e, ok)
	}
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if f.placedCount() != 1 {
		t.Fatal("second BUY sent while first is in unknown state")
	}

	tag := fmt.Sprint(f.placedAt(0)["ordertag"])
	f.mu.Lock()
	f.bookErr = nil
	f.rows = []map[string]any{{"orderid": "BRK-7", "ordertag": tag, "orderstatus": "complete", "averageprice": 101.5}}
	f.mu.Unlock()

	waitFor(t, "adoption", func() bool {
		e, _ := entryFor(oe, realSig(strategy.ActionBuy))
		return e.OrderID == "BRK-7" && e.Price == 10150 && e.GttRuleID != "" && e.GttSLRuleID != ""
	})
}

// #7b — an unknown BUY that never reached the broker is cleared.
func TestRealBuy_UnknownState_AbsentCleared(t *testing.T) {
	unknown := fmt.Errorf("%w: timeout", smartconnect.ErrOutcomeUnknown)
	f := &fakeAPI{placeErr: []error{unknown}, bookErr: errors.New("down")}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.bookErr = nil // book readable, order absent
	f.mu.Unlock()
	waitFor(t, "provisional entry cleared", func() bool {
		_, ok := entryFor(oe, realSig(strategy.ActionBuy))
		return !ok && !inCall(oe)
	})
}

// #8 — without a live session, an exit must not erase a real position.
func TestRealEntry_NotLive_ExitKeepsState(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.mu.Lock()
	oe.sessionOK = false
	oe.mu.Unlock()

	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if _, ok := entryFor(oe, realSig(strategy.ActionExit)); !ok {
		t.Fatal("real entry erased by a paper exit — broker still holds the position")
	}
	if !inCall(oe) {
		t.Fatal("position flag cleared by a paper exit")
	}
}

// Restart while an exit is pending: settlement must resume from the
// restored snapshot, or the position stays stuck refusing exits.
func TestResumeSettlement_PendingExitAfterRestart(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.autoStatus = "open"
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	data, err := oe.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	restarted := liveExecutor(t, f)
	if err := restarted.Restore(data); err != nil {
		t.Fatal(err)
	}
	f.setRow(map[string]any{"orderid": "OID-2", "orderstatus": "complete", "averageprice": 105.0})
	restarted.ResumeSettlement()
	waitFor(t, "resumed exit settles", func() bool {
		_, ok := entryFor(restarted, realSig(strategy.ActionExit))
		return !ok && !inCall(restarted)
	})
}
