package portfolio

import (
	"testing"
	"time"
)

func TestPnLTracker_StrategyDailyPnL(t *testing.T) {
	tracker := NewPnLTracker()

	// Test 1: Initial state - should be zero
	t.Run("InitialState", func(t *testing.T) {
		pnl := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		if pnl != 0 {
			t.Errorf("Expected initial P&L to be 0, got %d", pnl)
		}
	})

	// Test 2: Record BUY trade - should not affect daily P&L yet
	t.Run("BuyTrade", func(t *testing.T) {
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "BUY",
			Qty:          65,
			Price:        15000, // ₹150.00
			Timestamp:    time.Now(),
		})

		pnl := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		if pnl != 0 {
			t.Errorf("Expected P&L after BUY to be 0, got %d", pnl)
		}
	})

	// Test 3: Record SELL trade - should update daily P&L
	t.Run("SellTradeProfit", func(t *testing.T) {
		realizedPnL := tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "SELL",
			Qty:          65,
			Price:        15600, // ₹156.00
			Timestamp:    time.Now(),
		})

		// Expected: (15600 - 15000) * 65 = 39000 paise = ₹390
		expectedPnL := int64(39000)
		if realizedPnL != expectedPnL {
			t.Errorf("Expected realized P&L %d, got %d", expectedPnL, realizedPnL)
		}

		dailyPnL := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		if dailyPnL != expectedPnL {
			t.Errorf("Expected daily P&L %d, got %d", expectedPnL, dailyPnL)
		}
	})

	// Test 4: Multiple trades - should accumulate
	t.Run("MultipleTrades", func(t *testing.T) {
		// Second BUY
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "BUY",
			Qty:          65,
			Price:        14000, // ₹140.00
			Timestamp:    time.Now(),
		})

		// Second SELL
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "SELL",
			Qty:          65,
			Price:        14600, // ₹146.00
			Timestamp:    time.Now(),
		})

		// Expected: 39000 (first trade) + (14600-14000)*65 = 39000 + 39000 = 78000 paise = ₹780
		expectedTotal := int64(78000)
		dailyPnL := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		if dailyPnL != expectedTotal {
			t.Errorf("Expected accumulated P&L %d, got %d", expectedTotal, dailyPnL)
		}
	})

	// Test 5: Different strategy - should not affect NIFTY50_FNO
	t.Run("DifferentStrategy", func(t *testing.T) {
		beforePnL := tracker.GetStrategyDailyPnL("NIFTY50_FNO")

		// Trade for different strategy
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_10PTS",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "BUY",
			Qty:          65,
			Price:        15000,
			Timestamp:    time.Now(),
		})

		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_10PTS",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "SELL",
			Qty:          65,
			Price:        16000,
			Timestamp:    time.Now(),
		})

		afterPnL := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		if afterPnL != beforePnL {
			t.Errorf("NIFTY50_FNO P&L should not change, before=%d after=%d", beforePnL, afterPnL)
		}

		// Check NIFTY50_10PTS has its own P&L
		otherPnL := tracker.GetStrategyDailyPnL("NIFTY50_10PTS")
		expectedOther := int64(65000) // (16000-15000)*65
		if otherPnL != expectedOther {
			t.Errorf("Expected NIFTY50_10PTS P&L %d, got %d", expectedOther, otherPnL)
		}
	})

	// Test 6: Loss trade - should reduce daily P&L
	t.Run("LossTrade", func(t *testing.T) {
		beforePnL := tracker.GetStrategyDailyPnL("NIFTY50_FNO")

		// BUY high
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "BUY",
			Qty:          65,
			Price:        16000,
			Timestamp:    time.Now(),
		})

		// SELL low (loss)
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "SELL",
			Qty:          65,
			Price:        15500,
			Timestamp:    time.Now(),
		})

		afterPnL := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		expectedLoss := int64(-32500) // (15500-16000)*65
		expectedTotal := beforePnL + expectedLoss

		if afterPnL != expectedTotal {
			t.Errorf("Expected P&L after loss %d, got %d", expectedTotal, afterPnL)
		}
	})

	// Test 7: ResetDaily - should clear strategy P&L
	t.Run("ResetDaily", func(t *testing.T) {
		tracker.ResetDaily()

		pnl := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		if pnl != 0 {
			t.Errorf("Expected P&L after reset to be 0, got %d", pnl)
		}

		otherPnL := tracker.GetStrategyDailyPnL("NIFTY50_10PTS")
		if otherPnL != 0 {
			t.Errorf("Expected NIFTY50_10PTS P&L after reset to be 0, got %d", otherPnL)
		}
	})

	// Test 8: Trade without strategy name - should not crash
	t.Run("TradeWithoutStrategyName", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("RecordTrade panicked with empty strategy name: %v", r)
			}
		}()

		tracker.RecordTrade(Trade{
			StrategyName: "", // Empty strategy name
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "BUY",
			Qty:          65,
			Price:        15000,
			Timestamp:    time.Now(),
		})

		tracker.RecordTrade(Trade{
			StrategyName: "", // Empty strategy name
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "SELL",
			Qty:          65,
			Price:        15500,
			Timestamp:    time.Now(),
		})

		// Should not affect any strategy P&L
		pnl := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		if pnl != 0 {
			t.Errorf("Empty strategy name should not affect NIFTY50_FNO, got %d", pnl)
		}
	})
}

func TestPnLTracker_ProfitCapScenario(t *testing.T) {
	tracker := NewPnLTracker()

	// Simulate a trading day reaching profit cap
	t.Run("ReachProfitCap", func(t *testing.T) {
		// Trade 1: +₹5.00 profit
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "BUY",
			Qty:          65,
			Price:        15000,
			Timestamp:    time.Now(),
		})
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "SELL",
			Qty:          65,
			Price:        15500,
			Timestamp:    time.Now(),
		})

		pnl1 := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		expectedPnl1 := int64(32500) // (15500-15000)*65 = ₹325
		if pnl1 != expectedPnl1 {
			t.Errorf("After trade 1: expected %d, got %d", expectedPnl1, pnl1)
		}

		// Trade 2: +₹6.00 profit (total should be ₹11.00, exceeding ₹10 cap)
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "BUY",
			Qty:          65,
			Price:        14000,
			Timestamp:    time.Now(),
		})
		tracker.RecordTrade(Trade{
			StrategyName: "NIFTY50_FNO",
			Token:        "99926000",
			Exchange:     "NSE",
			Action:       "SELL",
			Qty:          65,
			Price:        14600,
			Timestamp:    time.Now(),
		})

		pnl2 := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		expectedPnl2 := int64(71500) // 32500 + (14600-14000)*65 = ₹715
		if pnl2 != expectedPnl2 {
			t.Errorf("After trade 2: expected %d, got %d", expectedPnl2, pnl2)
		}

		// Check if profit cap would be hit (₹10.00 = 1000 paise)
		profitCapPaise := int64(1000)
		if pnl2 < profitCapPaise {
			t.Errorf("Expected to exceed profit cap of %d, but got %d", profitCapPaise, pnl2)
		}

		t.Logf("✅ Profit cap would be triggered: ₹%.2f >= ₹10.00", float64(pnl2)/100)
	})
}

func TestPnLTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewPnLTracker()

	// Test concurrent reads and writes
	t.Run("ConcurrentAccess", func(t *testing.T) {
		done := make(chan bool)

		// Writer goroutine
		go func() {
			for i := 0; i < 100; i++ {
				tracker.RecordTrade(Trade{
					StrategyName: "NIFTY50_FNO",
					Token:        "99926000",
					Exchange:     "NSE",
					Action:       "BUY",
					Qty:          65,
					Price:        15000,
					Timestamp:    time.Now(),
				})
				tracker.RecordTrade(Trade{
					StrategyName: "NIFTY50_FNO",
					Token:        "99926000",
					Exchange:     "NSE",
					Action:       "SELL",
					Qty:          65,
					Price:        15100,
					Timestamp:    time.Now(),
				})
			}
			done <- true
		}()

		// Reader goroutine
		go func() {
			for i := 0; i < 100; i++ {
				_ = tracker.GetStrategyDailyPnL("NIFTY50_FNO")
			}
			done <- true
		}()

		// Wait for both goroutines
		<-done
		<-done

		// Verify final P&L
		finalPnL := tracker.GetStrategyDailyPnL("NIFTY50_FNO")
		expectedPnL := int64(650000) // (15100-15000)*65*100 = ₹6500
		if finalPnL != expectedPnL {
			t.Errorf("Expected final P&L %d, got %d", expectedPnL, finalPnL)
		}
	})
}
