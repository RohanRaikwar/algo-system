package orderexec

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

func fastExitRetries(t *testing.T) {
	t.Helper()
	old := exitRetryDelays
	exitRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { exitRetryDelays = old })
}

// The BUY's intent must be persisted before the broker sees the order, so a
// crash right after placement restarts with a record of it.
func TestRealBuy_IntentPersistedBeforePlacement(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	var snap []byte
	var placedAtPersist int
	oe.SetIntentPersister(func() error {
		if snap == nil {
			placedAtPersist = f.placedCount()
			snap, _ = oe.Snapshot()
		}
		return nil
	})

	oe.ExecuteSignal(realSig(strategy.ActionBuy))

	if snap == nil || placedAtPersist != 0 {
		t.Fatalf("intent not persisted before placement (persisted=%v placed=%d)", snap != nil, placedAtPersist)
	}
	var s executorSnapshot
	if err := json.Unmarshal(snap, &s); err != nil {
		t.Fatal(err)
	}
	rec, ok := s.EntryOrders["NIFTY50_FNO|CALL"]
	if !ok || !rec.Real || !rec.Pending || rec.ClientOrderID == "" || rec.IndexToken != "99926000" {
		t.Fatalf("persisted intent = %+v ok=%v, want real pending entry with tag and index token", rec, ok)
	}
}

func TestRealBuy_NotSentWhenIntentCannotBePersisted(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.SetIntentPersister(func() error { return errors.New("redis down") })

	oe.ExecuteSignal(realSig(strategy.ActionBuy))

	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d orders, want 0 when intent cannot be persisted", n)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("entry left behind after refused BUY")
	}
}

func TestRealExit_SentEvenWhenIntentCannotBePersisted(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.SetIntentPersister(func() error { return errors.New("redis down") })

	oe.ExecuteSignal(realSig(strategy.ActionExit))

	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want BUY+SELL — exits must never be blocked", n)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("position still tracked after completed exit")
	}
}

// Crash between placeOrder and the post-order snapshot: the restarted
// executor holds only the pre-send intent, finds the order in the book by
// its tag, adopts it with GTTs, and can close it for real.
func TestRealBuy_CrashAfterPlacementRecoveredFromIntent(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	var snap []byte
	oe.SetIntentPersister(func() error {
		if snap == nil {
			snap, _ = oe.Snapshot()
		}
		return nil
	})
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if f.placedCount() != 1 {
		t.Fatal("setup: BUY not placed")
	}

	restarted := liveExecutor(t, f)
	if err := restarted.Restore(snap); err != nil {
		t.Fatal(err)
	}
	restarted.ResumeSettlement()
	waitFor(t, "restored intent adopted", func() bool {
		r, ok := entryFor(restarted, realSig(strategy.ActionBuy))
		return ok && !r.Pending && r.GttSLRuleID != ""
	})

	restarted.ExecuteSignal(realSig(strategy.ActionExit))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want the restored position closed with a real SELL", n)
	}
	if got := f.placedAt(1)["transactiontype"]; got != "SELL" {
		t.Fatalf("second order %v, want SELL", got)
	}
}

func TestRealExit_FailedExitIsRetried(t *testing.T) {
	fastExitRetries(t)
	f := &fakeAPI{
		autoStatus: "complete", autoAvg: 100.0,
		placeErr:  []error{nil, errors.New("AB2001 exchange busy")},
		positions: []map[string]any{{"symboltoken": "111", "tradingsymbol": "NIFTY_CE", "netqty": "75"}},
	}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionExit))

	waitFor(t, "retried exit closes position", func() bool {
		_, open := entryFor(oe, realSig(strategy.ActionBuy))
		return !open
	})
	if n := f.placedCount(); n != 3 {
		t.Fatalf("placed %d, want BUY + failed SELL + retried SELL", n)
	}
	if q := f.placedAt(2)["quantity"]; q != "75" {
		t.Fatalf("retried SELL qty %v, want 75", q)
	}
}

func TestRealExit_RetryFinalisesWhenBrokerAlreadyFlat(t *testing.T) {
	fastExitRetries(t)
	f := &fakeAPI{
		autoStatus: "complete", autoAvg: 100.0,
		placeErr: []error{nil, errors.New("AB2001 exchange busy")},
		// no positions: a GTT closed it meanwhile
	}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionExit))

	waitFor(t, "flat position finalised", func() bool {
		_, open := entryFor(oe, realSig(strategy.ActionBuy))
		return !open
	})
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want no SELL resent on a flat position", n)
	}
}

type fillLog struct {
	mu    sync.Mutex
	fills []FillReport
}

func (l *fillLog) add(f FillReport) { l.mu.Lock(); l.fills = append(l.fills, f); l.mu.Unlock() }
func (l *fillLog) get() []FillReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]FillReport(nil), l.fills...)
}

func TestFills_PaperReportedAndExitWithoutEntryIsNot(t *testing.T) {
	oe := exitTestExecutor(10)
	oe.UpdateLTP(model.Tick{Token: "12345", Price: 5000})
	var l fillLog
	oe.SetFillListener(l.add)

	oe.ExecuteSignal(sig(strategy.ActionExit)) // nothing open
	oe.ExecuteSignal(sig(strategy.ActionBuy))
	oe.ExecuteSignal(sig(strategy.ActionExit))

	got := l.get()
	if len(got) != 2 || got[0].Direction != "BUY" || got[1].Direction != "SELL" || got[0].Real || got[0].FillPricePaise != 5000 {
		t.Fatalf("fills = %+v, want paper BUY then SELL at 5000 only", got)
	}
}

func TestFills_RealRejectedBuyNotReported(t *testing.T) {
	f := &fakeAPI{autoStatus: "rejected"}
	oe := liveExecutor(t, f)
	var l fillLog
	oe.SetFillListener(l.add)

	oe.ExecuteSignal(realSig(strategy.ActionBuy))

	if got := l.get(); len(got) != 0 {
		t.Fatalf("rejected BUY reported as fill: %+v", got)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("rejected BUY left an entry")
	}
}
