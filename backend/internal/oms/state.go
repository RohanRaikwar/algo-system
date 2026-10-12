// Package oms holds the order-management core shared by the executor: an
// explicit order state machine, a ledger that applies broker evidence to
// it, and a durable append-only journal of every order event.
//
// It has no broker or strategy dependencies, so the executor (and later a
// standalone OMS process) can use it without import cycles.
package oms

// State is an order's lifecycle state.
type State string

const (
	StateSubmitting State = "SUBMITTING" // journaled, being sent to the broker
	StateOpen       State = "OPEN"       // accepted by the broker, nothing filled yet
	StatePartial    State = "PARTIAL"    // some quantity filled, order still working
	StateFilled     State = "FILLED"     // fully filled
	StateRejected   State = "REJECTED"   // refused by the broker
	StateCancelled  State = "CANCELLED"  // cancelled; FilledQty may be > 0
	StateUnknown    State = "UNKNOWN"    // may or may not be at the broker
	StateAbsent     State = "ABSENT"     // proven never to have reached the broker
)

// Terminal reports a state no further broker event can change.
func (s State) Terminal() bool {
	switch s {
	case StateFilled, StateRejected, StateCancelled, StateAbsent:
		return true
	}
	return false
}

// transitions lists the legal moves out of each non-terminal state.
// PARTIAL→PARTIAL is legal: each event carries a larger filled quantity.
var transitions = map[State][]State{
	StateSubmitting: {StateOpen, StatePartial, StateFilled, StateRejected, StateCancelled, StateUnknown, StateAbsent},
	StateUnknown:    {StateOpen, StatePartial, StateFilled, StateRejected, StateCancelled, StateAbsent},
	StateOpen:       {StatePartial, StateFilled, StateRejected, StateCancelled},
	StatePartial:    {StatePartial, StateFilled, StateCancelled},
}

// CanTransition reports whether an order may move from one state to another.
func CanTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}
