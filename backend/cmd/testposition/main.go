package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/realmoney"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"

	"github.com/joho/godotenv"
	"github.com/pquerna/otp/totp"
)

// TestPosition places a BUY order with 1 quantity and immediately sells it
// to test order flow with minimal risk.
//
// Usage:
//   go run backend/cmd/testposition/main.go --i-understand-real-money CALL
//   go run backend/cmd/testposition/main.go --i-understand-real-money PUT
//
// It asks for a typed confirmation before logging in.
//
// Features:
//   - Uses 1 quantity only (minimal risk)
//   - Places REAL orders to test broker connectivity
//   - Immediately sells after buying (no waiting)
//   - Tests complete order cycle
//
// Environment variables required:
//   ANGEL_API_KEY, ANGEL_CLIENT_ID, ANGEL_PASSWORD, ANGEL_TOTP

func main() {
	args := realmoney.Confirm("places a real BUY and then a real SELL")

	// Load environment variables
	// Priority: .env.prod > .env (check both backend and root directories)
	envFile := ""
	for _, path := range []string{".env.prod", "../.env.prod", ".env", "../.env"} {
		if _, err := os.Stat(path); err == nil {
			envFile = path
			break
		}
	}
	
	if envFile == "" {
		log.Fatal("No .env or .env.prod file found")
	}
	
	if err := godotenv.Load(envFile); err != nil {
		log.Fatalf("Failed to load %s: %v", envFile, err)
	}
	log.Printf("📁 Using environment file: %s", envFile)

	// Parse command line arguments
	if len(args) < 1 {
		log.Fatal("Usage: go run backend/cmd/testposition/main.go " + realmoney.Flag + " [CALL|PUT] [--test-mode]")
	}

	side := args[0]
	if side != "CALL" && side != "PUT" {
		log.Fatal("Side must be CALL or PUT")
	}
	
	// Check for test mode flag
	testMode := false
	if len(args) > 1 && args[1] == "--test-mode" {
		testMode = true
		log.Println("\n🧪 TEST MODE ENABLED")
		log.Println("   Using hardcoded NIFTY price and strike for testing")
		log.Println("   This will place REAL orders with 1 qty")
	}

		log.Println("\n⚠️  IMPORTANT SAFETY NOTICE:")
		log.Println("   • This tool creates a NEW Angel One session")
		log.Println("   • If your WebSocket is running, you'll have 2 sessions")
		log.Println("   • Angel One allows multiple sessions, but be aware")
		log.Println("   • The test uses 1 lot (65 units) - this is REAL money!")
		log.Println("")
		log.Printf("   Press Ctrl+C to cancel, or wait 5 seconds to continue...")
	time.Sleep(5 * time.Second)

	// Get configuration from environment
	apiKey := os.Getenv("ANGEL_API_KEY")
	clientID := os.Getenv("ANGEL_CLIENT_CODE") // Use ANGEL_CLIENT_CODE from .env.prod
	if clientID == "" {
		clientID = os.Getenv("ANGEL_CLIENT_ID") // Fallback to ANGEL_CLIENT_ID
	}
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET") // Use ANGEL_TOTP_SECRET from .env.prod
	if totpSecret == "" {
		totpSecret = os.Getenv("ANGEL_TOTP") // Fallback to ANGEL_TOTP
	}

	if apiKey == "" || clientID == "" || password == "" || totpSecret == "" {
		log.Fatal("Missing Angel One credentials in environment variables")
	}

	// Get NIFTY index token (default: 99926000)
	niftyToken := os.Getenv("NIFTY_INDEX_TOKEN")
	if niftyToken == "" {
		niftyToken = "99926000" // NIFTY 50 index
	}

	// Use 1 lot (65 units) for testing - Angel One requires lot size multiples
	testQty := int64(65) // 1 lot = 65 units for NIFTY options

	log.Println("╔════════════════════════════════════════════════════════╗")
	log.Println("║    Test Position: BUY 1 lot → Immediate SELL          ║")
	log.Println("╚════════════════════════════════════════════════════════╝")
	log.Printf("Side: %s", side)
	log.Printf("Quantity: %d units (1 lot)", testQty)

	// Create SmartConnect client
	sc := smartconnect.NewSmartConnect(smartconnect.Config{
		APIKey: apiKey,
	})

	// Generate TOTP
	totpCode, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		log.Fatalf("TOTP generation failed: %v", err)
	}

	// Login
	log.Println("\n🔐 Logging in to Angel One...")
	session, err := sc.GenerateSession(clientID, password, totpCode)
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}
	log.Printf("✅ Login successful: %s", session["data"].(map[string]interface{})["clientcode"])

	// Step 1: Get current NIFTY index price
	log.Println("\n📊 Fetching NIFTY index price...")
	
	var niftyPrice int64
	
	if testMode {
		// Use hardcoded NIFTY price for testing
		niftyPrice = 2336700 // ₹23,367.00
		log.Printf("🧪 TEST MODE: Using hardcoded NIFTY Index: ₹%.2f", float64(niftyPrice)/100)
	} else {
		var err error
		niftyPrice, err = getNiftyIndexPrice(sc, niftyToken)
		if err != nil {
			log.Printf("⚠️  Failed to get NIFTY index: %v", err)
			log.Println("\n💡 Possible reasons:")
			log.Println("   • Market is closed (trading hours: 9:15 AM - 3:30 PM IST)")
			log.Println("   • Angel One API rate limit")
			log.Println("   • Network connectivity issue")
			log.Println("\n� TIP: Use --test-mode flag to test with hardcoded values:")
			log.Println("   go run cmd/testposition/main.go CALL --test-mode")
			log.Fatal("Cannot proceed without NIFTY index price")
		}
		log.Printf("NIFTY Index: ₹%.2f", float64(niftyPrice)/100)
	}

	// Step 2: Resolve ATM strike using StrikePicker
	log.Println("\n🎯 Resolving ATM strike...")
	picker := orderexec.NewStrikePicker(sc)
	if !picker.ResolveATM(niftyPrice) {
		log.Fatal("Failed to resolve ATM strike")
	}

	// Get the appropriate strike based on side
	var strikeInfo orderexec.StrikeInfo
	var strategySide strategy.PositionSide

	if side == "CALL" {
		strikeInfo = picker.GetCallToken()
		strategySide = strategy.SideCall
	} else {
		strikeInfo = picker.GetPutToken()
		strategySide = strategy.SidePut
	}

	token := strikeInfo.Token
	symbol := strikeInfo.Symbol

	log.Printf("\n📊 Trading: %s (token: %s, strike: %d)", symbol, token, strikeInfo.Strike)

	// Create order executor with 1 quantity
	callInfo := picker.GetCallToken()
	putInfo := picker.GetPutToken()
	
	executor := orderexec.NewOrderExecutor(orderexec.Config{
		LiveOrders:     true, // REAL ORDERS
		Qty:            testQty, // 1 quantity only
		CallFNOToken:   callInfo.Token,
		CallFNOSymbol:  callInfo.Symbol,
		PutFNOToken:    putInfo.Token,
		PutFNOSymbol:   putInfo.Symbol,
		FNOExchange:    "NFO",
		FNOOrderType:   "MARKET",
		FNOProductType: "INTRADAY",
		AngelAPIKey:    apiKey,
		AngelClientID:  clientID,
		AngelPassword:  password,
		AngelTOTP:      totpSecret,
		LogPrefix:      "[test_position]",
	})

	// Get current LTP
	log.Println("\n📈 Fetching current LTP...")
	ltp, err := getLTP(sc, token, "NFO")
	if err != nil {
		log.Fatalf("Failed to get LTP: %v", err)
	}
	log.Printf("Current LTP: ₹%.2f", float64(ltp)/100)

	// Update executor LTP
	executor.UpdateLTP(model.Tick{
		Token:    token,
		Exchange: "NFO",
		Price:    ltp,
		TickTS:   time.Now(),
	})

	// Step 1: Place BUY order
	log.Println("\n🟢 Step 1: Placing BUY order (1 lot = 65 units)...")
	log.Printf("⚠️  This will place a REAL order with REAL money!")
	log.Printf("⚠️  Risk: ~₹%.2f (65 units × entry price)", float64(ltp*65)/100)
	
	buyStartTime := time.Now()
	log.Printf("⏱️  BUY order start time: %s", buyStartTime.Format("15:04:05.000"))
	
	buySignal := strategy.Signal{
		StrategyName: "NIFTY50_FNO", // Use NIFTY50_FNO to enable real orders
		Action:       strategy.ActionBuy,
		Side:         strategySide,
		Token:        token,
		Exchange:     "NFO",
		Qty:          testQty,
		Price:        ltp,
		Reason:       fmt.Sprintf("Test %s BUY order (1 qty)", side),
	}

	executor.ExecuteSignal(buySignal)
	buyEndTime := time.Now()
	buyDuration := buyEndTime.Sub(buyStartTime)
	
	log.Printf("✅ BUY order placed")
	log.Printf("⏱️  BUY order end time: %s", buyEndTime.Format("15:04:05.000"))
	log.Printf("⏱️  BUY order execution time: %v", buyDuration)
	
	entryPrice := ltp
	log.Printf("\n📊 Position opened:")
	log.Printf("   Entry Price: ₹%.2f", float64(entryPrice)/100)

	// Wait for order to execute
	log.Println("\n⏳ Waiting 5 seconds for order execution...")
	time.Sleep(5 * time.Second)

	// Step 2: Get current LTP for exit
	log.Println("\n📈 Fetching current LTP for exit...")
	exitLTP, err := getLTP(sc, token, "NFO")
	if err != nil {
		log.Printf("⚠️  Failed to get exit LTP: %v (using entry price)", err)
		exitLTP = entryPrice
	}
	log.Printf("Current LTP: ₹%.2f", float64(exitLTP)/100)

	executor.UpdateLTP(model.Tick{
		Token:    token,
		Exchange: "NFO",
		Price:    exitLTP,
		TickTS:   time.Now(),
	})

	// Step 3: Place SELL order immediately
	log.Println("\n🔴 Step 3: Placing SELL order...")
	
	sellStartTime := time.Now()
	log.Printf("⏱️  SELL order start time: %s", sellStartTime.Format("15:04:05.000"))
	
	sellSignal := strategy.Signal{
		StrategyName: "NIFTY50_FNO", // Use NIFTY50_FNO to enable real orders
		Action:       strategy.ActionExit,
		Side:         strategySide,
		Token:        token,
		Exchange:     "NFO",
		Qty:          testQty,
		Price:        exitLTP,
		Reason:       fmt.Sprintf("Test %s SELL order (1 qty)", side),
	}

	executor.ExecuteSignal(sellSignal)
	sellEndTime := time.Now()
	sellDuration := sellEndTime.Sub(sellStartTime)
	
	log.Printf("✅ SELL order placed")
	log.Printf("⏱️  SELL order end time: %s", sellEndTime.Format("15:04:05.000"))
	log.Printf("⏱️  SELL order execution time: %v", sellDuration)

	// Calculate P&L and timing
	pnl := exitLTP - entryPrice
	pnlRupees := float64(pnl) / 100
	totalPnL := pnlRupees * float64(testQty)
	
	totalDuration := sellEndTime.Sub(buyStartTime)
	timeBetweenOrders := sellStartTime.Sub(buyEndTime)

	log.Println("\n╔════════════════════════════════════════════════════════╗")
	log.Println("║                    Test Complete                       ║")
	log.Println("╚════════════════════════════════════════════════════════╝")
	log.Printf("Entry Price:  ₹%.2f", float64(entryPrice)/100)
	log.Printf("Exit Price:   ₹%.2f", float64(exitLTP)/100)
	log.Printf("Price Change: ₹%.2f", pnlRupees)
	log.Printf("Quantity:     %d", testQty)
	log.Printf("Total P&L:    ₹%.2f", totalPnL)
	
	log.Println("\n⏱️  TIMING ANALYSIS:")
	log.Printf("BUY order start:         %s", buyStartTime.Format("15:04:05.000"))
	log.Printf("BUY order end:           %s", buyEndTime.Format("15:04:05.000"))
	log.Printf("BUY order execution:     %v (%.0f ms)", buyDuration, float64(buyDuration.Microseconds())/1000)
	log.Println("")
	log.Printf("Time between orders:     %v", timeBetweenOrders)
	log.Println("")
	log.Printf("SELL order start:        %s", sellStartTime.Format("15:04:05.000"))
	log.Printf("SELL order end:          %s", sellEndTime.Format("15:04:05.000"))
	log.Printf("SELL order execution:    %v (%.0f ms)", sellDuration, float64(sellDuration.Microseconds())/1000)
	log.Println("")
	log.Printf("Total test duration:     %v", totalDuration)

	if pnl > 0 {
		log.Printf("\n✅ Profit: +₹%.2f", totalPnL)
	} else if pnl < 0 {
		log.Printf("\n❌ Loss: ₹%.2f", totalPnL)
	} else {
		log.Println("\n➖ Break-even")
	}

	log.Println("\n⚠️  IMPORTANT:")
	log.Println("   • Check your Angel One account to verify orders")
	log.Println("   • This was a REAL order test with actual money")
	log.Println("   • 1 lot (65 units) was traded")
	log.Printf("   • Max risk was: ₹%.2f", float64(entryPrice*65)/100)
}

func getNiftyIndexPrice(sc *smartconnect.SmartConnect, token string) (int64, error) {
	// Retry up to 3 times with delays
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			log.Printf("   Retry attempt %d/3...", attempt)
			time.Sleep(time.Duration(attempt) * 2 * time.Second) // 2s, 4s, 6s
		}
		
		// Get NIFTY index market data
		// exchangeTokens format: {"NSE": ["token1", "token2"]}
		exchangeTokens := map[string][]string{
			"NSE": {token},
		}
		
		quotes, err := sc.GetMarketData("FULL", exchangeTokens)
		if err != nil {
			lastErr = fmt.Errorf("get NIFTY market data failed: %w", err)
			log.Printf("   ⚠️  Attempt %d failed: %v", attempt, err)
			continue
		}

		// Extract LTP from response
		if data, ok := quotes["data"].(map[string]interface{}); ok {
			if fetched, ok := data["fetched"].([]interface{}); ok && len(fetched) > 0 {
				if quote, ok := fetched[0].(map[string]interface{}); ok {
					// Try float64 first
					if ltp, ok := quote["ltp"].(float64); ok {
						return int64(ltp * 100), nil // Convert to paise
					}
					// Try string format
					if ltpStr, ok := quote["ltp"].(string); ok {
						if ltp, err := strconv.ParseFloat(ltpStr, 64); err == nil {
							return int64(ltp * 100), nil
						}
					}
				}
			}
		}
		
		lastErr = fmt.Errorf("NIFTY index price not found in market data response")
		log.Printf("   ⚠️  Attempt %d: LTP not found in response", attempt)
	}

	return 0, lastErr
}

func getLTP(sc *smartconnect.SmartConnect, token, exchange string) (int64, error) {
	// Retry up to 3 times with delays
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			log.Printf("   Retry attempt %d/3...", attempt)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		
		// Get market data from Angel One
		// exchangeTokens format: {"NFO": ["token1", "token2"]}
		exchangeTokens := map[string][]string{
			exchange: {token},
		}
		
		quotes, err := sc.GetMarketData("FULL", exchangeTokens)
		if err != nil {
			lastErr = fmt.Errorf("get market data failed: %w", err)
			log.Printf("   ⚠️  Attempt %d failed: %v", attempt, err)
			continue
		}

		// Extract LTP from response
		if data, ok := quotes["data"].(map[string]interface{}); ok {
			if fetched, ok := data["fetched"].([]interface{}); ok && len(fetched) > 0 {
				if quote, ok := fetched[0].(map[string]interface{}); ok {
					if ltp, ok := quote["ltp"].(float64); ok {
						return int64(ltp * 100), nil // Convert to paise
					}
				}
			}
		}
		
		lastErr = fmt.Errorf("LTP not found in market data response")
		log.Printf("   ⚠️  Attempt %d: LTP not found in response", attempt)
	}

	return 0, lastErr
}
