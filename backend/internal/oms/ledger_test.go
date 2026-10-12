package oms

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestJournal(t *testing.T) *Journal {
	t.Helper()
	j, err := OpenJournal(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

func newTestLedger(t *testing.T, s Store) *Ledger {
	t.Helper()
	l, err := NewLedger(s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCanTransition(t *testing.T) {
	for _, c := range []struct {
		from, to State
		want     bool
	}{
		{StateSubmitting, StateOpen, true},
		{StateSubmitting, StateUnknown, true},
		{StateUnknown, StateAbsent, true},
		{StateOpen, StateFilled, true},
		{StatePartial, StatePartial, true},
		{StateOpen, StateUnknown, false},
		{StateFilled, StateCancelled, false},
		{StateRejected, StateOpen, false},
		{StateAbsent, StateOpen, false},
	} {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestLedger_LifecycleAndReplay(t *testing.T) {
	j := openTestJournal(t)
	l := newTestLedger(t, j)
	if err := l.Submit(Event{ClientOrderID: "abc", Strategy: "S", Side: "BUY", Qty: 75}); err != nil {
		t.Fatal(err)
	}
	if err := l.Apply(Event{ClientOrderID: "abc", BrokerOrderID: "B1", State: StateOpen}); err != nil {
		t.Fatal(err)
	}
	// A later event that only knows the broker id finds the order.
	if err := l.Apply(Event{BrokerOrderID: "B1", State: StateFilled, FilledQty: 75, AvgPricePaise: 10050}); err != nil {
		t.Fatal(err)
	}
	o, _ := l.Order("abc")
	if o.State != StateFilled || o.FilledQty != 75 || o.AvgPricePaise != 10050 || o.Qty != 75 {
		t.Fatalf("order %+v", o)
	}

	// A fresh ledger over the same journal rebuilds the same view.
	l2 := newTestLedger(t, j)
	o2, ok := l2.Order("abc")
	if !ok || o2.State != StateFilled || o2.BrokerOrderID != "B1" || o2.FilledQty != 75 {
		t.Fatalf("replayed order %+v ok=%v", o2, ok)
	}
}

func TestLedger_IllegalTransitionRefused(t *testing.T) {
	l := newTestLedger(t, openTestJournal(t))
	_ = l.Submit(Event{ClientOrderID: "x"})
	_ = l.Apply(Event{ClientOrderID: "x", State: StateFilled})
	err := l.Apply(Event{ClientOrderID: "x", State: StateCancelled})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("err %v, want ErrIllegalTransition", err)
	}
	if o, _ := l.Order("x"); o.State != StateFilled {
		t.Fatalf("state %s after refused transition, want FILLED", o.State)
	}
}

func TestLedger_RepeatStateIsNoop(t *testing.T) {
	s := &memStore{}
	l := newTestLedger(t, s)
	_ = l.Submit(Event{ClientOrderID: "x"})
	_ = l.Apply(Event{ClientOrderID: "x", State: StateOpen})
	_ = l.Apply(Event{ClientOrderID: "x", State: StateOpen})
	_ = l.Apply(Event{ClientOrderID: "x", State: StatePartial, FilledQty: 25})
	_ = l.Apply(Event{ClientOrderID: "x", State: StatePartial, FilledQty: 50})
	if n := len(s.evs); n != 4 {
		t.Fatalf("journaled %d events, want 4 (repeat OPEN skipped, growing PARTIAL kept)", n)
	}
}

func TestLedger_RetryTagMapsToBaseOrder(t *testing.T) {
	l := newTestLedger(t, &memStore{})
	_ = l.Submit(Event{ClientOrderID: "abcd1234abcd1234", Strategy: "S"})
	_ = l.Apply(Event{ClientOrderID: "abcd1234abcd1234", State: StateUnknown})
	if !l.HasUnknown("S") {
		t.Fatal("HasUnknown false with an UNKNOWN order")
	}
	if err := l.Apply(Event{ClientOrderID: "abcd1234abcd1234R", BrokerOrderID: "B9", State: StateFilled}); err != nil {
		t.Fatal(err)
	}
	if o, _ := l.Order("abcd1234abcd1234"); o.State != StateFilled || o.BrokerOrderID != "B9" {
		t.Fatalf("order %+v", o)
	}
	if l.HasUnknown("S") {
		t.Fatal("HasUnknown true after settle")
	}
}

// Journal first: a failed write leaves memory unchanged.
func TestLedger_StoreFailureChangesNothing(t *testing.T) {
	s := &memStore{}
	l := newTestLedger(t, s)
	_ = l.Submit(Event{ClientOrderID: "x"})
	s.failAppend = errors.New("disk full")
	if err := l.Apply(Event{ClientOrderID: "x", State: StateOpen}); err == nil {
		t.Fatal("Apply succeeded with a failing store")
	}
	if o, _ := l.Order("x"); o.State != StateSubmitting {
		t.Fatalf("state %s after failed write, want SUBMITTING", o.State)
	}
	if err := l.Submit(Event{ClientOrderID: "y"}); err == nil {
		t.Fatal("Submit succeeded with a failing store")
	}
	if _, ok := l.Order("y"); ok {
		t.Fatal("order y exists after failed submit")
	}
}

func TestLedger_UnknownOrder(t *testing.T) {
	l := newTestLedger(t, &memStore{})
	if err := l.Apply(Event{BrokerOrderID: "nope", State: StateFilled}); !errors.Is(err, ErrUnknownOrder) {
		t.Fatalf("err %v, want ErrUnknownOrder", err)
	}
}

func TestJournal_ClaimIntentOnce(t *testing.T) {
	j := openTestJournal(t)
	l := newTestLedger(t, j)
	ok, err := l.ClaimIntent("i1")
	if err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	ok, err = l.ClaimIntent("i1")
	if err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v, want false", ok, err)
	}
	// Survives reopening through a new ledger on the same store.
	if ok, _ := newTestLedger(t, j).ClaimIntent("i1"); ok {
		t.Fatal("claim reusable after reopen")
	}
}

type memStore struct {
	evs        []Event
	claims     map[string]bool
	failAppend error
}

func (m *memStore) ClaimIntent(id string, _ time.Time) (bool, error) {
	if m.claims == nil {
		m.claims = map[string]bool{}
	}
	if m.claims[id] {
		return false, nil
	}
	m.claims[id] = true
	return true, nil
}

func (m *memStore) Append(ev Event) error {
	if m.failAppend != nil {
		return m.failAppend
	}
	m.evs = append(m.evs, ev)
	return nil
}

func (m *memStore) Events(since time.Time) ([]Event, error) {
	var out []Event
	for _, ev := range m.evs {
		if !ev.TS.Before(since) {
			out = append(out, ev)
		}
	}
	return out, nil
}
