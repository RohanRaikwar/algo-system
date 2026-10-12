package orderexec

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/oms"
	"trading-systemv1/internal/strategy"
)

func attachTestLedger(t *testing.T, oe *OrderExecutor) *oms.Ledger {
	t.Helper()
	j, err := oms.OpenJournal(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	l, err := oms.NewLedger(j)
	if err != nil {
		t.Fatal(err)
	}
	oe.SetOrderLedger(l)
	return l
}

func stampedSig(action strategy.Action, ts time.Time) strategy.Signal {
	s := realSig(action)
	s.SignalTS = ts
	return s
}

func TestIdempotencyKey_DeterministicForStampedEntry(t *testing.T) {
	ts := time.Date(2026, 10, 12, 4, 31, 0, 0, time.UTC)
	buy := stampedSig(strategy.ActionBuy, ts)
	if generateIdempotencyKey(buy) != generateIdempotencyKey(buy) {
		t.Fatal("stamped BUY keys differ, want deterministic")
	}
	other := stampedSig(strategy.ActionBuy, ts.Add(time.Minute))
	if generateIdempotencyKey(buy) == generateIdempotencyKey(other) {
		t.Fatal("different candles share a key")
	}
	exit := stampedSig(strategy.ActionExit, ts)
	k1 := generateIdempotencyKey(exit)
	time.Sleep(time.Millisecond)
	if k1 == generateIdempotencyKey(exit) {
		t.Fatal("exit keys repeat, want unique")
	}
}

// Every real order event lands in the ledger: BUY and SELL end FILLED
// with the broker's order id and fill.
func TestJournal_RealRoundTripRecorded(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	l := attachTestLedger(t, oe)
	buy := stampedSig(strategy.ActionBuy, time.Now())
	oe.ExecuteSignal(buy)
	key := generateIdempotencyKey(buy)
	o, ok := l.Order(key)
	if !ok || o.State != oms.StateFilled || o.BrokerOrderID != "OID-1" || o.Side != "BUY" || o.Qty != 75 || o.AvgPricePaise != 10000 {
		t.Fatalf("BUY order %+v ok=%v", o, ok)
	}
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if w := l.Working(); len(w) != 0 {
		t.Fatalf("working orders after round trip: %+v", w)
	}
	tag := f.placedAt(1)["ordertag"].(string)
	if s, _ := l.Order(tag); s.State != oms.StateFilled || s.Side != "SELL" {
		t.Fatalf("SELL order %+v", s)
	}
}

// The same entry signal replayed (e.g. after a restart) is refused.
func TestJournal_ReplayedEntryRefused(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	attachTestLedger(t, oe)
	alerts := alertSink(oe)
	ts := time.Now()
	oe.ExecuteSignal(stampedSig(strategy.ActionBuy, ts))
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	oe.ExecuteSignal(stampedSig(strategy.ActionBuy, ts))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want BUY+SELL only (replay refused)", n)
	}
	if !strings.Contains(alerts(), "replayed signal") {
		t.Fatalf("no replay alert: %q", alerts())
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("refused replay left an entry")
	}
	oe.ExecuteSignal(stampedSig(strategy.ActionBuy, ts.Add(time.Minute)))
	if n := f.placedCount(); n != 3 {
		t.Fatalf("placed %d, want a new candle's BUY to go through", n)
	}
}

func TestJournal_RequiredButMissingRefusesEntry(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.cfg.RequireJournal = true
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d with no journal, want 0", n)
	}
}

type failingLedger struct{ OrderLedger }

func (failingLedger) ClaimIntent(string) (bool, error) { return true, nil }
func (failingLedger) Submit(oms.Event) error           { return errors.New("disk full") }
func (failingLedger) Apply(oms.Event) error            { return nil }

func TestJournal_SubmitFailureRefusesEntryNotExit(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy)) // filled without a journal
	oe.SetOrderLedger(failingLedger{})
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want the exit sent despite journal failure", n)
	}
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want the BUY refused when it cannot be journaled", n)
	}
}

func TestJournal_RejectionRecorded(t *testing.T) {
	f := &fakeAPI{placeErr: []error{errors.New("place order failed: Insufficient funds")}}
	oe := liveExecutor(t, f)
	l := attachTestLedger(t, oe)
	buy := stampedSig(strategy.ActionBuy, time.Now())
	oe.ExecuteSignal(buy)
	if o, _ := l.Order(generateIdempotencyKey(buy)); o.State != oms.StateRejected || !strings.Contains(o.Reason, "Insufficient") {
		t.Fatalf("order %+v, want REJECTED with reason", o)
	}
}
