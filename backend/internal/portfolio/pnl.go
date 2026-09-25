package portfolio

import (
	"encoding/json"
	"sync"
	"time"
)

// Trade represents a completed trade for P&L calculation.
type Trade struct {
	Token        string    `json:"token"`
	Exchange     string    `json:"exchange"`
	Action       string    `json:"action"` // BUY or SELL
	Qty          int64     `json:"qty"`
	Price        int64     `json:"price"` // in paise
	Timestamp    time.Time `json:"timestamp"`
	StrategyName string    `json:"strategy_name,omitempty"` // Strategy that placed the trade
}

// DailyStats holds aggregated stats for the current trading day.
type DailyStats struct {
	TradeCount  int     `json:"trade_count"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	WinRate     float64 `json:"win_rate"`     // 0–100
	LargestWin  int64   `json:"largest_win"`  // paise
	LargestLoss int64   `json:"largest_loss"` // paise (negative)
}

// PnLTracker tracks realized and unrealized P&L.
type PnLTracker struct {
	mu     sync.RWMutex
	trades []Trade

	// Realized P&L from closed positions (in paise)
	realizedPnL int64

	// Per-token cost basis for P&L calculation
	costBasis map[string]costEntry

	// Daily tracking
	dailyTradeCount  int
	dailyWins        int
	dailyLosses      int
	dailyLargestWin  int64
	dailyLargestLoss int64

	// Per-strategy daily P&L tracking (strategy name → realized P&L in paise)
	strategyDailyPnL map[string]int64
}

type costEntry struct {
	Qty      int64
	AvgPrice int64 // in paise
}

// pnlSnapshot is the JSON-serializable shape for Snapshot/Restore.
type pnlSnapshot struct {
	Trades           []Trade              `json:"trades"`
	RealizedPnL      int64                `json:"realized_pnl"`
	CostBasis        map[string]costEntry `json:"cost_basis"`
	DailyTradeCount  int                  `json:"daily_trade_count"`
	DailyWins        int                  `json:"daily_wins"`
	DailyLosses      int                  `json:"daily_losses"`
	DailyLargestWin  int64                `json:"daily_largest_win"`
	DailyLargestLoss int64                `json:"daily_largest_loss"`
	StrategyDailyPnL map[string]int64     `json:"strategy_daily_pnl,omitempty"`
}

// NewPnLTracker creates a new P&L tracker.
func NewPnLTracker() *PnLTracker {
	return &PnLTracker{
		trades:           make([]Trade, 0, 500),
		costBasis:        make(map[string]costEntry),
		strategyDailyPnL: make(map[string]int64),
	}
}

// RecordTrade records a trade and updates realized P&L and daily stats.
func (p *PnLTracker) RecordTrade(trade Trade) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.trades = append(p.trades, trade)
	p.dailyTradeCount++
	key := trade.Exchange + ":" + trade.Token
	entry := p.costBasis[key]

	var realizedPnL int64

	if trade.Action == "BUY" {
		// Increase position
		if entry.Qty == 0 {
			entry.Qty = trade.Qty
			entry.AvgPrice = trade.Price
		} else {
			// Weighted average price
			totalCost := entry.AvgPrice*entry.Qty + trade.Price*trade.Qty
			entry.Qty += trade.Qty
			if entry.Qty > 0 {
				entry.AvgPrice = totalCost / entry.Qty
			}
		}
	} else {
		// Reduce position — calculate realized P&L
		sellQty := trade.Qty
		if sellQty > entry.Qty {
			sellQty = entry.Qty
		}
		realizedPnL = (trade.Price - entry.AvgPrice) * sellQty
		entry.Qty -= sellQty
		if entry.Qty <= 0 {
			entry.Qty = 0
			entry.AvgPrice = 0
		}
		p.realizedPnL += realizedPnL

		// Track per-strategy daily P&L
		if trade.StrategyName != "" {
			p.strategyDailyPnL[trade.StrategyName] += realizedPnL
		}

		// Track wins/losses
		if realizedPnL > 0 {
			p.dailyWins++
			if realizedPnL > p.dailyLargestWin {
				p.dailyLargestWin = realizedPnL
			}
		} else if realizedPnL < 0 {
			p.dailyLosses++
			if realizedPnL < p.dailyLargestLoss {
				p.dailyLargestLoss = realizedPnL
			}
		}
	}

	p.costBasis[key] = entry
	return realizedPnL
}

// CorrectLastFill replaces the price of the most recent matching trade with
// the broker's actual fill price and carries the difference into P&L.
// Trades are recorded at signal time from the LTP; the fill confirmation
// arrives later. For a BUY still open, the cost basis moves; for a BUY
// already closed, or a SELL, realized P&L moves. Win/loss tallies are not
// re-evaluated. Returns the realized P&L change and whether a trade matched.
func (p *PnLTracker) CorrectLastFill(strategyName, exchange, token, action string, fillPrice int64) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	idx := -1
	for i := len(p.trades) - 1; i >= 0; i-- {
		t := p.trades[i]
		if t.StrategyName == strategyName && t.Exchange == exchange && t.Token == token && t.Action == action {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, false
	}
	tr := &p.trades[idx]
	diff := fillPrice - tr.Price
	tr.Price = fillPrice
	if diff == 0 {
		return 0, true
	}

	key := exchange + ":" + token
	var realizedDelta int64
	if action == "BUY" {
		if entry := p.costBasis[key]; entry.Qty > 0 {
			entry.AvgPrice += diff * tr.Qty / entry.Qty
			p.costBasis[key] = entry
			return 0, true
		}
		realizedDelta = -diff * tr.Qty // paid more → earned less
	} else {
		realizedDelta = diff * tr.Qty
	}
	p.realizedPnL += realizedDelta
	if strategyName != "" {
		p.strategyDailyPnL[strategyName] += realizedDelta
	}
	return realizedDelta, true
}

// GetStrategyDailyPnL returns the daily realized P&L for a specific strategy (in paise).
func (p *PnLTracker) GetStrategyDailyPnL(strategyName string) int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.strategyDailyPnL[strategyName]
}

// GetRealizedPnL returns total realized P&L in paise.
func (p *PnLTracker) GetRealizedPnL() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.realizedPnL
}

// GetUnrealizedPnL calculates unrealized P&L from current prices.
// currentPrices maps "exchange:token" -> latest price in paise.
func (p *PnLTracker) GetUnrealizedPnL(currentPrices map[string]int64) int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var unrealized int64
	for key, entry := range p.costBasis {
		if entry.Qty <= 0 {
			continue
		}
		if price, ok := currentPrices[key]; ok {
			unrealized += (price - entry.AvgPrice) * entry.Qty
		}
	}
	return unrealized
}

// GetTrades returns a snapshot of all trades.
func (p *PnLTracker) GetTrades() []Trade {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cp := make([]Trade, len(p.trades))
	copy(cp, p.trades)
	return cp
}

// GetDailyStats returns aggregated stats for the current trading day.
func (p *PnLTracker) GetDailyStats() DailyStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	winRate := 0.0
	total := p.dailyWins + p.dailyLosses
	if total > 0 {
		winRate = float64(p.dailyWins) / float64(total) * 100
	}

	return DailyStats{
		TradeCount:  p.dailyTradeCount,
		Wins:        p.dailyWins,
		Losses:      p.dailyLosses,
		WinRate:     winRate,
		LargestWin:  p.dailyLargestWin,
		LargestLoss: p.dailyLargestLoss,
	}
}

// ResetDaily clears daily counters (call at market open).
func (p *PnLTracker) ResetDaily() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dailyTradeCount = 0
	p.dailyWins = 0
	p.dailyLosses = 0
	p.dailyLargestWin = 0
	p.dailyLargestLoss = 0
	p.strategyDailyPnL = make(map[string]int64)
}

// Summary returns a P&L summary.
type PnLSummary struct {
	RealizedPnL   int64 `json:"realized_pnl"`
	UnrealizedPnL int64 `json:"unrealized_pnl"`
	TotalPnL      int64 `json:"total_pnl"`
	TotalTrades   int   `json:"total_trades"`
	OpenPositions int   `json:"open_positions"`
}

// GetSummary returns the current P&L summary.
func (p *PnLTracker) GetSummary(currentPrices map[string]int64) PnLSummary {
	p.mu.RLock()
	defer p.mu.RUnlock()

	unrealized := int64(0)
	openPositions := 0
	for key, entry := range p.costBasis {
		if entry.Qty <= 0 {
			continue
		}
		openPositions++
		if price, ok := currentPrices[key]; ok {
			unrealized += (price - entry.AvgPrice) * entry.Qty
		}
	}

	return PnLSummary{
		RealizedPnL:   p.realizedPnL,
		UnrealizedPnL: unrealized,
		TotalPnL:      p.realizedPnL + unrealized,
		TotalTrades:   len(p.trades),
		OpenPositions: openPositions,
	}
}

// Snapshot serializes the PnL tracker state to JSON.
func (p *PnLTracker) Snapshot() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snap := pnlSnapshot{
		Trades:           p.trades,
		RealizedPnL:      p.realizedPnL,
		CostBasis:        p.costBasis,
		DailyTradeCount:  p.dailyTradeCount,
		DailyWins:        p.dailyWins,
		DailyLosses:      p.dailyLosses,
		DailyLargestWin:  p.dailyLargestWin,
		DailyLargestLoss: p.dailyLargestLoss,
		StrategyDailyPnL: p.strategyDailyPnL,
	}
	return json.Marshal(snap)
}

// RestorePnL restores PnL tracker state from a JSON snapshot.
func (p *PnLTracker) RestorePnL(data []byte) error {
	var snap pnlSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trades = snap.Trades
	p.realizedPnL = snap.RealizedPnL
	p.costBasis = snap.CostBasis
	p.dailyTradeCount = snap.DailyTradeCount
	p.dailyWins = snap.DailyWins
	p.dailyLosses = snap.DailyLosses
	p.dailyLargestWin = snap.DailyLargestWin
	p.dailyLargestLoss = snap.DailyLargestLoss
	// Per-strategy daily P&L feeds the profit cap; without it a restart
	// would reset the cap mid-day.
	if snap.StrategyDailyPnL != nil {
		p.strategyDailyPnL = snap.StrategyDailyPnL
	}
	return nil
}
