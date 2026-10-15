package main

import (
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/portfolio"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("[test] Testing Profit Cap Calculation")

	// Create P&L tracker
	pnlTracker := portfolio.NewPnLTracker()

	// Test scenario: 65 qty trades with 10 point profit target
	const qty = 65
	const profitCapPaise int64 = 65000 // 65 qty × 10 points × 100 paise = ₹650.00

	fmt.Printf("🎯 Profit Cap: %d paise (₹%.2f)\n", profitCapPaise, float64(profitCapPaise)/100)
	fmt.Printf("📊 Quantity: %d\n", qty)
	fmt.Printf("📈 Target: 10 points per unit\n\n")

	// Scenario 1: First trade - 5 points profit (should continue live trading)
	fmt.Println("=== Trade 1: 5 Points Profit ===")
	entryPrice1 := int64(10000) // ₹100.00
	exitPrice1 := int64(10500)  // ₹105.00
	
	// Record BUY
	pnl1Buy := pnlTracker.RecordTrade(portfolio.Trade{
		StrategyName: "NIFTY50_FNO",
		Token:        "12345",
		Exchange:     "NFO",
		Action:       "BUY",
		Qty:          qty,
		Price:        entryPrice1,
		Timestamp:    time.Now(),
	})
	fmt.Printf("BUY: %d qty @ ₹%.2f → P&L: %d paise\n", qty, float64(entryPrice1)/100, pnl1Buy)

	// Record SELL
	pnl1Sell := pnlTracker.RecordTrade(portfolio.Trade{
		StrategyName: "NIFTY50_FNO",
		Token:        "12345",
		Exchange:     "NFO",
		Action:       "SELL",
		Qty:          qty,
		Price:        exitPrice1,
		Timestamp:    time.Now(),
	})
	fmt.Printf("SELL: %d qty @ ₹%.2f → P&L: %d paise (₹%.2f)\n", qty, float64(exitPrice1)/100, pnl1Sell, float64(pnl1Sell)/100)

	dailyPnL1 := pnlTracker.GetStrategyDailyPnL("NIFTY50_FNO")
	fmt.Printf("Daily P&L: %d paise (₹%.2f)\n", dailyPnL1, float64(dailyPnL1)/100)
	
	if dailyPnL1 >= profitCapPaise {
		fmt.Printf("❌ PROFIT CAP HIT - Switch to PAPER mode\n")
	} else {
		fmt.Printf("✅ CONTINUE LIVE TRADING (₹%.2f remaining)\n", float64(profitCapPaise-dailyPnL1)/100)
	}
	fmt.Println()

	// Scenario 2: Second trade - another 5 points profit (should hit cap)
	fmt.Println("=== Trade 2: 5 Points Profit (Should Hit Cap) ===")
	entryPrice2 := int64(11000) // ₹110.00
	exitPrice2 := int64(11500)  // ₹115.00
	
	// Record BUY
	pnl2Buy := pnlTracker.RecordTrade(portfolio.Trade{
		StrategyName: "NIFTY50_FNO",
		Token:        "12346",
		Exchange:     "NFO",
		Action:       "BUY",
		Qty:          qty,
		Price:        entryPrice2,
		Timestamp:    time.Now(),
	})
	fmt.Printf("BUY: %d qty @ ₹%.2f → P&L: %d paise\n", qty, float64(entryPrice2)/100, pnl2Buy)

	// Record SELL
	pnl2Sell := pnlTracker.RecordTrade(portfolio.Trade{
		StrategyName: "NIFTY50_FNO",
		Token:        "12346",
		Exchange:     "NFO",
		Action:       "SELL",
		Qty:          qty,
		Price:        exitPrice2,
		Timestamp:    time.Now(),
	})
	fmt.Printf("SELL: %d qty @ ₹%.2f → P&L: %d paise (₹%.2f)\n", qty, float64(exitPrice2)/100, pnl2Sell, float64(pnl2Sell)/100)

	dailyPnL2 := pnlTracker.GetStrategyDailyPnL("NIFTY50_FNO")
	fmt.Printf("Daily P&L: %d paise (₹%.2f)\n", dailyPnL2, float64(dailyPnL2)/100)
	
	if dailyPnL2 >= profitCapPaise {
		fmt.Printf("🔴 PROFIT CAP HIT - Switch to PAPER mode\n")
	} else {
		fmt.Printf("✅ CONTINUE LIVE TRADING (₹%.2f remaining)\n", float64(profitCapPaise-dailyPnL2)/100)
	}
	fmt.Println()

	// Scenario 3: Third trade - should be in paper mode
	fmt.Println("=== Trade 3: After Cap Hit (Paper Mode) ===")
	entryPrice3 := int64(12000) // ₹120.00
	exitPrice3 := int64(12500)  // ₹125.00
	
	// Check if we should be in paper mode BEFORE recording
	dailyPnLBefore := pnlTracker.GetStrategyDailyPnL("NIFTY50_FNO")
	paperMode := dailyPnLBefore >= profitCapPaise
	
	fmt.Printf("Before Trade 3 - Daily P&L: ₹%.2f, Paper Mode: %v\n", float64(dailyPnLBefore)/100, paperMode)
	
	if paperMode {
		fmt.Printf("📝 PAPER TRADE: BUY %d qty @ ₹%.2f (not sent to Angel One)\n", qty, float64(entryPrice3)/100)
		fmt.Printf("📝 PAPER TRADE: SELL %d qty @ ₹%.2f (not sent to Angel One)\n", qty, float64(exitPrice3)/100)
		fmt.Printf("📝 PAPER PROFIT: ₹%.2f (simulated only)\n", float64((exitPrice3-entryPrice3)*qty)/100)
	} else {
		fmt.Printf("💰 LIVE TRADE: Would continue with real orders\n")
	}

	// Summary
	fmt.Println("\n=== SUMMARY ===")
	fmt.Printf("Total Real Profit: ₹%.2f\n", float64(dailyPnL2)/100)
	fmt.Printf("Profit Cap: ₹%.2f\n", float64(profitCapPaise)/100)
	fmt.Printf("Cap Status: %s\n", map[bool]string{true: "HIT ✅", false: "NOT HIT ❌"}[dailyPnL2 >= profitCapPaise])
	
	// Verify calculation
	expectedProfit := int64((exitPrice1-entryPrice1)*qty + (exitPrice2-entryPrice2)*qty)
	fmt.Printf("Expected Profit: %d paise (₹%.2f)\n", expectedProfit, float64(expectedProfit)/100)
	fmt.Printf("Actual Profit: %d paise (₹%.2f)\n", dailyPnL2, float64(dailyPnL2)/100)
	
	if expectedProfit == dailyPnL2 {
		fmt.Println("✅ Calculation CORRECT")
	} else {
		fmt.Println("❌ Calculation ERROR")
	}

	// Test edge case: exactly at cap
	fmt.Println("\n=== EDGE CASE TEST ===")
	fmt.Printf("Profit Cap: %d paise\n", profitCapPaise)
	fmt.Printf("Current P&L: %d paise\n", dailyPnL2)
	fmt.Printf("Difference: %d paise\n", dailyPnL2-profitCapPaise)
	
	if dailyPnL2 == profitCapPaise {
		fmt.Println("🎯 EXACTLY at profit cap")
	} else if dailyPnL2 > profitCapPaise {
		fmt.Println("📈 EXCEEDED profit cap")
	} else {
		fmt.Println("📉 BELOW profit cap")
	}
}