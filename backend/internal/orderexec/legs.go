package orderexec

import (
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/strategy"
)

// executeLegPaper runs one leg of a multi-leg (basket) signal. Legs are
// paper-only by construction: this path never touches the broker, the
// real-order gate, GTTs, the rate limiter or the circuit breaker, whatever
// the strategy name or session state.
//
// A long leg opens with BUY and closes with SELL. A short leg (sig.Short)
// opens with SELL and closes with BUY. Each leg is its own position under
// strategy|side|leg, so legs of one basket never collide.
func (oe *OrderExecutor) executeLegPaper(sig strategy.Signal) {
	prefix := oe.logPrefix()
	if sig.Side != strategy.SideCall && sig.Side != strategy.SidePut {
		log.Printf("%s leg %s: side %q — cannot determine CALL/PUT", prefix, sig.Leg, sig.Side)
		return
	}
	var isExit bool
	switch sig.Action {
	case strategy.ActionBuy:
	case strategy.ActionExit, strategy.ActionSell:
		isExit = true
	default:
		log.Printf("%s leg %s: unknown action %q", prefix, sig.Leg, sig.Action)
		return
	}

	posKey := oe.positionKey(sig)
	kl := oe.keyLock(posKey)
	kl.Lock()
	defer kl.Unlock()

	if isExit {
		rec, ok := oe.entry(posKey)
		if !ok {
			log.Printf("%s 📋 PAPER leg EXIT %s: nothing open", prefix, posKey)
			return
		}
		closeDir := legDirection(rec.Short, true)
		ltp := oe.paperFill(closeDir, rec.Token, oe.GetLTP(rec.Token))
		oe.mu.Lock()
		delete(oe.entryOrders, posKey)
		delete(oe.positionInst, posKey)
		oe.mu.Unlock()
		// The locked contract decides what is closed, not the exit signal.
		closeSig := sig
		closeSig.FNOToken, closeSig.FNOSymbol, closeSig.Short = rec.Token, rec.Symbol, rec.Short
		log.Printf("%s 📋 PAPER leg %s %s %s (%s) qty=%d ltp=%d entry=%d reason=%s",
			prefix, closeDir, posKey, rec.Symbol, rec.Token, rec.Quantity, ltp, rec.Price, sig.Reason)
		oe.reportFill(closeSig, closeDir, ltp, rec.Quantity, false)
		return
	}
	openDir := legDirection(sig.Short, false)

	if sig.FNOToken == "" {
		log.Printf("%s 🚫 leg %s: no option token — entry skipped", prefix, posKey)
		return
	}
	oe.mu.Lock()
	if _, open := oe.entryOrders[posKey]; open {
		oe.mu.Unlock()
		log.Printf("%s 🚫 BLOCKED: leg %s already open", prefix, posKey)
		return
	}
	// oe.mu is already held (write lock) here for the open-check/reserve
	// below; paperFill's own RLock would deadlock against it (RWMutex is
	// not reentrant), so call the lock-free variant instead.
	ltp := oe.paperFillLocked(openDir, sig.FNOToken, oe.ltp[sig.FNOToken])
	orderID := fmt.Sprintf("PAPER_%s_%s_%s_%d", sig.StrategyName, sig.Leg, openDir, time.Now().UnixMilli())
	oe.positionInst[posKey] = fnoInstrument{Token: sig.FNOToken, Symbol: sig.FNOSymbol}
	oe.entryOrders[posKey] = OrderRecord{
		OrderID:       orderID,
		ClientOrderID: generateIdempotencyKey(sig),
		Symbol:        sig.FNOSymbol,
		Token:         sig.FNOToken,
		Side:          openDir,
		Quantity:      oe.cfg.Qty,
		Price:         ltp,
		Timestamp:     time.Now(),
		StrategyName:  sig.StrategyName,
		PositionSide:  string(sig.Side),
		IndexToken:    sig.Token,
		IndexExchange: sig.Exchange,
		Leg:           sig.Leg,
		Short:         sig.Short,
	}
	oe.mu.Unlock()
	log.Printf("%s 📋 PAPER leg %s %s %s (%s) qty=%d ltp=%d → %s reason=%s",
		prefix, openDir, posKey, sig.FNOSymbol, sig.FNOToken, oe.cfg.Qty, ltp, orderID, sig.Reason)
	oe.reportFill(sig, openDir, ltp, oe.cfg.Qty, false)
}

// legDirection is the order direction for opening or closing a leg.
func legDirection(short, closing bool) string {
	if short != closing {
		return "SELL"
	}
	return "BUY"
}
