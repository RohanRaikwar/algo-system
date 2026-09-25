package orderexec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"
)

func alertSink(oe *OrderExecutor) func() string {
	var mu sync.Mutex
	var got []string
	oe.SetAlerter(func(m string) { mu.Lock(); got = append(got, m); mu.Unlock() })
	return func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(got, "\n") }
}

// R3-1a: the exit sells what is actually held, not the configured size.
func TestRealExit_SellsTrackedQuantityAfterPartialBuy(t *testing.T) {
	f := &fakeAPI{autoStatus: "cancelled", autoAvg: 100.0, autoFilled: "25"}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.autoStatus, f.autoFilled = "complete", ""
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want BUY+SELL", n)
	}
	if q := f.placedAt(1)["quantity"]; q != "25" {
		t.Fatalf("SELL quantity %v, want 25 (held)", q)
	}
}

// R3-1b: a "net quantity"/flat rejection only closes the position if the
// broker really shows it flat.
func TestRealExit_FlatRejectionButBrokerStillHolds_KeepsOpen(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	alerts := alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.placeErr = []error{nil, errors.New("AB1014: Order Quantity Cannot Be More Than Net Quantity")}
	f.positions = []map[string]any{{"symboltoken": "111", "tradingsymbol": "NIFTY_CE", "netqty": "75"}}
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if _, ok := entryFor(oe, realSig(strategy.ActionExit)); !ok {
		t.Fatal("position closed on a flat-rejection while broker still holds 75")
	}
	if len(f.cancelled()) != 0 {
		t.Fatal("GTT protection cancelled while position still held")
	}
	if !strings.Contains(alerts(), "still holds") {
		t.Fatalf("want operator alert, got %q", alerts())
	}
}

func TestRealExit_FlatRejectionAndBrokerFlat_Closes(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.placeErr = []error{nil, errors.New("AB1018: No Holdings Available")}
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if _, ok := entryFor(oe, realSig(strategy.ActionExit)); ok {
		t.Fatal("broker flat: position should be closed")
	}
}

// R3-2: an order book replying status:false is unreadable, not empty — no resend.
func TestRealBuy_TimeoutAndBookStatusFalse_NoResend(t *testing.T) {
	unknown := fmt.Errorf("%w: timeout", smartconnect.ErrOutcomeUnknown)
	f := &fakeAPI{placeErr: []error{unknown}, bookStatusFalse: true}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("placed %d — resent although the order book was unreadable", n)
	}
	if e, ok := entryFor(oe, realSig(strategy.ActionBuy)); !ok || !e.Pending {
		t.Fatalf("want provisional pending entry, got %+v ok=%v", e, ok)
	}
}

// R3-3: a definite but non-session rejection must not be resent.
func TestOrderAttempt_NonSessionRejection_NoResend(t *testing.T) {
	f := &fakeBroker{placeResults: []error{errors.New("place order failed: AB2001 insufficient margin")}}
	if _, err := f.attempt("tag1").run(); err == nil {
		t.Fatal("want error")
	}
	if f.placeCalls != 1 || f.refreshCalls != 0 {
		t.Fatalf("place=%d refresh=%d, want 1/0", f.placeCalls, f.refreshCalls)
	}
}

// R3-4: an entry with no known price has no GTT protection — alert.
func TestRealBuy_NoPrice_AlertsNoProtection(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 0}
	oe := liveExecutor(t, f)
	alerts := alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy)) // ltp unknown (0), avg unreadable
	if !strings.Contains(alerts(), "no exchange-side") && !strings.Contains(alerts(), "NO exchange-side") {
		t.Fatalf("want no-protection alert, got %q", alerts())
	}
}

// R3-5 + R3-7: an accepted-but-unconfirmed BUY stays pending with no GTTs;
// once filled it gets GTTs synchronously (IDs present as soon as it's adopted).
func TestRealBuy_Unconfirmed_PendingUntilFilledThenProtected(t *testing.T) {
	f := &fakeAPI{autoStatus: "open", autoAvg: 0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || !e.Pending || e.GttRuleID != "" {
		t.Fatalf("want pending entry without GTTs, got %+v", e)
	}
	f.setRow(map[string]any{"orderid": "OID-1", "orderstatus": "complete", "averageprice": 100.0})
	waitFor(t, "adopted with GTTs", func() bool {
		e, _ := entryFor(oe, realSig(strategy.ActionBuy))
		return !e.Pending && e.GttRuleID != "" && e.GttSLRuleID != ""
	})
}

func TestRealBuy_Filled_GTTIDsRecordedSynchronously(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	if e.GttRuleID == "" || e.GttSLRuleID == "" {
		t.Fatalf("GTT IDs must be recorded before ExecuteSignal returns, got %+v", e)
	}
}

// R3-6: an exit during a pending BUY is deferred and sent once the BUY fills.
func TestRealExit_DuringPendingBuy_DeferredThenSent(t *testing.T) {
	f := &fakeAPI{autoStatus: "open", autoAvg: 0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("SELL sent against an unfilled BUY (placed %d)", n)
	}
	f.mu.Lock()
	f.autoStatus = "complete"
	f.autoAvg = 105.0
	f.mu.Unlock()
	f.setRow(map[string]any{"orderid": "OID-1", "orderstatus": "complete", "averageprice": 100.0})
	waitFor(t, "deferred exit sent and closed", func() bool {
		_, open := entryFor(oe, realSig(strategy.ActionExit))
		return f.placedCount() == 2 && !open
	})
	if f.placedAt(1)["transactiontype"] != "SELL" {
		t.Fatal("second order must be the deferred SELL")
	}
}
