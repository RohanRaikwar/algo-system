package orderexec

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// Execution modes for real orders.
//
//   - ExecChase ("chase"): a marketable LIMIT (ask + N ticks for a BUY,
//     bid − N ticks for a SELL), re-priced up to ChaseReprices times while
//     it rests. A BUY that still has not filled is cancelled (any partial
//     fill is kept) — and never chased more than ChaseMaxBps above its
//     first price. A SELL ends as MARKET: an exit must happen.
//   - ExecMarket ("market"): the legacy FNOOrderType (MARKET or LIMIT).
const (
	ExecChase  = "chase"
	ExecMarket = "market"
)

// chasePollDelays spaces order-book reads between re-prices (~1.5s).
var chasePollDelays = []time.Duration{300 * time.Millisecond, 500 * time.Millisecond, 700 * time.Millisecond}

func (oe *OrderExecutor) chaseMode() bool { return oe.cfg.ExecMode == ExecChase }

func (oe *OrderExecutor) chaseTicks() int64 {
	if oe.cfg.ChaseTicks > 0 {
		return oe.cfg.ChaseTicks
	}
	return 2
}

// marketablePrice is a LIMIT price expected to fill now: a few ticks
// through the touch (quote when fresh, else LTP). 0 when no price is known.
func (oe *OrderExecutor) marketablePrice(direction, token string, ltp int64) int64 {
	tick := oe.tickPaise()
	base := ltp
	oe.mu.RLock()
	q := oe.quotes
	oe.mu.RUnlock()
	if q != nil {
		if bid, ask, ok := q.BidAsk(token, time.Now()); ok {
			if direction == "BUY" && ask > 0 {
				base = ask
			} else if direction == "SELL" && bid > 0 {
				base = bid
			}
		}
	}
	if base <= 0 {
		return 0
	}
	through := oe.chaseTicks() * tick
	if direction == "BUY" {
		return ceilTick(base+through, tick)
	}
	if p := floorTick(base-through, tick); p >= tick {
		return p
	}
	return tick
}

// orderPrice is the LIMIT price sent with a new real order (0 = MARKET).
func (oe *OrderExecutor) orderPrice(direction, token string, ltp int64) int64 {
	if oe.chaseMode() || strings.EqualFold(oe.cfg.FNOOrderType, "LIMIT") {
		return oe.marketablePrice(direction, token, ltp)
	}
	return 0
}

// awaitFill waits for a placed order's outcome: in chase mode it re-prices
// a resting LIMIT, then cancels a BUY or turns a SELL into MARKET; else it
// polls the book. A non-terminal result means it is still working (the
// caller follows it in the background).
func (oe *OrderExecutor) awaitFill(orderID, direction, symbol, token string, qty, price int64) (fillResult, error) {
	if !oe.chaseMode() || price <= 0 {
		return confirmFill(oe.readOrderBook, orderID, defaultFillPollDelays)
	}
	first := price
	for i := 0; ; i++ {
		f, err := confirmFill(oe.readOrderBook, orderID, chasePollDelays)
		if err == nil && f.terminal() {
			return f, nil
		}
		if i >= oe.cfg.ChaseReprices {
			break
		}
		next := oe.marketablePrice(direction, token, oe.GetLTP(token))
		if next <= 0 || next == price {
			continue
		}
		if direction == "BUY" && oe.cfg.ChaseMaxBps > 0 && (next-first)*10000 > first*oe.cfg.ChaseMaxBps {
			log.Printf("%s 🏃 chase BUY %s: %d is over %d bps above first price %d — giving up", oe.logPrefix(), orderID, next, oe.cfg.ChaseMaxBps, first)
			break
		}
		if err := oe.modifyOrder(orderID, symbol, token, "LIMIT", qty, next); err != nil {
			log.Printf("%s 🏃 chase %s %s: re-price to %d refused: %v", oe.logPrefix(), direction, orderID, next, err)
			continue
		}
		log.Printf("%s 🏃 chase %s %s: re-priced %d → %d", oe.logPrefix(), direction, orderID, price, next)
		price = next
	}

	if direction == "BUY" {
		api := oe.brokerAPI()
		if api == nil {
			return fillResult{}, errNoSession
		}
		if res, err := api.CancelOrder(orderID, "NORMAL"); err != nil || !statusOK(res) {
			log.Printf("%s 🏃 chase BUY %s: cancel refused (%v %v) — reading book", oe.logPrefix(), orderID, err, res)
		} else {
			log.Printf("%s 🏃 chase BUY %s: not filled after %d re-prices — cancelled", oe.logPrefix(), orderID, oe.cfg.ChaseReprices)
		}
		return confirmFill(oe.readOrderBook, orderID, defaultFillPollDelays)
	}
	if err := oe.modifyOrder(orderID, symbol, token, "MARKET", qty, 0); err != nil {
		log.Printf("%s 🏃 chase SELL %s: switch to MARKET refused: %v", oe.logPrefix(), orderID, err)
	} else {
		log.Printf("%s 🏃 chase SELL %s: still open — switched to MARKET", oe.logPrefix(), orderID)
	}
	return confirmFill(oe.readOrderBook, orderID, defaultFillPollDelays)
}

// modifyOrder changes a resting NORMAL order's type and price.
func (oe *OrderExecutor) modifyOrder(orderID, symbol, token, orderType string, qty, price int64) error {
	api := oe.brokerAPI()
	if api == nil {
		return errNoSession
	}
	params := map[string]any{
		"variety":       "NORMAL",
		"orderid":       orderID,
		"ordertype":     orderType,
		"producttype":   oe.cfg.FNOProductType,
		"duration":      "DAY",
		"quantity":      fmt.Sprintf("%d", qty),
		"tradingsymbol": symbol,
		"symboltoken":   token,
		"exchange":      oe.cfg.FNOExchange,
		"price":         "0",
	}
	if price > 0 {
		params["price"] = paiseToRupees(price)
	}
	res, err := api.ModifyOrder(params)
	if err != nil {
		return err
	}
	if !statusOK(res) {
		return fmt.Errorf("modify refused: %v", res)
	}
	return nil
}
