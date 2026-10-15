package orderexec

import (
	"strings"

	"trading-systemv1/internal/underlying"
)

// extraProfileOf returns the non-primary underlying whose options symbol
// belongs to (prefix "SENSEX…"), or ok=false for NIFTY and anything
// unknown. NIFTY keeps every executor default, so its orders are unchanged.
func extraProfileOf(symbol string) (underlying.Profile, bool) {
	all := underlying.All()
	for _, p := range all[1:] {
		if strings.HasPrefix(symbol, p.OptionName) {
			return p, true
		}
	}
	return underlying.Profile{}, false
}

// exchangeFor is the options segment an order for symbol goes to: the
// underlying's (BFO for SENSEX), else the configured FNOExchange.
func (oe *OrderExecutor) exchangeFor(symbol string) string {
	if p, ok := extraProfileOf(symbol); ok {
		return p.FNOExchange
	}
	return oe.cfg.FNOExchange
}

// freezeQtyFor is the exchange freeze quantity for symbol's underlying.
func (oe *OrderExecutor) freezeQtyFor(symbol string) int64 {
	if p, ok := extraProfileOf(symbol); ok && p.FreezeQty > 0 {
		return int64(p.FreezeQty)
	}
	return oe.cfg.FreezeQty
}

// paperQtyFor is the size of a paper trade in symbol. NIFTY uses STRAT_QTY
// as before; another underlying trades LiveLots of its own lot size, so its
// paper P&L is on a quantity the exchange would accept.
func (oe *OrderExecutor) paperQtyFor(symbol string) int64 {
	p, ok := extraProfileOf(symbol)
	if !ok {
		return oe.cfg.Qty
	}
	lot, err := oe.lotSizeOf(symbol)
	if err != nil || lot <= 0 {
		lot = int64(p.LotFallback)
	}
	return oe.cfg.LiveLots * lot
}

// underlyingOfStrategy is the underlying key a strategy trades: its
// profile's when it is an SR strategy of an extra underlying, else the
// primary (NIFTY).
func underlyingOfStrategy(name string) string {
	if p, ok := underlying.ByStrategy(name); ok {
		return p.Key
	}
	return underlying.Primary().Key
}
