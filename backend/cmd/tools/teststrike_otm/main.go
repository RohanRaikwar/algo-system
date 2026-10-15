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
	// Check if running in mock mode (no credentials needed)
	mockMode := os.Getenv("MOCK_MODE")
	if mockMode == "true" || mockMode == "1" {
		log.Println("🧪 Running in MOCK MODE (no Angel One connection)")
		TestStrikeSelection24000()
		return
	}

	// Get credentials from environment
	apiKey := os.Getenv("ANGEL_API_KEY")
	clientCode := os.Getenv("ANGEL_CLIENT_CODE")
	password := os.Getenv("ANGEL_PASSWORD")
	totpSecret := os.Getenv("ANGEL_TOTP_SECRET")

	if apiKey == "" || clientCode == "" || password == "" || totpSecret == "" {
		log.Println("⚠️  Missing Angel One credentials")
		log.Println("💡 Running in MOCK MODE instead...")
		TestStrikeSelection24000()
		return
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

	// Test with current NIFTY spot price (example: 23850)
	indexPrice := int64(23850 * 100) // Convert to paise: 2385000
	log.Printf("\n📊 Testing OTM strike selection with index price: ₹%.2f (paise: %d)", float64(indexPrice)/100, indexPrice)

	// Load option chain
	log.Println("\n🔍 Loading option chain...")
	chain, err := picker.LoadOptionChain(time.Now())
	if err != nil {
		log.Fatalf("Failed to load option chain: %v", err)
	}
	log.Printf("✅ Loaded %d option contracts", len(chain))

	// Test 1: ATM selection (default)
	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("TEST 1: ATM Strike Selection (Default)")
	fmt.Println(strings.Repeat("═", 70))
	
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
		SpotPrice:        indexPrice,
		StrikeMode:       "ATM",
	}
	
	pickATM, err := picker.SelectContract(inputATM)
	if err != nil {
		log.Printf("❌ ATM selection failed: %v", err)
	} else {
		displayPick("ATM CALL", pickATM)
	}

	// Test 2: OTM1 selection (1 strike OTM)
	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("TEST 2: OTM1 Strike Selection (1 Strike Out-of-Money)")
	fmt.Println(strings.Repeat("═", 70))
	
	inputOTM1 := orderexec.AutomationInput{
		SignalType:       orderexec.SignalBuyCall,
		IsExpiryDay:      false,
		EntryTime:        time.Now(),
		MarketState:      orderexec.MarketStateTrending,
		TrendStrength:    orderexec.StrengthHigh,
		MomentumStrength: orderexec.StrengthHigh,
		HoldType:         orderexec.HoldTypeIntraday,
		OptionChain:      chain,
		ExpectedIVMove:   orderexec.IVMoveNeutral,
		SpotPrice:        indexPrice,
		StrikeMode:       "OTM1",
	}
	
	pickOTM1, err := picker.SelectContract(inputOTM1)
	if err != nil {
		log.Printf("❌ OTM1 selection failed: %v", err)
	} else {
		displayPick("OTM1 CALL", pickOTM1)
	}

	// Test 3: OTM selection with 200-250 premium filter
	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("TEST 3: OTM Strike with ₹200-250 Premium Filter (Short/Sell Strategy)")
	fmt.Println(strings.Repeat("═", 70))
	
	inputShort := orderexec.AutomationInput{
		SignalType:       orderexec.SignalBuyPut,
		IsExpiryDay:      false,
		EntryTime:        time.Now(),
		MarketState:      orderexec.MarketStateTrending,
		TrendStrength:    orderexec.StrengthMedium,
		MomentumStrength: orderexec.StrengthMedium,
		HoldType:         orderexec.HoldTypeIntraday,
		OptionChain:      chain,
		ExpectedIVMove:   orderexec.IVMoveNeutral,
		SpotPrice:        indexPrice,
		StrikeMode:       "OTM2", // 2 strikes OTM for short strategy
		MinPremium:       20000,  // ₹200 in paise
		MaxPremium:       25000,  // ₹250 in paise
	}
	
	pickShort, err := picker.SelectContract(inputShort)
	if err != nil {
		log.Printf("❌ Short strategy selection failed: %v", err)
	} else {
		displayPick("SHORT PUT (₹200-250)", pickShort)
	}

	// Test 4: Show all OTM strikes with premiums
	fmt.Println("\n" + strings.Repeat("═", 70))
	fmt.Println("TEST 4: All Available OTM Strikes with Premiums")
	fmt.Println(strings.Repeat("═", 70))
	
	displayOTMStrikes(chain, indexPrice)
}

func displayPick(label string, pick orderexec.AutomationPick) {
	fmt.Printf("\n%s Selection:\n", label)
	fmt.Printf("   Strike:        %d\n", pick.Contract.Strike)
	fmt.Printf("   Symbol:        %s\n", pick.Contract.Symbol)
	fmt.Printf("   Premium:       ₹%.2f\n", pick.Contract.Premium)
	fmt.Printf("   Delta:         %.4f\n", pick.Contract.Delta)
	fmt.Printf("   Theta:         %.4f\n", pick.Contract.Theta)
	fmt.Printf("   Vega:          %.4f\n", pick.Contract.Vega)
	fmt.Printf("   IV:            %.2f%%\n", pick.Contract.IV*100)
	fmt.Printf("   Option Type:   %s\n", pick.Contract.OptionType)
	fmt.Printf("   Expiry:        %s\n", pick.SelectedExpiry.Format("02-Jan-2006"))
	fmt.Printf("   Reason:        %s\n", pick.Reason)
	fmt.Printf("   Used Fallback: %v\n", pick.UsedFallback)
}

func displayOTMStrikes(chain []orderexec.OptionContract, spotPrice int64) {
	spotRupees := float64(spotPrice) / 100.0
	atmStrike := int64(spotRupees/50) * 50
	
	fmt.Printf("\nSpot Price: ₹%.2f | ATM Strike: %d\n\n", spotRupees, atmStrike)
	
	// Filter for current week expiry
	var currentExpiry time.Time
	for _, c := range chain {
		if currentExpiry.IsZero() || c.Expiry.Before(currentExpiry) {
			currentExpiry = c.Expiry
		}
	}
	
	fmt.Printf("Showing strikes for expiry: %s\n\n", currentExpiry.Format("02-Jan-2006"))
	
	// Display CALL strikes
	fmt.Println("CALL Options (CE):")
	fmt.Println("Strike | Premium | Delta  | Type")
	fmt.Println(strings.Repeat("-", 50))
	
	for _, c := range chain {
		if c.OptionType != orderexec.OptionTypeCall {
			continue
		}
		if !c.Expiry.Equal(currentExpiry) {
			continue
		}
		
		strikeType := "ATM"
		diff := c.Strike - atmStrike
		if diff < 0 {
			strikeType = fmt.Sprintf("ITM%d", -diff/50)
		} else if diff > 0 {
			strikeType = fmt.Sprintf("OTM%d", diff/50)
		}
		
		premiumRupees := c.Premium
		if premiumRupees >= 200 && premiumRupees <= 250 {
			fmt.Printf("%-6d | ₹%-6.2f | %6.4f | %s ⭐\n", c.Strike, premiumRupees, c.Delta, strikeType)
		} else {
			fmt.Printf("%-6d | ₹%-6.2f | %6.4f | %s\n", c.Strike, premiumRupees, c.Delta, strikeType)
		}
	}
	
	// Display PUT strikes
	fmt.Println("\nPUT Options (PE):")
	fmt.Println("Strike | Premium | Delta  | Type")
	fmt.Println(strings.Repeat("-", 50))
	
	for _, c := range chain {
		if c.OptionType != orderexec.OptionTypePut {
			continue
		}
		if !c.Expiry.Equal(currentExpiry) {
			continue
		}
		
		strikeType := "ATM"
		diff := c.Strike - atmStrike
		if diff > 0 {
			strikeType = fmt.Sprintf("ITM%d", diff/50)
		} else if diff < 0 {
			strikeType = fmt.Sprintf("OTM%d", -diff/50)
		}
		
		premiumRupees := c.Premium
		if premiumRupees >= 200 && premiumRupees <= 250 {
			fmt.Printf("%-6d | ₹%-6.2f | %6.4f | %s ⭐\n", c.Strike, premiumRupees, c.Delta, strikeType)
		} else {
			fmt.Printf("%-6d | ₹%-6.2f | %6.4f | %s\n", c.Strike, premiumRupees, c.Delta, strikeType)
		}
	}
	
	fmt.Println("\n⭐ = Premium in ₹200-250 range (ideal for short/sell strategies)")
}
