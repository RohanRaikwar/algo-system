package orderexec

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/strategy"
)

// broker is the slice of the Angel One client the executor calls. Defined
// here, where it is consumed, so the order path can run against a fake.
type broker interface {
	PlaceOrder(params map[string]any) (string, error)
	PlaceOrderPaperTrade(params map[string]any) (string, error)
	OrderBook() (map[string]any, error)
	Position() (map[string]any, error)
	GTTCreateRule(params map[string]any) (string, error)
	GTTCancelRule(params map[string]any) (map[string]any, error)
}

var errNoSession = errors.New("no broker session")

// SetAlerter registers fn to receive operator alerts: situations that need
// a human (position possibly open without protection, exit not done, order
// state unknown). fn must not block.
func (oe *OrderExecutor) SetAlerter(fn func(msg string)) {
	oe.mu.Lock()
	oe.alerter = fn
	oe.mu.Unlock()
}

// alertf logs an operator alert and forwards it to the alerter.
func (oe *OrderExecutor) alertf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("%s 🚨 %s", oe.logPrefix(), msg)
	oe.mu.RLock()
	fn := oe.alerter
	oe.mu.RUnlock()
	if fn != nil {
		fn(msg)
	}
}

// Background settling cadence for orders whose outcome is not yet known:
// ambiguous placements and exits still open after the inline fill poll.
// ~2 minutes at a pace well inside the order-book rate limit.
var (
	asyncPollEvery = 2 * time.Second
	asyncPollTries = 60
)

// brokerAPI returns the broker under the lock (RefreshSession swaps it).
// A nil result means no session.
func (oe *OrderExecutor) brokerAPI() broker {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	return oe.api
}

// liveSession reports, under the lock, whether real orders can be sent.
func (oe *OrderExecutor) liveSession() bool {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	return oe.live && oe.sessionOK && oe.api != nil
}

// keyLock returns the mutex that serialises every order for one position
// (strategy + side), so a SELL can never overtake its own in-flight BUY.
func (oe *OrderExecutor) keyLock(posKey string) *sync.Mutex {
	m, _ := oe.keyLocks.LoadOrStore(posKey, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// inflightBuy reserves a position between the gate and a stored entry.
type inflightBuy struct {
	strategy string
	side     strategy.PositionSide
	real     bool
}

func opposite(side strategy.PositionSide) strategy.PositionSide {
	if side == strategy.SideCall {
		return strategy.SidePut
	}
	return strategy.SideCall
}

// oppositeConflictLocked reports whether a BUY for sig must wait: an
// opposite-side position (open, pending or in flight) exists for the same
// strategy, or — when the BUY is real — any real one. Caller holds oe.mu.
func (oe *OrderExecutor) oppositeConflictLocked(sig strategy.Signal, real bool) bool {
	opp := string(opposite(sig.Side))
	for _, r := range oe.entryOrders {
		if r.PositionSide == opp && (r.StrategyName == sig.StrategyName || (real && r.Real)) {
			return true
		}
	}
	for _, b := range oe.inflight {
		if string(b.side) == opp && (b.strategy == sig.StrategyName || (real && b.real)) {
			return true
		}
	}
	return false
}

// sideOpen reports whether any strategy holds, or is entering, side.
func (oe *OrderExecutor) sideOpen(side strategy.PositionSide) bool {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	for _, r := range oe.entryOrders {
		if r.PositionSide == string(side) {
			return true
		}
	}
	for _, b := range oe.inflight {
		if b.side == side {
			return true
		}
	}
	return false
}

func (oe *OrderExecutor) entry(posKey string) (OrderRecord, bool) {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	r, ok := oe.entryOrders[posKey]
	return r, ok
}

// updateEntry applies fn to the entry for posKey if it exists.
func (oe *OrderExecutor) updateEntry(posKey string, fn func(*OrderRecord)) {
	oe.mu.Lock()
	if r, ok := oe.entryOrders[posKey]; ok {
		fn(&r)
		oe.entryOrders[posKey] = r
	}
	oe.mu.Unlock()
}

// storeEntry records an entry and locks its instrument, without GTTs.
func (oe *OrderExecutor) storeEntry(sig strategy.Signal, rec OrderRecord) {
	if rec.IndexToken == "" {
		rec.IndexToken, rec.IndexExchange = sig.Token, sig.Exchange
	}
	oe.setPositionInstrument(sig, rec.Token, rec.Symbol)
	oe.mu.Lock()
	oe.entryOrders[oe.positionKey(sig)] = rec
	oe.mu.Unlock()
}

// commitEntry records a filled real entry and places its GTT protection
// synchronously, so the rule IDs are in the record (and the next snapshot)
// before anything else can act on the position. Caller holds the position
// lock.
func (oe *OrderExecutor) commitEntry(sig strategy.Signal, rec OrderRecord) {
	posKey := oe.positionKey(sig)
	rec.Pending = false
	// Rules already recorded (e.g. adopted again after a crash) are kept:
	// placing a second pair would orphan the first.
	if rec.GttRuleID == "" {
		rec.GttRuleID = oe.placeTargetGTT(posKey, rec.Symbol, rec.Token, rec.Price, rec.Quantity)
	}
	if rec.GttSLRuleID == "" {
		rec.GttSLRuleID = oe.placeStopLossGTT(posKey, rec.Symbol, rec.Token, rec.Price, rec.Quantity)
	}
	oe.storeEntry(sig, rec)
}

// onBuySettled applies a BUY's terminal order-book result to its Pending
// entry: adopt it with GTT protection (and send any exit deferred while it
// was pending), or drop it if nothing filled. Caller holds the position lock.
func (oe *OrderExecutor) onBuySettled(sig strategy.Signal, orderID string, f fillResult) {
	posKey := oe.positionKey(sig)
	cur, ok := oe.entry(posKey)
	if !ok {
		oe.alertf("BUY %s settled (%s) but its entry is gone — check Angel positions for an untracked %s", orderID, f.Status, sig.Side)
		return
	}
	rec, filled := oe.filledEntry(cur, f)
	if !filled {
		log.Printf("%s BUY %s settled: %s by broker (%s) — clearing", oe.logPrefix(), orderID, f.Status, f.Text)
		oe.dropEntry(sig)
		return
	}
	rec.OrderID = orderID
	log.Printf("%s ✅ BUY %s settled: FILLED qty=%d avg=%d — adopting with GTT protection", oe.logPrefix(), orderID, rec.Quantity, rec.Price)
	oe.commitEntry(sig, rec)
	oe.reportFill(sig, "BUY", rec.Price, rec.Quantity, true)
	if rec.ExitRequested {
		oe.sendDeferredExit(sig)
	}
}

// sendDeferredExit sends the exit that arrived while the BUY was pending.
// Caller holds the position lock.
func (oe *OrderExecutor) sendDeferredExit(sig strategy.Signal) {
	posKey := oe.positionKey(sig)
	rec, ok := oe.entry(posKey)
	if !ok {
		return
	}
	oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitRequested = false })
	if !oe.liveSession() {
		oe.alertf("deferred EXIT for %s %s cannot be sent: no live broker session — close manually", posKey, rec.Symbol)
		return
	}
	exit := sig
	exit.Action = strategy.ActionExit
	exit.Reason = "deferred exit (arrived while BUY was settling)"
	key := generateIdempotencyKey(exit)
	log.Printf("%s ▶️  sending deferred EXIT for %s %s qty=%d", oe.logPrefix(), posKey, rec.Symbol, rec.Quantity)
	oe.executeReal(exit, posKey, "SELL", rec.Token, rec.Symbol, oe.GetLTP(rec.Token), rec.Quantity, key)
}

// dropEntry forgets an entry that never became a live position.
func (oe *OrderExecutor) dropEntry(sig strategy.Signal) {
	posKey := oe.positionKey(sig)
	oe.cancelPairedGTTs(posKey)
	oe.mu.Lock()
	delete(oe.entryOrders, posKey)
	delete(oe.exitRetries, posKey)
	oe.mu.Unlock()
	oe.clearPositionInstrument(sig)
}

// finalizeExit closes a position the broker has confirmed flat.
func (oe *OrderExecutor) finalizeExit(sig strategy.Signal) {
	oe.dropEntry(sig)
	log.Printf("%s 🔓 %s position closed", oe.logPrefix(), sig.Side)
}

func (oe *OrderExecutor) reportFill(sig strategy.Signal, direction string, pricePaise, qty int64, real bool) {
	if pricePaise <= 0 {
		return
	}
	oe.mu.RLock()
	fn := oe.onFill
	oe.mu.RUnlock()
	if fn != nil {
		fn(FillReport{Signal: sig, Direction: direction, FillPricePaise: pricePaise, Qty: qty, Real: real})
	}
}

// signalFor rebuilds the signal identity of a stored entry, for settling
// it after a restart or retrying its exit. Token/Exchange are the index
// instrument that opened it, so fills land on the same P&L key.
func signalFor(rec OrderRecord) strategy.Signal {
	return strategy.Signal{
		StrategyName: rec.StrategyName,
		Side:         strategy.PositionSide(rec.PositionSide),
		Token:        rec.IndexToken,
		Exchange:     rec.IndexExchange,
	}
}

// pollOrderAsync follows one order until the book shows a terminal status,
// then calls done under the position lock. match selects the order's row.
// If the order never shows up, absent is called; if it stays non-terminal
// or the book stays unreadable for the whole window, an alert is logged and
// state is left for an operator.
func (oe *OrderExecutor) pollOrderAsync(posKey, what string, match func(map[string]any) (map[string]any, bool),
	done func(orderID string, f fillResult), absent func()) {
	if oe.brokerAPI() == nil {
		oe.alertf("%s: no broker session to settle order — check Angel order book manually", what)
		return
	}
	every, tries := asyncPollEvery, asyncPollTries
	slowEvery, slowTries := asyncSlowEvery, asyncSlowTries
	go func() {
		sawBook, seen, lastStatus := false, false, ""
		settleUnder := func(f func()) {
			kl := oe.keyLock(posKey)
			kl.Lock()
			f()
			kl.Unlock()
			oe.notifyState()
		}
		for i := 0; i < tries+slowTries; i++ {
			if i == tries {
				oe.alertf("%s: not settled after %s (last status %q, book readable=%v) — still following it slowly every %s; check Angel order book",
					what, time.Duration(tries)*every, lastStatus, sawBook, slowEvery)
			}
			if i < tries {
				time.Sleep(every)
			} else {
				time.Sleep(slowEvery)
			}
			book, err := oe.readOrderBookBackground()
			if err != nil {
				continue
			}
			sawBook = true
			row, ok := match(book)
			if !ok {
				// Absent for the whole fast window with a readable book: the
				// order never reached the broker.
				if !seen && i == tries-1 && absent != nil {
					settleUnder(absent)
					return
				}
				continue
			}
			seen = true
			f := parseFillRow(row)
			lastStatus = f.Status
			if !f.terminal() {
				continue
			}
			oid := toStr(row["orderid"])
			settleUnder(func() { done(oid, f) })
			return
		}
		oe.alertf("%s: gave up after %s (last status %q, book readable=%v) — check Angel order book and positions manually",
			what, time.Duration(tries)*every+time.Duration(slowTries)*slowEvery, lastStatus, sawBook)
	}()
}

func matchByID(orderID string) func(map[string]any) (map[string]any, bool) {
	return func(book map[string]any) (map[string]any, bool) { return findOrderByID(book, orderID) }
}

// matchByTags finds the live order among an attempt's tags (first and
// retry), preferring a row that is not rejected.
func matchByTags(tags ...string) func(map[string]any) (map[string]any, bool) {
	return func(book map[string]any) (map[string]any, bool) {
		var fallback map[string]any
		for _, t := range tags {
			if row, ok := findOrderByTag(book, t); ok {
				if !rowRejected(row) {
					return row, true
				}
				fallback = row
			}
		}
		return fallback, fallback != nil
	}
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// ResumeSettlement restarts background settling for restored entries whose
// outcome was still open at shutdown: unsettled BUYs (Pending) and exits
// sent but not confirmed (ExitOrderID). Call after Restore. Fill-price P&L
// corrections are not replayed for these.
func (oe *OrderExecutor) ResumeSettlement() {
	prefix := oe.logPrefix()
	oe.mu.RLock()
	var recs []OrderRecord
	for _, r := range oe.entryOrders {
		if r.Real && (r.Pending || r.ExitOrderID != "") {
			recs = append(recs, r)
		}
	}
	oe.mu.RUnlock()

	for _, rec := range recs {
		rec := rec
		sig := signalFor(rec)
		posKey := oe.positionKey(sig)
		log.Printf("%s 🔁 resuming settlement for %s (pending=%v exit=%q)", prefix, posKey, rec.Pending, rec.ExitOrderID)

		if rec.Pending && beforeTodayIST(rec.Timestamp) {
			// The order book only covers today: settle from positions.
			oe.settleStalePending(sig, rec)
			continue
		}
		if rec.Pending {
			match := matchByTags(rec.ClientOrderID, retryTag(rec.ClientOrderID))
			if rec.OrderID != "" {
				match = matchByID(rec.OrderID)
			}
			oe.pollOrderAsync(posKey, "restored pending BUY "+rec.ClientOrderID, match,
				func(orderID string, f fillResult) {
					oe.onBuySettled(sig, orderID, f)
				}, func() { oe.dropEntry(sig) })
			continue
		}

		match := matchByID(rec.ExitOrderID)
		if tag, ok := strings.CutPrefix(rec.ExitOrderID, "tag:"); ok {
			match = matchByTags(tag, retryTag(tag))
		}
		oe.pollOrderAsync(posKey, "restored SELL "+rec.ExitOrderID, match,
			func(orderID string, f fillResult) {
				oe.applyExitResult(sig, orderID, f)
			}, func() {
				oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "" })
				oe.alertf("restored EXIT for %s never reached broker — position still OPEN, retrying the exit", posKey)
				oe.scheduleExitRetry(sig)
			})
	}
}

// applyExitResult settles a real SELL from its terminal order-book result.
// Complete closes the position; a partial fill reduces it and leaves the
// rest open; a rejection leaves it open. Caller holds the position lock.
func (oe *OrderExecutor) applyExitResult(sig strategy.Signal, orderID string, f fillResult) {
	posKey := oe.positionKey(sig)
	rec, _ := oe.entry(posKey)
	switch {
	case f.Rejected():
		oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "" })
		oe.alertf("EXIT %s %s by broker (%s) — %s position still OPEN, retrying the exit", orderID, f.Status, f.Text, rec.Symbol)
		oe.scheduleExitRetry(sig)
	case f.Partial() && f.FilledQty < rec.Quantity:
		left := rec.Quantity - f.FilledQty
		oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = ""; r.Quantity = left })
		oe.alertf("EXIT %s only partly filled (%d of %d) — %d of %s still OPEN; paired GTTs still sized %d, retrying the rest",
			orderID, f.FilledQty, rec.Quantity, left, rec.Symbol, rec.Quantity)
		oe.reportFill(sig, "SELL", oe.fillPriceOr(f, rec.Token), f.FilledQty, true)
		oe.scheduleExitRetry(sig)
	default:
		oe.finalizeExit(sig)
		oe.reportFill(sig, "SELL", oe.fillPriceOr(f, rec.Token), rec.Quantity, true)
	}
}

// fillPriceOr is the fill's average price, or the token's LTP when the
// broker did not report one.
func (oe *OrderExecutor) fillPriceOr(f fillResult, token string) int64 {
	if f.AvgPricePaise > 0 {
		return f.AvgPricePaise
	}
	return oe.GetLTP(token)
}

// exitRetryDelays spaces automatic resends of a real exit that failed, was
// rejected, or only partly filled. The strategy has already gone flat on
// its exit signal, so without a resend nothing would close the position
// but its GTT stop loss.
var exitRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}

// scheduleExitRetry resends the exit for sig's position after the next
// delay in exitRetryDelays, and alerts once they are used up.
func (oe *OrderExecutor) scheduleExitRetry(sig strategy.Signal) {
	posKey := oe.positionKey(sig)
	oe.mu.Lock()
	n := oe.exitRetries[posKey]
	if n >= len(exitRetryDelays) {
		oe.mu.Unlock()
		oe.alertf("EXIT for %s still not done after %d automatic retries — position OPEN, close manually", posKey, n)
		return
	}
	oe.exitRetries[posKey] = n + 1
	oe.mu.Unlock()

	delay := exitRetryDelays[n]
	log.Printf("%s 🔁 exit retry %d/%d for %s in %s", oe.logPrefix(), n+1, len(exitRetryDelays), posKey, delay)
	go func() {
		time.Sleep(delay)
		kl := oe.keyLock(posKey)
		kl.Lock()
		oe.retryExitLocked(sig, n+1)
		kl.Unlock()
		oe.notifyState()
	}()
}

// retryExitLocked resends a real exit if the position is still open and
// nothing else is closing it. The broker's net position decides the size,
// so a position closed another way (GTT, by hand) is finalised instead of
// sold short. Caller holds the position lock.
func (oe *OrderExecutor) retryExitLocked(sig strategy.Signal, attempt int) {
	prefix := oe.logPrefix()
	posKey := oe.positionKey(sig)
	rec, ok := oe.entry(posKey)
	if !ok || !rec.Real || rec.Pending || rec.ExitOrderID != "" {
		return // closed, still settling (its deferred exit covers it), or another exit in flight
	}
	if !oe.liveSession() {
		oe.alertf("exit retry for %s: no live broker session", posKey)
		oe.scheduleExitRetry(sig)
		return
	}
	held, err := oe.brokerNetQty(rec.Token)
	switch {
	case err != nil:
		log.Printf("%s ⚠️  exit retry for %s: broker positions unreadable: %v", prefix, posKey, err)
		oe.scheduleExitRetry(sig)
		return
	case held < 0:
		oe.alertf("exit retry for %s: broker shows %s SHORT %d — not selling; check manually", posKey, rec.Symbol, held)
		return
	case held == 0:
		log.Printf("%s ✅ exit retry for %s: broker already flat — closing", prefix, posKey)
		oe.finalizeExit(sig)
		oe.reportFill(sig, "SELL", oe.GetLTP(rec.Token), rec.Quantity, true)
		return
	}
	qty := rec.Quantity
	if held < qty {
		qty = held
	}
	exit := sig
	exit.Action = strategy.ActionExit
	exit.Reason = fmt.Sprintf("automatic exit retry %d", attempt)
	key := generateIdempotencyKey(exit)
	log.Printf("%s ▶️  exit retry %d for %s: SELL %d %s", prefix, attempt, posKey, qty, rec.Symbol)
	oe.executeReal(exit, posKey, "SELL", rec.Token, rec.Symbol, oe.GetLTP(rec.Token), qty, key)
}

// SetIntentPersister registers fn to save executor state synchronously
// just before a real order is sent. If fn fails a real BUY is not sent (an
// entry can always be skipped); a real SELL is sent regardless.
func (oe *OrderExecutor) SetIntentPersister(fn func() error) {
	oe.mu.Lock()
	oe.persistIntent = fn
	oe.mu.Unlock()
}

func (oe *OrderExecutor) persistIntentNow() error {
	oe.mu.RLock()
	fn := oe.persistIntent
	oe.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn()
}

// filledEntry turns a terminal BUY result into the entry to record. ok is
// false when nothing was filled.
func (oe *OrderExecutor) filledEntry(rec OrderRecord, f fillResult) (OrderRecord, bool) {
	if f.Rejected() {
		return rec, false
	}
	if f.AvgPricePaise > 0 {
		rec.Price = f.AvgPricePaise
	}
	if f.Partial() {
		rec.Quantity = f.FilledQty
	}
	return rec, true
}

// beforeTodayIST reports whether t falls on an earlier IST calendar day.
func beforeTodayIST(t time.Time) bool {
	now := time.Now().In(istZone)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, istZone)
	return t.Before(midnight)
}

// settleStalePending settles a BUY left Pending on a previous day, which
// the day-scoped order book can no longer show, from broker positions.
func (oe *OrderExecutor) settleStalePending(sig strategy.Signal, rec OrderRecord) {
	posKey := oe.positionKey(sig)
	kl := oe.keyLock(posKey)
	kl.Lock()
	defer kl.Unlock()
	held, err := oe.brokerNetQty(rec.Token)
	switch {
	case err != nil:
		oe.alertf("pending BUY %s from a previous day could not be checked (%v) — kept pending; verify Angel positions for %s", posKey, err, rec.Symbol)
	case held > 0:
		oe.updateEntry(posKey, func(r *OrderRecord) { r.Pending, r.Quantity = false, held })
		oe.alertf("pending BUY %s from a previous day: broker holds %d %s — kept OPEN without GTT protection (fill price unknown); protect or close manually", posKey, held, rec.Symbol)
	default:
		oe.dropEntry(sig)
		oe.alertf("pending BUY %s from a previous day: broker holds none of %s — dropped", posKey, rec.Symbol)
	}
	oe.notifyState()
}

// After the fast window, settling continues at a slow pace (≈4h) instead
// of giving up with a position stuck Pending or an exit parked.
var (
	asyncSlowEvery = 30 * time.Second
	asyncSlowTries = 480
)

// SetStateListener registers fn to run after any background state change
// (settlement, adoption, stale-day handling) so the caller can persist a
// snapshot immediately. fn runs on a background goroutine.
func (oe *OrderExecutor) SetStateListener(fn func()) {
	oe.mu.Lock()
	oe.onState = fn
	oe.mu.Unlock()
}

func (oe *OrderExecutor) notifyState() {
	oe.mu.RLock()
	fn := oe.onState
	oe.mu.RUnlock()
	if fn != nil {
		fn()
	}
}
