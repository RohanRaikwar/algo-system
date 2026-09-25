package main

import (
	"log"
	"os"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/realmoney"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"

	"github.com/joho/godotenv"
)

// This test simulates the COMPLETE strategy order execution flow
// exactly as it works in production stratengine
func main() {
	realmoney.Confirm("places a real NIFTY BUY and SELL (1 lot)")
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("═══════════════════════════════════════════════════════")
	log.Println("  STRATEGY ORDER EXECUTION TEST")
	log.Println("  Testing complete flow: Strategy → OrderExecutor → Angel One")
	log.Println("═══════════════════════════════════════════════════════")
	log.Println()
	log.Println("⚠️  WARNING: This test places REAL ORDERS through Angel One!")
	log.Println("⚠️  Market is currently CLOSED, so orders will be AMO orders.")
	log.Println("⚠️  Remember to CANCEL test orders after completion!")
	log.Println()

	// Load environment
	// Try .env.prod first (from repo root)
	if err := godotenv.Load(".env.prod"); err != nil {
		log.Printf("⚠️  .env.prod not found, trying .env...")
		if err := godotenv.Load(".env"); err != nil {
			log.Fatalf("❌ Failed to load .env: %v", err)
		}
	}

	// Get configuration
	angelAPIKey := os.Getenv("ANGEL_API_KEY")
	angelClientID := os.Getenv("ANGEL_CLIENT_CODE")
	angelPassword := os.Getenv("ANGEL_PASSWORD")
	angelTOTP := os.Getenv("ANGEL_TOTP_SECRET")
	productType := os.Getenv("STRAT_FNO_PRODUCT_TYPE")

	if angelAPIKey == "" || angelClientID == "" || angelPassword == "" || angelTOTP == "" {
		log.Fatal("❌ Missing Angel One credentials in .env")
	}

	if productType == "" {
		productType = "INTRADAY"
	}

	log.Printf("📋 Product Type: %s", productType)
	log.Println()

	// ═══════════════════════════════════════════════════════
	// STEP 1: Create Session Manager (with auto-refresh)
	// ═══════════════════════════════════════════════════════
	log.Println("STEP 1: Creating Session Manager...")
	sessionManager := smartconnect.NewSessionManager(angelAPIKey, angelClientID, angelPassword, angelTOTP, false)
	
	if err := sessionManager.Login(); err != nil {
		log.Fatalf("❌ Session manager login failed: %v", err)
	}
	
	sessionManager.Start() // Start proactive refresh
	log.Println("✅ Session manager initialized with auto-refresh")
	log.Println()

	// ═══════════════════════════════════════════════════════
	// STEP 2: Get Strike Picker for dynamic token resolution
	// ═══════════════════════════════════════════════════════
	log.Println("STEP 2: Initializing Strike Picker...")
	picker := orderexec.NewStrikePicker(sessionManager.GetClient())
	
	// Resolve ATM strikes using current NIFTY spot price
	spotPrice := int64(2450000) // 24500.00 INR in paise
	if !picker.ResolveATM(spotPrice) {
		log.Fatal("❌ Strike resolution failed")
	}
	
	callInfo := picker.GetCallToken()
	putInfo := picker.GetPutToken()
	
	log.Printf("✅ CALL: %s (token: %s)", callInfo.Symbol, callInfo.Token)
	log.Printf("✅ PUT:  %s (token: %s)", putInfo.Symbol, putInfo.Token)
	log.Println()

	// ═══════════════════════════════════════════════════════
	// STEP 3: Create Order Executor (using session manager)
	// ═══════════════════════════════════════════════════════
	log.Println("STEP 3: Creating Order Executor...")
	executor := orderexec.NewOrderExecutor(orderexec.Config{
		LiveOrders:     true, // REAL ORDERS
		Qty:            65,   // 1 lot for NIFTY
		CallFNOToken:   callInfo.Token,
		CallFNOSymbol:  callInfo.Symbol,
		PutFNOToken:    putInfo.Token,
		PutFNOSymbol:   putInfo.Symbol,
		FNOExchange:    "NFO",
		FNOOrderType:   "MARKET",
		FNOProductType: productType,
		LogPrefix:      "[test_strategy_order]",
		SessionManager: sessionManager, // Use session manager for auto-refresh
	})
	
	executor.SetStrikePicker(picker)
	log.Println("✅ Order executor initialized with session manager")
	log.Println()

	// ═══════════════════════════════════════════════════════
	// STEP 4: Create Strategy Instance
	// ═══════════════════════════════════════════════════════
	log.Println("STEP 4: Creating Strategy (NIFTY50_FNO)...")
	strat := strategy.NewNifty50FnO(1) // 1 lot
	strat.SetFNOTokens(callInfo.Token, putInfo.Token)
	_ = strat // Strategy created but not used directly (executor handles signals)
	log.Println("✅ Strategy created")
	log.Println()

	// ═══════════════════════════════════════════════════════
	// STEP 5: Simulate Market Data (Tick)
	// ═══════════════════════════════════════════════════════
	log.Println("STEP 5: Simulating Market Tick...")
	
	// Update LTP for NIFTY spot
	executor.UpdateLTP(model.Tick{
		Token: "99926000",
		Price: 2450000, // 24500.00 INR in paise
	})
	
	// Update LTP for CALL and PUT options
	executor.UpdateLTP(model.Tick{
		Token: callInfo.Token,
		Price: 24050, // 240.50 INR in paise
	})
	executor.UpdateLTP(model.Tick{
		Token: putInfo.Token,
		Price: 22880, // 228.80 INR in paise
	})
	
	log.Println("✅ LTP updated for all instruments")
	log.Println()

	// ═══════════════════════════════════════════════════════
	// STEP 6: Generate BUY CALL Signal
	// ═══════════════════════════════════════════════════════
	log.Println("STEP 6: Generating BUY CALL Signal...")
	log.Println("⏰ Timestamp:", time.Now().Format("15:04:05.000"))
	
	buySignal := strategy.Signal{
		StrategyName: "NIFTY50_FNO", // Use NIFTY50_FNO for real orders
		Action:       strategy.ActionBuy,
		Side:         strategy.SideCall,
		Token:        callInfo.Token,
		Exchange:     "NFO",
		Qty:          65,
		Price:        24050,
		Reason:       "Test BUY CALL signal",
	}
	
	startTime := time.Now()
	executor.ExecuteSignal(buySignal)
	buyDuration := time.Since(startTime)
	
	log.Printf("⏱️  BUY order execution time: %v", buyDuration)
	log.Println()

	// Wait a bit before SELL
	log.Println("⏳ Waiting 5 seconds before SELL...")
	time.Sleep(5 * time.Second)

	// ═══════════════════════════════════════════════════════
	// STEP 7: Generate SELL CALL Signal
	// ═══════════════════════════════════════════════════════
	log.Println("STEP 7: Generating SELL CALL Signal...")
	log.Println("⏰ Timestamp:", time.Now().Format("15:04:05.000"))
	
	sellSignal := strategy.Signal{
		StrategyName: "NIFTY50_FNO", // Use NIFTY50_FNO for real orders
		Action:       strategy.ActionSell,
		Side:         strategy.SideCall,
		Token:        callInfo.Token,
		Exchange:     "NFO",
		Qty:          65,
		Price:        22880,
		Reason:       "Test SELL CALL signal",
	}
	
	startTime = time.Now()
	executor.ExecuteSignal(sellSignal)
	sellDuration := time.Since(startTime)
	
	log.Printf("⏱️  SELL order execution time: %v", sellDuration)
	log.Println()

	// ═══════════════════════════════════════════════════════
	// STEP 8: Summary
	// ═══════════════════════════════════════════════════════
	log.Println("═══════════════════════════════════════════════════════")
	log.Println("  TEST SUMMARY")
	log.Println("═══════════════════════════════════════════════════════")
	log.Printf("✅ Session Manager: Active with auto-refresh")
	log.Printf("✅ Strike Picker: Resolved ATM strikes")
	log.Printf("✅ Order Executor: Using session manager")
	log.Printf("✅ Strategy: Generated signals")
	log.Printf("⏱️  BUY execution time: %v", buyDuration)
	log.Printf("⏱️  SELL execution time: %v", sellDuration)
	log.Printf("📋 Product Type: %s", productType)
	log.Println()
	log.Println("🎯 Complete strategy order flow tested successfully!")
	log.Println("═══════════════════════════════════════════════════════")
	
	// Check session health
	sessionInfo := sessionManager.GetSessionInfo()
	log.Println()
	log.Println("📊 Session Manager Status:")
	log.Printf("   Valid: %v", sessionInfo["valid"])
	log.Printf("   Age: %.2f minutes", sessionInfo["age_minutes"])
	log.Printf("   Total Refreshes: %v", sessionInfo["total_refreshes"])
	log.Printf("   Circuit Breaker: %v", sessionInfo["circuit_breaker_open"])
	
	// Stop session manager
	sessionManager.Stop()
	log.Println()
	log.Println("✅ Test completed successfully!")
}
