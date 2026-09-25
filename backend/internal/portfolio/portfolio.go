// Package portfolio tracks positions, P&L, and portfolio-level metrics.
//
// It maintains a real-time view of all open positions, calculates unrealized
// P&L from latest market prices, and provides exposure summaries.
package portfolio

import (
	"encoding/json"
	"sync"

	"trading-systemv1/internal/model"
)

// Position represents a single instrument position.
type Position struct {
	Token    string `json:"token"`
	Exchange string `json:"exchange"`
	Qty      int64  `json:"qty"`       // positive = long, negative = short
	AvgPrice int64  `json:"avg_price"` // average entry price in paise
	LastLTP  int64  `json:"last_ltp"`  // last traded price in paise
}

// UnrealizedPnL returns the unrealized P&L in paise.
func (p *Position) UnrealizedPnL() int64 {
	return (p.LastLTP - p.AvgPrice) * p.Qty
}

// Portfolio tracks all open positions.
type Portfolio struct {
	mu        sync.RWMutex
	positions map[string]*Position // key = "exchange:token"
}

// New creates a new empty Portfolio.
func New() *Portfolio {
	return &Portfolio{
		positions: make(map[string]*Position),
	}
}

// OpenPosition adds or updates a position.
// If a position already exists for the token, qty is added and avg price is recomputed.
func (pf *Portfolio) OpenPosition(token, exchange string, qty, price int64) {
	key := exchange + ":" + token
	pf.mu.Lock()
	defer pf.mu.Unlock()

	if pos, ok := pf.positions[key]; ok {
		// Weighted average price
		totalCost := pos.AvgPrice*pos.Qty + price*qty
		pos.Qty += qty
		if pos.Qty != 0 {
			pos.AvgPrice = totalCost / pos.Qty
		}
	} else {
		pf.positions[key] = &Position{
			Token:    token,
			Exchange: exchange,
			Qty:      qty,
			AvgPrice: price,
		}
	}
}

// ClosePosition removes and returns the position for the given token.
// Returns nil if no position exists.
func (pf *Portfolio) ClosePosition(token, exchange string) *Position {
	key := exchange + ":" + token
	pf.mu.Lock()
	defer pf.mu.Unlock()

	pos, ok := pf.positions[key]
	if !ok {
		return nil
	}
	delete(pf.positions, key)
	cp := *pos
	return &cp
}

// UpdatePrice updates the last traded price for a position.
func (pf *Portfolio) UpdatePrice(candle model.Candle) {
	key := candle.Exchange + ":" + candle.Token
	pf.mu.Lock()
	defer pf.mu.Unlock()
	if pos, ok := pf.positions[key]; ok {
		pos.LastLTP = candle.Close
	}
}

// GetPositions returns a snapshot of all positions.
func (pf *Portfolio) GetPositions() []Position {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	result := make([]Position, 0, len(pf.positions))
	for _, p := range pf.positions {
		result = append(result, *p)
	}
	return result
}

// PositionCount returns the number of open positions.
func (pf *Portfolio) PositionCount() int {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	return len(pf.positions)
}

// TotalExposure returns the sum of abs(qty * avgPrice) across all positions (in paise).
func (pf *Portfolio) TotalExposure() int64 {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	var total int64
	for _, p := range pf.positions {
		exp := p.Qty * p.AvgPrice
		if exp < 0 {
			exp = -exp
		}
		total += exp
	}
	return total
}

// TotalUnrealizedPnL returns the total unrealized P&L across all positions.
func (pf *Portfolio) TotalUnrealizedPnL() int64 {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	var total int64
	for _, p := range pf.positions {
		total += p.UnrealizedPnL()
	}
	return total
}

// Snapshot serializes the portfolio positions to JSON.
func (pf *Portfolio) Snapshot() ([]byte, error) {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	positions := make([]Position, 0, len(pf.positions))
	for _, p := range pf.positions {
		positions = append(positions, *p)
	}
	return json.Marshal(positions)
}

// RestorePositions restores portfolio positions from a JSON snapshot.
func (pf *Portfolio) RestorePositions(data []byte) error {
	var positions []Position
	if err := json.Unmarshal(data, &positions); err != nil {
		return err
	}
	pf.mu.Lock()
	defer pf.mu.Unlock()
	pf.positions = make(map[string]*Position, len(positions))
	for i := range positions {
		key := positions[i].Exchange + ":" + positions[i].Token
		p := positions[i]
		pf.positions[key] = &p
	}
	return nil
}
