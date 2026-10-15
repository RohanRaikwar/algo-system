package orderexec

import (
	"fmt"
	"log"
	"strings"
	"time"

	"trading-systemv1/internal/oms"
	"trading-systemv1/internal/strategy"
)

// Protection modes for a filled real entry.
//
//   - ProtectionStop ("sl"): one resting SELL STOPLOSS_LIMIT order at the
//     exchange, sized to the held quantity. One order, so nothing can fire
//     twice. An exit cancels it, confirms the cancel, then sells; if the
//     stop already filled, the exit is done without selling.
//   - ProtectionGTT ("gtt"): the older GTT rules (stop and optional
//     target). GTT rules are not one-cancels-other.
const (
	ProtectionStop = "sl"
	ProtectionGTT  = "gtt"
)

const stopVariety = "STOPLOSS"

// stopCancelDelays spaces order-book reads while confirming a stop's
// cancel before an exit sells.
var stopCancelDelays = []time.Duration{0, 300 * time.Millisecond, 700 * time.Millisecond, time.Second}

func (oe *OrderExecutor) stopMode() bool { return oe.cfg.ProtectionMode == ProtectionStop }

// placeStop places the protective stop for a filled entry and returns its
// broker order id ("" on failure, with an alert). Caller holds the
// position lock.
func (oe *OrderExecutor) placeStop(posKey string, rec OrderRecord) string {
	if oe.cfg.FNOStopLossPaise <= 0 {
		return ""
	}
	if rec.Price <= 0 || rec.Price-oe.cfg.FNOStopLossPaise <= 0 || rec.Quantity <= 0 {
		oe.alertf("STOP not placed for %s: entry price %d / qty %d unusable — position has NO exchange-side stop", posKey, rec.Price, rec.Quantity)
		return ""
	}
	if oe.brokerAPI() == nil {
		oe.alertf("STOP not placed for %s: no broker session — position has NO exchange-side stop", posKey)
		return ""
	}
	trigger, limit := gttStopLossPaise(rec.Price, oe.cfg.FNOStopLossPaise, oe.tickPaise())
	tag := generateIdempotencyKey(strategy.Signal{StrategyName: rec.StrategyName, Side: strategy.PositionSide(rec.PositionSide), Action: "STOP"})
	params := map[string]any{
		"variety":         stopVariety,
		"tradingsymbol":   rec.Symbol,
		"symboltoken":     rec.Token,
		"transactiontype": "SELL",
		"exchange":        oe.exchangeFor(rec.Symbol),
		"ordertype":       "STOPLOSS_LIMIT",
		"producttype":     oe.cfg.FNOProductType,
		"duration":        "DAY",
		"price":           paiseToRupees(limit),
		"triggerprice":    paiseToRupees(trigger),
		"quantity":        fmt.Sprintf("%d", rec.Quantity),
		"ordertag":        tag,
	}
	if l := oe.orderLedger(); l != nil {
		if err := l.Submit(oms.Event{ClientOrderID: tag, Strategy: rec.StrategyName, PositionKey: posKey, Side: "SELL",
			Symbol: rec.Symbol, Token: rec.Token, Qty: rec.Quantity, Reason: "protective stop", Source: "protection"}); err != nil {
			oe.alertf("STOP for %s not journaled (%v) — placing anyway", posKey, err)
		}
	}
	attempt := orderAttempt{
		tag: tag,
		place: func(t string) (string, error) {
			api := oe.brokerAPI()
			if api == nil {
				return "", errNoSession
			}
			p := make(map[string]any, len(params))
			for k, v := range params {
				p[k] = v
			}
			p["ordertag"] = t
			return api.PlaceOrder(p)
		},
		orderBook:    oe.readOrderBook,
		refresh:      oe.RefreshSession,
		isFinal:      func(error) bool { return false },
		lookupDelays: defaultLookupDelays,
	}
	id, err := attempt.run()
	oe.journalPlacement(tag, id, err, false)
	if err != nil {
		oe.alertf("STOP failed for %s %s: %v — position has NO exchange-side stop", posKey, rec.Symbol, err)
		return ""
	}
	log.Printf("%s 🛡️  STOP placed: %s SELL %d trigger %s limit %s orderID=%s",
		oe.logPrefix(), rec.Symbol, rec.Quantity, paiseToRupees(trigger), paiseToRupees(limit), id)
	return id
}

// rearmStop places a stop for a held real position that has none. Caller
// holds the position lock.
func (oe *OrderExecutor) rearmStop(posKey string) {
	if !oe.stopMode() {
		return
	}
	rec, ok := oe.entry(posKey)
	if !ok || !rec.Real || rec.Pending || rec.SLOrderID != "" || rec.ExitOrderID != "" {
		return
	}
	if id := oe.placeStop(posKey, rec); id != "" {
		oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID, r.SLPlacedAt = id, time.Now() })
	}
}

// stopOutcome is what confirming a stop's cancel established.
type stopOutcome int

const (
	stopGone    stopOutcome = iota // cancelled (or never there); sell what is left
	stopFilled                     // the stop sold the whole position; nothing to sell
	stopUnknown                    // could not confirm; do not sell now
)

// cancelStopForExit cancels a position's stop before an exit sells, and
// reads the order book to learn what the stop really did. A partial stop
// fill is booked and the entry reduced. Caller holds the position lock.
func (oe *OrderExecutor) cancelStopForExit(sig strategy.Signal, posKey string) stopOutcome {
	rec, ok := oe.entry(posKey)
	if !ok || rec.SLOrderID == "" {
		return stopGone
	}
	api := oe.brokerAPI()
	if api == nil {
		return stopUnknown
	}
	id := rec.SLOrderID
	if res, err := api.CancelOrder(id, stopVariety); err != nil || !statusOK(res) {
		// A stop that already filled or was cancelled refuses the cancel;
		// the order book below says which.
		log.Printf("%s stop %s cancel refused (%v %v) — checking order book", oe.logPrefix(), id, err, res)
	}
	f, err := confirmFill(oe.readOrderBook, &oe.pushes, id, stopCancelDelays)
	if err == nil {
		oe.journalFill("", id, f, "protection")
	}
	switch {
	case err != nil && rec.SLPlacedAt.Before(startOfTodayIST()):
		// A DAY order from an earlier session is gone from today's book.
		oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID = "" })
		return stopGone
	case err != nil || !f.terminal():
		oe.alertf("stop %s for %s not confirmed cancelled (status %q, err %v) — exit held back to avoid selling twice; retrying",
			id, posKey, f.Status, err)
		return stopUnknown
	case f.Status == "complete":
		log.Printf("%s 🛡️  stop %s already FILLED for %s — position closed by the stop", oe.logPrefix(), id, posKey)
		oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID = "" })
		oe.finalizeExit(sig)
		oe.reportFill(sig, "SELL", oe.fillPriceOr(f, rec.Token), rec.Quantity, true)
		return stopFilled
	}
	// Cancelled or rejected: book whatever it sold before that.
	oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID = "" })
	if f.FilledQty > 0 {
		left := rec.Quantity - f.FilledQty
		oe.reportFill(sig, "SELL", oe.fillPriceOr(f, rec.Token), f.FilledQty, true)
		if left <= 0 {
			oe.finalizeExit(sig)
			return stopFilled
		}
		oe.updateEntry(posKey, func(r *OrderRecord) { r.Quantity = left })
		log.Printf("%s 🛡️  stop %s sold %d before cancel — %d left for %s", oe.logPrefix(), id, f.FilledQty, left, posKey)
	}
	return stopGone
}

// cancelStopQuiet cancels a position's stop without waiting, for an entry
// being dropped (closed another way, or never filled). Caller holds the
// position lock.
func (oe *OrderExecutor) cancelStopQuiet(posKey string) {
	rec, ok := oe.entry(posKey)
	if !ok || rec.SLOrderID == "" {
		return
	}
	api := oe.brokerAPI()
	if api == nil {
		return
	}
	if res, err := api.CancelOrder(rec.SLOrderID, stopVariety); err != nil || !statusOK(res) {
		msg := fmt.Sprint(err, res)
		if !orderAlreadyDone(msg) {
			oe.alertf("stop %s for closed %s %s not cancelled (%s) — cancel it in Angel One: it can sell short", rec.SLOrderID, posKey, rec.Symbol, msg)
		}
	}
}

// SweepProtection checks every held real position's stop: a filled stop
// closes the position, a missing or dead one is placed again. Call it
// periodically during market hours; it is a no-op outside stop mode or
// without a live session.
func (oe *OrderExecutor) SweepProtection() {
	if !oe.stopMode() || !oe.liveSession() {
		return
	}
	oe.mu.RLock()
	var keys []string
	for k, r := range oe.entryOrders {
		if r.Real && !r.Pending && r.ExitOrderID == "" && r.Leg == "" {
			keys = append(keys, k)
		}
	}
	oe.mu.RUnlock()
	for _, k := range keys {
		kl := oe.keyLock(k)
		kl.Lock()
		oe.sweepOne(k)
		kl.Unlock()
	}
	if len(keys) > 0 {
		oe.notifyState()
	}
}

func (oe *OrderExecutor) sweepOne(posKey string) {
	rec, ok := oe.entry(posKey)
	if !ok || !rec.Real || rec.Pending || rec.ExitOrderID != "" {
		return
	}
	if rec.SLOrderID == "" {
		oe.rearmStop(posKey)
		return
	}
	f, pushed := oe.pushes.terminal(rec.SLOrderID)
	if !pushed {
		book, err := oe.readOrderBookBackground()
		if err != nil {
			return
		}
		row, found := findOrderByID(book, rec.SLOrderID)
		if !found {
			if rec.SLPlacedAt.Before(startOfTodayIST()) {
				// Yesterday's DAY stop expired: protect today's session.
				oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID = "" })
				oe.rearmStop(posKey)
			}
			return
		}
		f = parseFillRow(row)
		oe.journalFill("", rec.SLOrderID, f, "sweep")
	}
	sig := signalFor(rec)
	switch {
	case f.Status == "complete":
		log.Printf("%s 🛡️  stop %s FILLED for %s — closing position", oe.logPrefix(), rec.SLOrderID, posKey)
		oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID = "" })
		oe.finalizeExit(sig)
		oe.reportFill(sig, "SELL", oe.fillPriceOr(f, rec.Token), rec.Quantity, true)
	case f.terminal():
		// Cancelled or rejected outside the executor: book any partial
		// sale and protect what is left.
		left := rec.Quantity - f.FilledQty
		if f.FilledQty > 0 {
			oe.reportFill(sig, "SELL", oe.fillPriceOr(f, rec.Token), f.FilledQty, true)
		}
		if left <= 0 {
			oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID = "" })
			oe.finalizeExit(sig)
			return
		}
		oe.updateEntry(posKey, func(r *OrderRecord) { r.SLOrderID, r.Quantity = "", left })
		oe.alertf("stop %s for %s ended %s (%s) — placing a new stop for %d", rec.SLOrderID, posKey, f.Status, f.Text, left)
		oe.rearmStop(posKey)
	}
}

func statusOK(res map[string]any) bool {
	st, ok := res["status"].(bool)
	return ok && st
}

// orderAlreadyDone reports a cancel refused because the order is already
// complete, cancelled or rejected.
func orderAlreadyDone(msg string) bool {
	s := strings.ToLower(msg)
	for _, m := range []string{"complete", "cancelled", "canceled", "rejected", "not found", "does not exist"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func startOfTodayIST() time.Time {
	now := time.Now().In(istZone)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, istZone)
}
