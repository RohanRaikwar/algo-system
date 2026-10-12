package oms

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Event is one change to one order. ClientOrderID (the ordertag sent to
// the broker) identifies the order; an event that only knows the broker's
// order id is matched through it once the broker id has been recorded.
type Event struct {
	ClientOrderID string
	BrokerOrderID string
	Strategy      string
	PositionKey   string
	Side          string // BUY or SELL
	Symbol        string
	Token         string
	State         State
	Qty           int64 // order quantity
	FilledQty     int64
	AvgPricePaise int64
	Reason        string
	Source        string // executor, poll, restore, …
	TS            time.Time
}

// Order is an order's current view, folded from its events.
type Order struct {
	ClientOrderID string
	BrokerOrderID string
	Strategy      string
	PositionKey   string
	Side          string
	Symbol        string
	Token         string
	State         State
	Qty           int64
	FilledQty     int64
	AvgPricePaise int64
	Reason        string
	Created       time.Time
	Updated       time.Time
}

// Store is the durable side of the ledger. Append must be durable when it
// returns: the ledger writes before it changes memory.
type Store interface {
	ClaimIntent(intentID string, at time.Time) (bool, error)
	Append(ev Event) error
	Events(since time.Time) ([]Event, error)
}

var (
	// ErrIllegalTransition means an event would move an order along an
	// edge the state machine does not allow. The order is left unchanged.
	ErrIllegalTransition = errors.New("illegal order state transition")
	// ErrUnknownOrder means an event names no order the ledger knows.
	ErrUnknownOrder = errors.New("unknown order")
)

// Ledger is the in-memory order book backed by a Store. Safe for
// concurrent use.
type Ledger struct {
	mu       sync.Mutex
	store    Store
	orders   map[string]*Order // client order id → order
	byBroker map[string]string // broker order id → client order id
	now      func() time.Time
}

// replayWindow bounds how far back NewLedger rebuilds orders. The broker's
// order book is day-scoped; older orders only matter for audit.
const replayWindow = 7 * 24 * time.Hour

// NewLedger rebuilds the ledger from the store's recent events.
func NewLedger(store Store) (*Ledger, error) {
	l := &Ledger{
		store:    store,
		orders:   make(map[string]*Order),
		byBroker: make(map[string]string),
		now:      time.Now,
	}
	evs, err := store.Events(time.Now().Add(-replayWindow))
	if err != nil {
		return nil, fmt.Errorf("oms: replay journal: %w", err)
	}
	for _, ev := range evs {
		l.fold(ev)
	}
	return l, nil
}

// ClaimIntent records that an order for intentID is being sent. It returns
// false when the intent was already claimed — a replayed signal that must
// not be sent again.
func (l *Ledger) ClaimIntent(intentID string) (bool, error) {
	return l.store.ClaimIntent(intentID, l.now())
}

// Submit journals a new order in SUBMITTING before it is sent. An error
// means nothing was recorded and the order must not be sent if it can be
// skipped (an entry).
func (l *Ledger) Submit(ev Event) error {
	if ev.ClientOrderID == "" {
		return errors.New("oms: submit without client order id")
	}
	ev.State = StateSubmitting
	l.stamp(&ev)
	l.mu.Lock()
	defer l.mu.Unlock()
	if o, ok := l.orders[ev.ClientOrderID]; ok {
		return fmt.Errorf("oms: order %s already exists in %s", ev.ClientOrderID, o.State)
	}
	if err := l.store.Append(ev); err != nil {
		return fmt.Errorf("oms: journal submit %s: %w", ev.ClientOrderID, err)
	}
	l.fold(ev)
	return nil
}

// Apply moves an order to ev.State. A repeat of the current state is a
// no-op (except PARTIAL with more filled). An illegal move returns
// ErrIllegalTransition and changes nothing. The event is journaled before
// memory changes.
func (l *Ledger) Apply(ev Event) error {
	l.stamp(&ev)
	l.mu.Lock()
	defer l.mu.Unlock()
	o := l.findLocked(ev)
	if o == nil {
		return fmt.Errorf("%w: client=%q broker=%q", ErrUnknownOrder, ev.ClientOrderID, ev.BrokerOrderID)
	}
	ev.ClientOrderID = o.ClientOrderID
	if o.State == ev.State && (ev.State != StatePartial || ev.FilledQty <= o.FilledQty) {
		if ev.BrokerOrderID == "" || ev.BrokerOrderID == o.BrokerOrderID {
			return nil
		}
	} else if !CanTransition(o.State, ev.State) {
		return fmt.Errorf("%w: %s %s → %s", ErrIllegalTransition, o.ClientOrderID, o.State, ev.State)
	}
	if err := l.store.Append(ev); err != nil {
		return fmt.Errorf("oms: journal %s → %s: %w", o.ClientOrderID, ev.State, err)
	}
	l.fold(ev)
	return nil
}

// Order returns the current view of an order by client order id.
func (l *Ledger) Order(clientOrderID string) (Order, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	o, ok := l.orders[clientOrderID]
	if !ok {
		return Order{}, false
	}
	return *o, true
}

// Working returns every order not yet in a terminal state.
func (l *Ledger) Working() []Order {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Order
	for _, o := range l.orders {
		if !o.State.Terminal() {
			out = append(out, *o)
		}
	}
	return out
}

// HasUnknown reports whether a strategy has an order in UNKNOWN.
func (l *Ledger) HasUnknown(strategy string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, o := range l.orders {
		if o.Strategy == strategy && o.State == StateUnknown {
			return true
		}
	}
	return false
}

func (l *Ledger) stamp(ev *Event) {
	if ev.TS.IsZero() {
		ev.TS = l.now()
	}
}

// findLocked resolves an event to its order: by client id (a retry tag
// "<id>R" maps to its base order), else by broker id.
func (l *Ledger) findLocked(ev Event) *Order {
	if id := ev.ClientOrderID; id != "" {
		if o, ok := l.orders[id]; ok {
			return o
		}
		if o, ok := l.orders[strings.TrimSuffix(id, "R")]; ok {
			return o
		}
	}
	if cid, ok := l.byBroker[ev.BrokerOrderID]; ok && ev.BrokerOrderID != "" {
		return l.orders[cid]
	}
	return nil
}

// fold applies a journaled event to memory without validation; replay and
// the write paths above share it.
func (l *Ledger) fold(ev Event) {
	o, ok := l.orders[ev.ClientOrderID]
	if !ok {
		o = &Order{ClientOrderID: ev.ClientOrderID, Created: ev.TS}
		l.orders[ev.ClientOrderID] = o
	}
	setStr := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	setStr(&o.BrokerOrderID, ev.BrokerOrderID)
	setStr(&o.Strategy, ev.Strategy)
	setStr(&o.PositionKey, ev.PositionKey)
	setStr(&o.Side, ev.Side)
	setStr(&o.Symbol, ev.Symbol)
	setStr(&o.Token, ev.Token)
	o.Reason = ev.Reason
	if ev.Qty > 0 {
		o.Qty = ev.Qty
	}
	if ev.FilledQty > 0 {
		o.FilledQty = ev.FilledQty
	}
	if ev.AvgPricePaise > 0 {
		o.AvgPricePaise = ev.AvgPricePaise
	}
	o.State = ev.State
	o.Updated = ev.TS
	if o.BrokerOrderID != "" {
		l.byBroker[o.BrokerOrderID] = o.ClientOrderID
	}
}
