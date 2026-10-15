package orderexec

import (
	"errors"
	"fmt"
	"log"

	"trading-systemv1/internal/circuitbreaker"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/oms"
)

// OrderLedger is the durable order record the executor writes every real
// order event to (satisfied by *oms.Ledger). Defined here, where it is
// consumed.
type OrderLedger interface {
	ClaimIntent(intentID string) (bool, error)
	Submit(ev oms.Event) error
	Apply(ev oms.Event) error
}

// SetOrderLedger attaches the order ledger. With Config.RequireJournal,
// real entries are refused until one is set.
func (oe *OrderExecutor) SetOrderLedger(l OrderLedger) {
	oe.mu.Lock()
	oe.ledger = l
	oe.mu.Unlock()
	log.Printf("%s 📒 order ledger attached", oe.logPrefix())
}

func (oe *OrderExecutor) orderLedger() OrderLedger {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	return oe.ledger
}

// journalEntryIntent claims a real BUY's intent and journals it as
// SUBMITTING before anything is sent. It returns false — and the BUY must
// not be sent — when the intent was already claimed (a replayed signal),
// or when the journal cannot record it.
func (oe *OrderExecutor) journalEntryIntent(sig model.Signal, posKey, token, symbol string, qty int64, key string) bool {
	l := oe.orderLedger()
	if l == nil {
		if oe.cfg.RequireJournal {
			oe.alertf("BUY %s %s NOT sent: no order journal attached", sig.StrategyName, symbol)
			return false
		}
		return true
	}
	claimed, err := l.ClaimIntent(key)
	switch {
	case err != nil:
		oe.alertf("BUY %s %s NOT sent: intent %s could not be claimed (%v)", sig.StrategyName, symbol, key, err)
		return false
	case !claimed:
		oe.alertf("BUY %s %s NOT sent: intent %s already used — replayed signal", sig.StrategyName, symbol, key)
		return false
	}
	if err := l.Submit(oms.Event{
		ClientOrderID: key, Strategy: sig.StrategyName, PositionKey: posKey, Side: "BUY",
		Symbol: symbol, Token: token, Qty: qty, Source: "executor",
	}); err != nil {
		oe.alertf("BUY %s %s NOT sent: order could not be journaled (%v)", sig.StrategyName, symbol, err)
		return false
	}
	return true
}

// journalExitIntent journals a real SELL as SUBMITTING. A failure is
// alerted but never stops the exit.
func (oe *OrderExecutor) journalExitIntent(sig model.Signal, posKey, token, symbol string, qty int64, key string) {
	l := oe.orderLedger()
	if l == nil {
		return
	}
	if err := l.Submit(oms.Event{
		ClientOrderID: key, Strategy: sig.StrategyName, PositionKey: posKey, Side: "SELL",
		Symbol: symbol, Token: token, Qty: qty, Source: "executor",
	}); err != nil {
		oe.alertf("EXIT %s %s not journaled (%v) — sending anyway", sig.StrategyName, symbol, err)
	}
}

// journalPlacement records what the placement attempt established.
func (oe *OrderExecutor) journalPlacement(key, orderID string, err error, positionFlat bool) {
	ev := oms.Event{ClientOrderID: key, BrokerOrderID: orderID, Source: "executor"}
	switch {
	case positionFlat:
		ev.State, ev.Reason = oms.StateRejected, "position already flat at broker"
	case err == nil:
		ev.State = oms.StateOpen
	case errors.Is(err, ErrOrderStateUnknown):
		ev.State, ev.Reason = oms.StateUnknown, err.Error()
	case errors.Is(err, circuitbreaker.ErrCircuitOpen):
		ev.State, ev.Reason = oms.StateAbsent, "not sent: circuit open"
	default:
		ev.State, ev.Reason = oms.StateRejected, err.Error()
	}
	oe.ledgerApply(ev)
}

// journalFill records a broker order-book result. Non-terminal rows are
// recorded only once something has filled.
func (oe *OrderExecutor) journalFill(tag, orderID string, f fillResult, source string) {
	ev := oms.Event{
		ClientOrderID: tag, BrokerOrderID: orderID, FilledQty: f.FilledQty,
		AvgPricePaise: f.AvgPricePaise, Reason: f.Text, Source: source,
	}
	switch {
	case f.Status == "complete":
		ev.State = oms.StateFilled
	case f.Status == "rejected":
		ev.State = oms.StateRejected
	case f.Status == "cancelled":
		ev.State = oms.StateCancelled
	case f.FilledQty > 0:
		ev.State = oms.StatePartial
	default:
		return
	}
	oe.ledgerApply(ev)
}

// ledgerApply writes ev to the ledger if one is attached. Problems are
// surfaced, never allowed to block an order: an illegal transition or an
// order the ledger does not know is an operator alert, a journal write
// failure too.
func (oe *OrderExecutor) ledgerApply(ev oms.Event) {
	l := oe.orderLedger()
	if l == nil {
		return
	}
	if err := l.Apply(ev); err != nil {
		if errors.Is(err, oms.ErrUnknownOrder) {
			// Orders sent before the journal existed (restored from an
			// older snapshot) are not in the ledger; nothing to record.
			log.Printf("%s 📒 ledger: %v", oe.logPrefix(), err)
			return
		}
		oe.alertf("order journal: %v", fmt.Errorf("%s %s: %w", ev.ClientOrderID, ev.State, err))
	}
}
