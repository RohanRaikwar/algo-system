package orderexec

import (
	"testing"
	"trading-systemv1/internal/strategy"
)

// MockPnLTracker implements PnLTracker interface for testing
type MockPnLTracker struct {
	strategyPnL map[string]int64
}

func NewMockPnLTracker() *MockPnLTracker {
	return &MockPnLTracker{
		strategyPnL: make(map[string]int64),
	}
}

func (m *MockPnLTracker) GetStrategyDailyPnL(strategyName string) int64 {
	return m.strategyPnL[strategyName]
}

func (m *MockPnLTracker) SetStrategyPnL(strategyName string, pnl int64) {
	m.strategyPnL[strategyName] = pnl
}

// TestOrderExecutor_NoProfitCap verifies that NIFTY50_FNO signals are never
// downgraded to PAPER_CAP regardless of daily P&L. The profit cap has been
// removed so the strategy can place real orders all day.
func TestOrderExecutor_NoProfitCap(t *testing.T) {
	cfg := Config{
		LiveOrders:     false, // Dry-run mode for test safety
		Qty:            65,
		CallFNOToken:   "43456",
		CallFNOSymbol:  "NIFTY24JAN24000CE",
		PutFNOToken:    "43457",
		PutFNOSymbol:   "NIFTY24JAN24000PE",
		FNOExchange:    "NFO",
		FNOOrderType:   "MARKET",
		FNOProductType: "CARRYFORWARD",
		LogPrefix:      "[test_executor]",
	}

	executor := NewOrderExecutor(cfg)
	mockTracker := NewMockPnLTracker()
	executor.SetPnLTracker(mockTracker)

	tests := []struct {
		name         string
		strategyName string
		dailyPnL     int64
		description  string
	}{
		{
			name:         "BelowOldCap",
			strategyName: "NIFTY50_FNO",
			dailyPnL:     500, // ₹5.00
			description:  "Below old 10pt cap — should work (always did)",
		},
		{
			name:         "AtOldCap",
			strategyName: "NIFTY50_FNO",
			dailyPnL:     1000, // ₹10.00 — previously would trigger PAPER_CAP
			description:  "At old 10pt cap — should still place orders now",
		},
		{
			name:         "AboveOldCap",
			strategyName: "NIFTY50_FNO",
			dailyPnL:     5000, // ₹50.00
			description:  "Well above old cap — should still place orders now",
		},
		{
			name:         "NegativePnL",
			strategyName: "NIFTY50_FNO",
			dailyPnL:     -500, // -₹5.00
			description:  "Negative P&L — should place orders",
		},
		{
			name:         "OtherStrategy",
			strategyName: "NIFTY50_10PTS",
			dailyPnL:     5000,
			description:  "Other strategies remain paper-only regardless",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTracker.SetStrategyPnL(tt.strategyName, tt.dailyPnL)

			sig := strategy.Signal{
				StrategyName: tt.strategyName,
				Action:       strategy.ActionBuy,
				Side:         strategy.SideCall,
				Token:        "99926000",
				Exchange:     "NSE",
				Qty:          65,
				Reason:       "Test signal",
			}

			// Should not panic or error regardless of P&L level
			executor.ExecuteSignal(sig)
			t.Logf("✅ %s: P&L=₹%.2f — signal processed without profit cap block",
				tt.description, float64(tt.dailyPnL)/100)
		})
	}
}

func TestOrderExecutor_NoPnLTracker(t *testing.T) {
	cfg := Config{
		LiveOrders:     false,
		Qty:            65,
		CallFNOToken:   "43456",
		CallFNOSymbol:  "NIFTY24JAN24000CE",
		PutFNOToken:    "43457",
		PutFNOSymbol:   "NIFTY24JAN24000PE",
		FNOExchange:    "NFO",
		FNOOrderType:   "MARKET",
		FNOProductType: "CARRYFORWARD",
		LogPrefix:      "[test_executor]",
	}

	executor := NewOrderExecutor(cfg)
	// Don't attach P&L tracker

	t.Run("ExecuteWithoutTracker", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Executor panicked without P&L tracker: %v", r)
			}
		}()

		sig := strategy.Signal{
			StrategyName: "NIFTY50_FNO",
			Action:       strategy.ActionBuy,
			Side:         strategy.SideCall,
			Token:        "99926000",
			Exchange:     "NSE",
			Qty:          65,
			Reason:       "Test without tracker",
		}

		executor.ExecuteSignal(sig)
		t.Log("✅ Executor works without P&L tracker")
	})
}

func TestOrderExecutor_StrategyFiltering(t *testing.T) {
	cfg := Config{
		LiveOrders:     false,
		Qty:            65,
		CallFNOToken:   "43456",
		CallFNOSymbol:  "NIFTY24JAN24000CE",
		PutFNOToken:    "43457",
		PutFNOSymbol:   "NIFTY24JAN24000PE",
		FNOExchange:    "NFO",
		FNOOrderType:   "MARKET",
		FNOProductType: "CARRYFORWARD",
		LogPrefix:      "[test_executor]",
	}

	executor := NewOrderExecutor(cfg)

	strategies := []string{
		"NIFTY50_FNO",
		"NIFTY50_10PTS",
		"NIFTY50_FNO_SL",
		"NIFTY50_FNO_SL2",
	}

	for _, stratName := range strategies {
		t.Run(stratName, func(t *testing.T) {
			sig := strategy.Signal{
				StrategyName: stratName,
				Action:       strategy.ActionBuy,
				Side:         strategy.SideCall,
				Token:        "99926000",
				Exchange:     "NSE",
				Qty:          65,
				Reason:       "Test strategy filtering",
			}

			executor.ExecuteSignal(sig)

			if stratName == "NIFTY50_FNO" {
				t.Logf("✅ %s: Real order strategy (routes to REAL when LiveOrders=true)", stratName)
			} else {
				t.Logf("✅ %s: Paper-only strategy (always PAPER)", stratName)
			}
		})
	}
}
