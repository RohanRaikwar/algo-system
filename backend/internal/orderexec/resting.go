package orderexec

import (
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/model"
)

// Resting entries: a signal that carries EntryLimitPaise (Telegram calls)
// is bought with one order that rests at the exchange instead of a chased
// marketable LIMIT:
//
//   - LTP below EntryTriggerPaise (or unknown): a STOPLOSS_LIMIT BUY, so the
//     exchange fires it at the trigger with no software in between, and it
//     fills at most at the limit.
//   - LTP at or above the trigger: a NORMAL LIMIT at the limit.
//
// The order is never re-priced above the limit. The entry stays Pending
// (blocks a second BUY) and is settled by the order-status push or the
// background poll; the order worker does not wait for the fill. An exit,
// or EntryExpiresAt, cancels an unfilled resting entry; a partial fill is
// kept and sold by the deferred exit.

// restingEntry reports whether sig is a BUY with an exchange-side entry.
func restingEntry(sig model.Signal) bool { return sig.EntryLimitPaise > 0 }

// restingOrderSpec is a resting entry's LIMIT price and, for a stop entry,
// its trigger (0 = plain LIMIT). The limit is floored to the tick so it
// never exceeds the signal's cap; the trigger is raised to the tick.
func (oe *OrderExecutor) restingOrderSpec(sig model.Signal, ltp int64) (limit, trigger int64) {
	tick := oe.tickPaise()
	limit = floorTick(sig.EntryLimitPaise, tick)
	if sig.EntryTriggerPaise > 0 && (ltp <= 0 || ltp < sig.EntryTriggerPaise) {
		trigger = ceilTick(sig.EntryTriggerPaise, tick)
		if limit < trigger {
			limit = trigger
		}
	}
	return limit, trigger
}

// entryVariety is the Angel variety of an entry order.
func entryVariety(trigger int64) string {
	if trigger > 0 {
		return stopVariety
	}
	return "NORMAL"
}

// SetEntryDropListener registers fn, called when a resting entry ends
// without a fill (cancelled, expired, rejected) or a paper resting entry
// is refused above its limit, so the strategy can forget the position. fn
// must not block.
func (oe *OrderExecutor) SetEntryDropListener(fn func(sig model.Signal, reason string)) {
	oe.mu.Lock()
	oe.onEntryDrop = fn
	oe.mu.Unlock()
}

func (oe *OrderExecutor) notifyEntryDropped(sig model.Signal, reason string) {
	oe.mu.RLock()
	fn := oe.onEntryDrop
	oe.mu.RUnlock()
	if fn != nil {
		fn(sig, reason)
	}
}

// cancelResting asks the broker to cancel a resting entry that has not
// filled. The outcome (cancelled, or filled first) settles through the
// push or the background poll. Caller holds the position lock.
func (oe *OrderExecutor) cancelResting(posKey, why string) {
	rec, ok := oe.entry(posKey)
	if !ok || !rec.Real || !rec.Pending || !rec.Resting || rec.OrderID == "" {
		return
	}
	api := oe.brokerAPI()
	if api == nil {
		oe.alertf("resting BUY %s for %s cannot be cancelled (%s): no broker session — cancel it in Angel One", rec.OrderID, posKey, why)
		return
	}
	variety := rec.Variety
	if variety == "" {
		variety = "NORMAL"
	}
	if res, err := api.CancelOrder(rec.OrderID, variety); err != nil || !statusOK(res) {
		msg := fmt.Sprint(err, res)
		if !orderAlreadyDone(msg) {
			oe.alertf("resting BUY %s for %s not cancelled (%s): %s — check Angel One", rec.OrderID, posKey, why, msg)
		}
		return
	}
	log.Printf("%s ✋ resting BUY %s for %s cancel sent (%s)", oe.logPrefix(), rec.OrderID, posKey, why)
}

// expireRestingAt cancels the resting entry orderID of posKey at at, if it
// is still unfilled then.
func (oe *OrderExecutor) expireRestingAt(posKey, orderID string, at time.Time) {
	if at.IsZero() {
		return
	}
	time.AfterFunc(time.Until(at), func() {
		kl := oe.keyLock(posKey)
		kl.Lock()
		defer kl.Unlock()
		if rec, ok := oe.entry(posKey); ok && rec.Pending && rec.OrderID == orderID {
			oe.cancelResting(posKey, "entry window ended")
		}
	})
}

// followResting tracks a placed resting entry until it settles. Caller
// holds the position lock.
func (oe *OrderExecutor) followResting(sig model.Signal, posKey string, rec OrderRecord) {
	oe.storeEntry(sig, rec)
	oe.followOrder(posKey, "resting BUY "+rec.OrderID, rec.OrderID, matchByID(rec.OrderID), func(oid string, f fillResult) {
		oe.onBuySettled(sig, oid, f)
	}, nil, false)
	oe.expireRestingAt(posKey, rec.OrderID, rec.RestUntil)
}

// paperRestingFill is a paper resting entry's fill: the trigger when the
// price is still below it (the exchange would fire there), else the paper
// fill. ok is false when that is above the limit (no fill).
func (oe *OrderExecutor) paperRestingFill(sig model.Signal, token string, ltp int64) (int64, bool) {
	limit, trigger := oe.restingOrderSpec(sig, ltp)
	fill := oe.paperFill("BUY", token, ltp)
	if trigger > 0 {
		fill = trigger
	}
	return fill, fill > 0 && fill <= limit
}
