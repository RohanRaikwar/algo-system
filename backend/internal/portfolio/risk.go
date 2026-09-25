package portfolio

import (
	"encoding/json"
	"log"
	"sync"
)

// RiskLimits defines configurable risk management thresholds.
type RiskLimits struct {
	MaxPositionSize  int64   `json:"max_position_size"`  // max qty per instrument
	MaxDailyLoss     int64   `json:"max_daily_loss"`     // max daily loss in paise
	MaxOpenPositions int     `json:"max_open_positions"` // max number of concurrent positions
	MaxExposure      int64   `json:"max_exposure"`       // max total exposure in paise
	MaxDrawdownPct   float64 `json:"max_drawdown_pct"`   // max drawdown percentage (0-100)
}

// DefaultRiskLimits returns conservative default limits.
func DefaultRiskLimits() RiskLimits {
	return RiskLimits{
		MaxPositionSize:  100,
		MaxDailyLoss:     500000, // ₹5,000
		MaxOpenPositions: 5,
		MaxExposure:      10000000, // ₹1,00,000
		MaxDrawdownPct:   5.0,
	}
}

// RiskManager validates trades against risk limits and tracks equity.
type RiskManager struct {
	mu        sync.RWMutex
	limits    RiskLimits
	portfolio *Portfolio

	dailyPnL   int64
	equity     int64
	peakEquity int64

	// Kill switch
	killSwitch bool

	// Streak tracking
	winStreak         int
	lossStreak        int
	currentStreakType string // "win", "loss", or ""
}

// riskSnapshot is the JSON-serializable shape for Snapshot/Restore.
type riskSnapshot struct {
	DailyPnL          int64  `json:"daily_pnl"`
	Equity            int64  `json:"equity"`
	PeakEquity        int64  `json:"peak_equity"`
	KillSwitch        bool   `json:"kill_switch"`
	WinStreak         int    `json:"win_streak"`
	LossStreak        int    `json:"loss_streak"`
	CurrentStreakType string `json:"current_streak_type"`
}

// NewRiskManager creates a RiskManager with the given limits, portfolio, and starting equity.
func NewRiskManager(limits RiskLimits, pf *Portfolio, initialEquity int64) *RiskManager {
	return &RiskManager{
		limits:     limits,
		portfolio:  pf,
		equity:     initialEquity,
		peakEquity: initialEquity,
	}
}

// CanTrade checks if a new trade would violate any risk limits.
// Returns true if the trade is allowed, false with a reason if not.
func (rm *RiskManager) CanTrade(token, exchange string, qty int64) (bool, string) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Kill switch
	if rm.killSwitch {
		return false, "kill switch is active"
	}

	positions := rm.portfolio.GetPositions()

	// Check max open positions
	key := exchange + ":" + token
	isNew := true
	for _, pos := range positions {
		if pos.Exchange+":"+pos.Token == key {
			isNew = false
			break
		}
	}
	if isNew && len(positions) >= rm.limits.MaxOpenPositions {
		return false, "max open positions reached"
	}

	// Check position size
	if qty > rm.limits.MaxPositionSize || qty < -rm.limits.MaxPositionSize {
		return false, "position size exceeds limit"
	}

	// Check daily loss
	if rm.dailyPnL < -rm.limits.MaxDailyLoss {
		return false, "max daily loss reached"
	}

	// Check drawdown
	if rm.peakEquity > 0 {
		drawdown := float64(rm.peakEquity-rm.equity) / float64(rm.peakEquity) * 100
		if drawdown > rm.limits.MaxDrawdownPct {
			return false, "max drawdown exceeded"
		}
	}

	// Check exposure limit
	currentExposure := rm.portfolio.TotalExposure()
	// Estimate new exposure contribution (rough — uses 0 as price placeholder)
	if currentExposure >= rm.limits.MaxExposure {
		return false, "max exposure reached"
	}

	return true, ""
}

// RecordPnL updates daily P&L, equity tracking, and streaks.
func (rm *RiskManager) RecordPnL(pnl int64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.dailyPnL += pnl
	rm.equity += pnl
	if rm.equity > rm.peakEquity {
		rm.peakEquity = rm.equity
	}

	// Update streaks
	if pnl > 0 {
		if rm.currentStreakType == "win" {
			rm.winStreak++
		} else {
			rm.currentStreakType = "win"
			rm.winStreak = 1
			rm.lossStreak = 0
		}
	} else if pnl < 0 {
		if rm.currentStreakType == "loss" {
			rm.lossStreak++
		} else {
			rm.currentStreakType = "loss"
			rm.lossStreak = 1
			rm.winStreak = 0
		}
	}

	log.Printf("[risk] daily P&L: %d, equity: %d, peak: %d, win-streak: %d, loss-streak: %d",
		rm.dailyPnL, rm.equity, rm.peakEquity, rm.winStreak, rm.lossStreak)
}

// ResetDaily resets the daily P&L counter (call at market open).
func (rm *RiskManager) ResetDaily() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.dailyPnL = 0
}

// IsKillSwitchActive returns whether the kill switch is engaged.
func (rm *RiskManager) IsKillSwitchActive() bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.killSwitch
}

// SetKillSwitch sets the kill switch state.
func (rm *RiskManager) SetKillSwitch(active bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.killSwitch = active
	if active {
		log.Println("[risk] KILL SWITCH ACTIVATED — blocking new entries")
	} else {
		log.Println("[risk] kill switch deactivated — entries allowed")
	}
}

// GetStreaks returns the current win and loss streak.
func (rm *RiskManager) GetStreaks() (winStreak, lossStreak int) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.winStreak, rm.lossStreak
}

// GetStatus returns current risk status.
func (rm *RiskManager) GetStatus() map[string]interface{} {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	drawdown := 0.0
	if rm.peakEquity > 0 {
		drawdown = float64(rm.peakEquity-rm.equity) / float64(rm.peakEquity) * 100
	}

	return map[string]interface{}{
		"daily_pnl":    rm.dailyPnL,
		"equity":       rm.equity,
		"peak_equity":  rm.peakEquity,
		"drawdown_pct": drawdown,
		"kill_switch":  rm.killSwitch,
		"win_streak":   rm.winStreak,
		"loss_streak":  rm.lossStreak,
		"limits":       rm.limits,
	}
}

// Snapshot serializes the risk manager state to JSON.
func (rm *RiskManager) Snapshot() ([]byte, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	snap := riskSnapshot{
		DailyPnL:          rm.dailyPnL,
		Equity:            rm.equity,
		PeakEquity:        rm.peakEquity,
		KillSwitch:        rm.killSwitch,
		WinStreak:         rm.winStreak,
		LossStreak:        rm.lossStreak,
		CurrentStreakType: rm.currentStreakType,
	}
	return json.Marshal(snap)
}

// RestoreRisk restores risk manager state from a JSON snapshot.
// Note: the Portfolio reference and RiskLimits must be set before restoring.
func (rm *RiskManager) RestoreRisk(data []byte) error {
	var snap riskSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.dailyPnL = snap.DailyPnL
	rm.equity = snap.Equity
	rm.peakEquity = snap.PeakEquity
	rm.killSwitch = snap.KillSwitch
	rm.winStreak = snap.WinStreak
	rm.lossStreak = snap.LossStreak
	rm.currentStreakType = snap.CurrentStreakType
	return nil
}
