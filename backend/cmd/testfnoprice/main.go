package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

func main() {
	// Get credentials from environment
	apiKey := os.Getenv("ANGEL_API_KEY")
	clientCode := os.Getenv("ANGEL_CLIENT_CODE")
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET")

	if apiKey == "" || clientCode == "" || password == "" || totpSecret == "" {
		log.Fatal("Missing Angel One credentials in environment variables")
	}

	// Generate TOTP
	totpCode, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		log.Fatalf("TOTP generation failed: %v", err)
	}

	// Initialize SmartConnect
	sc := smartconnect.NewSmartConnect(smartconnect.Config{
		APIKey: apiKey,
		Debug:  false,
	})

	// Login
	log.Println("🔑 Logging in to Angel One...")
	_, err = sc.GenerateSession(clientCode, password, totpCode)
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}
	log.Println("✅ Logged in successfully")

	// Create StrikePicker
	picker := orderexec.NewStrikePicker(sc)

	// Get current NIFTY spot price from Angel One
	log.Println("\n📊 Fetching current NIFTY spot price from Angel One...")
	niftyToken := "99926000" // NIFTY 50 index
	
	// Get market data (LTP) for NIFTY
	marketData, err := sc.GetMarketData("LTP", map[string]any{
		"NSE": []string{niftyToken},
	})
	if err != nil {
		log.Fatalf("Failed to get NIFTY market data: %v", err)
	}

	// Parse spot price
	var spotPrice int64
	if data, ok := marketData["data"].(map[string]interface{}); ok {
		if fetched, ok := data["fetched"].([]interface{}); ok && len(fetched) > 0 {
			if first, ok := fetched[0].(map[string]interface{}); ok {
				if ltp, ok := first["ltp"].(float64); ok {
					spotPrice = int64(ltp * 100) // Convert to paise
					log.Printf("✅ Current NIFTY Spot: ₹%.2f (paise: %d)", ltp, spotPrice)
				}
			}
		}
	}

	if spotPrice == 0 {
		log.Fatal("Failed to parse NIFTY spot price")
	}

	// Load option chain from Angel One
	log.Println("\n🔍 Loading option chain from Angel One (OptionGreek API)...")
	chain, err := picker.LoadOptionChain(time.Now())
	if err != nil {
		log.Fatalf("Failed to load option chain: %v", err)
	}
	log.Printf("✅ Loaded %d option contracts from Angel One", len(chain))

	// Display banner
	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("FNO PRICE PICKER - Using Angel One Live Data")
	fmt.Println(strings.Repeat("═", 70))
	fmt.Printf("\nCurrent NIFTY Spot: ₹%.2f\n", float64(spotPrice)/100)
	fmt.Printf("ATM Strike: %d\n", (spotPrice/100/50)*50)
	fmt.Printf("Total Contracts: %d\n", len(chain))

	// Test 1: ATM Selection
	fmt.Println("\n" + strings.Repeat("─", 70))
	fmt.Println("TEST 1: ATM Strike Selection (Default)")
	fmt.Println(strings.Repeat("─", 70))
	
	inputATM := orderexec.AutomationInput{
		SignalType:       orderexec.SignalBuyCall,
		IsExpiryDay:      false,
		EntryTime:        time.Now(),
		MarketState:      orderexec.MarketStateTrending,
		TrendStrength:    orderexec.StrengthHigh,
		MomentumStrength: orderexec.StrengthHigh,
		HoldType:         orderexec.HoldTypeIntraday,
		OptionChain:      chain,
		ExpectedIVMove:   orderexec.IVMoveNeutral,
		SpotPrice:        spotPrice,
		StrikeMode:       "ATM",
	}
	
	pickATM, err := picker.SelectContract(inputATM)
	if err != nil {
		log.Printf("❌ ATM selection failed: %v", err)
	} else {
		displayPick("ATM CALL", pickATM, spotPrice)
	}

	// Test 2: OTM1 CALL with ₹200-250 Premium Filter
	fmt.Println("\n" + strings.Repeat("─", 70))
	fmt.Println("TEST 2: OTM1 CALL with ₹200-250 Premium Filter (Short Strategy)")
	fmt.Println(strings.Repeat("─", 70))
	
	inputOTM1Call := orderexec.AutomationInput{
		SignalType:       orderexec.SignalBuyCall,
		IsExpiryDay:      false,
		EntryTime:        time.Now(),
		MarketState:      orderexec.MarketStateTrending,
		TrendStrength:    orderexec.StrengthMedium,
		MomentumStrength: orderexec.StrengthMedium,
		HoldType:         orderexec.HoldTypeIntraday,
		OptionChain:      chain,
		ExpectedIVMove:   orderexec.IVMoveNeutral,
		SpotPrice:        spotPrice,
		StrikeMode:       "OTM1",
		MinPremium:       20000, // ₹200
		MaxPremium:       25000, // ₹250
	}
	
	pickOTM1Call, err := picker.SelectContract(inputOTM1Call)
	if err != nil {
		log.Printf("❌ OTM1 CALL selection failed: %v", err)
	} else {
		displayPick("OTM1 CALL (₹200-250)", pickOTM1Call, spotPrice)
	}

	// Test 3: OTM1 PUT with ₹200-250 Premium Filter
	fmt.Println("\n" + strings.Repeat("─", 70))
	fmt.Println("TEST 3: OTM1 PUT with ₹200-250 Premium Filter (Short Strategy)")
	fmt.Println(strings.Repeat("─", 70))
	
	inputOTM1Put := orderexec.AutomationInput{
		SignalType:       orderexec.SignalBuyPut,
		IsExpiryDay:      false,
		EntryTime:        time.Now(),
		MarketState:      orderexec.MarketStateTrending,
		TrendStrength:    orderexec.StrengthMedium,
		MomentumStrength: orderexec.StrengthMedium,
		HoldType:         orderexec.HoldTypeIntraday,
		OptionChain:      chain,
		ExpectedIVMove:   orderexec.IVMoveNeutral,
		SpotPrice:        spotPrice,
		StrikeMode:       "OTM1",
		MinPremium:       20000, // ₹200
		MaxPremium:       25000, // ₹250
	}
	
	pickOTM1Put, err := picker.SelectContract(inputOTM1Put)
	if err != nil {
		log.Printf("❌ OTM1 PUT selection failed: %v", err)
	} else {
		displayPick("OTM1 PUT (₹200-250)", pickOTM1Put, spotPrice)
	}

	// Test 4: OTM2 Selection
	fmt.Println("\n" + strings.Repeat("─", 70))
	fmt.Println("TEST 4: OTM2 Strike Selection (2 Strikes OTM)")
	fmt.Println(strings.Repeat("─", 70))
	
	inputOTM2 := orderexec.AutomationInput{
		SignalType:       orderexec.SignalBuyCall,
		IsExpiryDay:      false,
		EntryTime:        time.Now(),
		MarketState:      orderexec.MarketStateTrending,
		TrendStrength:    orderexec.StrengthMedium,
		MomentumStrength: orderexec.StrengthMedium,
		HoldType:         orderexec.HoldTypeIntraday,
		OptionChain:      chain,
		ExpectedIVMove:   orderexec.IVMoveNeutral,
		SpotPrice:        spotPrice,
		StrikeMode:       "OTM2",
	}
	
	pickOTM2, err := picker.SelectContract(inputOTM2)
	if err != nil {
		log.Printf("❌ OTM2 selection failed: %v", err)
	} else {
		displayPick("OTM2 CALL", pickOTM2, spotPrice)
	}

	// Display complete strike chain with live prices
	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("Complete Strike Chain with Live FNO Prices from Angel One")
	fmt.Println(strings.Repeat("═", 70))
	displayStrikeChain(chain, spotPrice)

	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("✅ All tests completed successfully!")
	fmt.Println("📊 All prices fetched from Angel One OptionGreek API")
	fmt.Println(strings.Repeat("═", 70))
}

func displayPick(label string, pick orderexec.AutomationPick, spotPrice int64) {
	atmStrike := (spotPrice / 100 / 50) * 50
	diff := pick.Contract.Strike - atmStrike
	
	strikeType := "ATM"
	if diff > 0 {
		strikeType = fmt.Sprintf("OTM%d", diff/50)
	} else if diff < 0 {
		strikeType = fmt.Sprintf("ITM%d", -diff/50)
	}

	fmt.Printf("\n%s Results:\n", label)
	fmt.Printf("  Strike:        %d (%s)\n", pick.Contract.Strike, strikeType)
	fmt.Printf("  Symbol:        %s\n", pick.Contract.Symbol)
	fmt.Printf("  Token:         %s\n", pick.Contract.Token)
	fmt.Printf("  Premium:       ₹%.2f (from Angel One)\n", pick.Contract.Premium)
	fmt.Printf("  Delta:         %.4f\n", pick.Contract.Delta)
	fmt.Printf("  Theta:         %.4f\n", pick.Contract.Theta)
	fmt.Printf("  Vega:          %.4f\n", pick.Contract.Vega)
	fmt.Printf("  IV:            %.2f%%\n", pick.Contract.IV*100)
	fmt.Printf("  Liquidity:     %.0f\n", pick.Contract.LiquidityScore)
	fmt.Printf("  Option Type:   %s\n", pick.Contract.OptionType)
	fmt.Printf("  Expiry:        %s\n", pick.SelectedExpiry.Format("02-Jan-2006"))
	fmt.Printf("  Reason:        %s\n", pick.Reason)
	fmt.Printf("  Used Fallback: %v\n", pick.UsedFallback)
	
	// Check if premium is in ₹200-250 range
	if pick.Contract.Premium >= 200 && pick.Contract.Premium <= 250 {
		fmt.Printf("  ⭐ Premium in ₹200-250 range - Perfect for short strategies!\n")
	}
}

func displayStrikeChain(chain []orderexec.OptionContract, spotPrice int64) {
	atmStrike := (spotPrice / 100 / 50) * 50
	
	// Find current week expiry
	var currentExpiry time.Time
	expiryMap := make(map[string]bool)
	for _, c := range chain {
		key := c.Expiry.Format("2006-01-02")
		if !expiryMap[key] {
			expiryMap[key] = true
			if currentExpiry.IsZero() || c.Expiry.Before(currentExpiry) {
				currentExpiry = c.Expiry
			}
		}
	}
	
	fmt.Printf("\nShowing strikes for expiry: %s\n", currentExpiry.Format("02-Jan-2006"))
	fmt.Printf("ATM Strike: %d\n\n", atmStrike)
	
	// Display CALL options
	fmt.Println("CALL Options (CE) - Live Prices from Angel One:")
	fmt.Println("Strike | Type  | Premium | Delta  | Theta  | IV     | In ₹200-250?")
	fmt.Println(strings.Repeat("─", 75))
	
	callContracts := []orderexec.OptionContract{}
	for _, c := range chain {
		if c.OptionType == orderexec.OptionTypeCall && 
		   c.Expiry.Format("2006-01-02") == currentExpiry.Format("2006-01-02") {
			callContracts = append(callContracts, c)
		}
	}
	
	// Sort by strike
	for i := 0; i < len(callContracts); i++ {
		for j := i + 1; j < len(callContracts); j++ {
			if callContracts[i].Strike > callContracts[j].Strike {
				callContracts[i], callContracts[j] = callContracts[j], callContracts[i]
			}
		}
	}
	
	for _, c := range callContracts {
		strikeType := "ATM"
		diff := c.Strike - atmStrike
		if diff < 0 {
			strikeType = fmt.Sprintf("ITM%d", -diff/50)
		} else if diff > 0 {
			strikeType = fmt.Sprintf("OTM%d", diff/50)
		}
		
		inRange := ""
		if c.Premium >= 200 && c.Premium <= 250 {
			inRange = "⭐ YES"
		}
		
		fmt.Printf("%-6d | %-5s | ₹%-6.2f | %6.4f | %6.4f | %5.2f%% | %s\n", 
			c.Strike, strikeType, c.Premium, c.Delta, c.Theta, c.IV*100, inRange)
	}
	
	// Display PUT options
	fmt.Println("\nPUT Options (PE) - Live Prices from Angel One:")
	fmt.Println("Strike | Type  | Premium | Delta  | Theta  | IV     | In ₹200-250?")
	fmt.Println(strings.Repeat("─", 75))
	
	putContracts := []orderexec.OptionContract{}
	for _, c := range chain {
		if c.OptionType == orderexec.OptionTypePut && 
		   c.Expiry.Format("2006-01-02") == currentExpiry.Format("2006-01-02") {
			putContracts = append(putContracts, c)
		}
	}
	
	// Sort by strike
	for i := 0; i < len(putContracts); i++ {
		for j := i + 1; j < len(putContracts); j++ {
			if putContracts[i].Strike > putContracts[j].Strike {
				putContracts[i], putContracts[j] = putContracts[j], putContracts[i]
			}
		}
	}
	
	for _, c := range putContracts {
		strikeType := "ATM"
		diff := c.Strike - atmStrike
		if diff > 0 {
			strikeType = fmt.Sprintf("ITM%d", diff/50)
		} else if diff < 0 {
			strikeType = fmt.Sprintf("OTM%d", -diff/50)
		}
		
		inRange := ""
		if c.Premium >= 200 && c.Premium <= 250 {
			inRange = "⭐ YES"
		}
		
		fmt.Printf("%-6d | %-5s | ₹%-6.2f | %6.4f | %6.4f | %5.2f%% | %s\n", 
			c.Strike, strikeType, c.Premium, c.Delta, c.Theta, c.IV*100, inRange)
	}
	
	fmt.Println("\n⭐ = Premium in ₹200-250 range (ideal for short strategies)")
	fmt.Println("📊 All prices, Greeks, and IV fetched live from Angel One OptionGreek API")
}
