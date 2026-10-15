package main

import (
	"fmt"
	"time"

	"trading-systemv1/internal/orderexec"
)

// TestStrikeSelection24000 tests strike selection with spot price at 24000
func TestStrikeSelection24000() {
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("Testing Strike Selection with Spot Price: ₹24,000")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	spotPrice := int64(24000 * 100) // 2400000 paise
	step := int64(50)

	// Create mock option chain for 24000 spot
	chain := createMockOptionChain24000()

	fmt.Printf("\nSpot Price: ₹24,000\n")
	fmt.Printf("ATM Strike: 24,000\n")
	fmt.Printf("Strike Step: %d\n\n", step)

	// Test 1: ATM Selection
	fmt.Println("─────────────────────────────────────────────────────────────")
	fmt.Println("TEST 1: ATM Strike Selection")
	fmt.Println("─────────────────────────────────────────────────────────────")
	
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

	testSelection("ATM CALL", inputATM, 24000)

	// Test 2: OTM1 Selection
	fmt.Println("\n─────────────────────────────────────────────────────────────")
	fmt.Println("TEST 2: OTM1 Strike Selection (1 Strike OTM)")
	fmt.Println("─────────────────────────────────────────────────────────────")
	
	inputOTM1 := inputATM
	inputOTM1.StrikeMode = "OTM1"
	testSelection("OTM1 CALL", inputOTM1, 24050)

	// Test 3: OTM2 Selection
	fmt.Println("\n─────────────────────────────────────────────────────────────")
	fmt.Println("TEST 3: OTM2 Strike Selection (2 Strikes OTM)")
	fmt.Println("─────────────────────────────────────────────────────────────")
	
	inputOTM2 := inputATM
	inputOTM2.StrikeMode = "OTM2"
	testSelection("OTM2 CALL", inputOTM2, 24100)

	// Test 4: OTM1 PUT with Premium Filter (₹200-250)
	fmt.Println("\n─────────────────────────────────────────────────────────────")
	fmt.Println("TEST 4: OTM1 PUT with ₹200-250 Premium Filter (Short Strategy)")
	fmt.Println("─────────────────────────────────────────────────────────────")
	
	inputShortPut := orderexec.AutomationInput{
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
	testSelection("OTM1 PUT (₹200-250)", inputShortPut, 23950)

	// Test 5: OTM2 PUT with Premium Filter
	fmt.Println("\n─────────────────────────────────────────────────────────────")
	fmt.Println("TEST 5: OTM2 PUT with ₹200-250 Premium Filter")
	fmt.Println("─────────────────────────────────────────────────────────────")
	
	inputShortPut2 := inputShortPut
	inputShortPut2.StrikeMode = "OTM2"
	testSelection("OTM2 PUT (₹200-250)", inputShortPut2, 23900)

	// Display strike chain
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("Complete Strike Chain for Spot: ₹24,000")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	displayStrikeChain(chain, spotPrice)
}

func testSelection(label string, input orderexec.AutomationInput, expectedStrike int64) {
	picker := &orderexec.StrikePicker{}
	
	// Debug: show what candidates are available before selection
	fmt.Printf("\nAvailable candidates for %s:\n", label)
	optType := orderexec.OptionTypeCall
	if input.SignalType == orderexec.SignalBuyPut {
		optType = orderexec.OptionTypePut
	}
	
	count := 0
	for _, c := range input.OptionChain {
		if c.OptionType == optType {
			count++
			if count <= 5 {
				fmt.Printf("  Strike %d: Premium=₹%.2f, Delta=%.4f\n", c.Strike, c.Premium, c.Delta)
			}
		}
	}
	fmt.Printf("  ... (%d total %s contracts)\n", count, optType)
	
	pick, err := picker.SelectContract(input)
	
	if err != nil {
		fmt.Printf("❌ %s FAILED: %v\n", label, err)
		return
	}

	fmt.Printf("\n%s Results:\n", label)
	fmt.Printf("  Expected Strike: %d\n", expectedStrike)
	fmt.Printf("  Selected Strike: %d", pick.Contract.Strike)
	
	if pick.Contract.Strike == expectedStrike {
		fmt.Printf(" ✅\n")
	} else {
		fmt.Printf(" ⚠️  (mismatch)\n")
	}
	
	fmt.Printf("  Premium:         ₹%.2f\n", pick.Contract.Premium)
	fmt.Printf("  Delta:           %.4f\n", pick.Contract.Delta)
	fmt.Printf("  Option Type:     %s\n", pick.Contract.OptionType)
	fmt.Printf("  Reason:          %s\n", pick.Reason)
	fmt.Printf("  Used Fallback:   %v\n", pick.UsedFallback)

	// Verify premium range if specified
	if input.MinPremium > 0 || input.MaxPremium > 0 {
		premiumPaise := pick.Contract.Premium * 100
		inRange := true
		if input.MinPremium > 0 && premiumPaise < input.MinPremium {
			inRange = false
		}
		if input.MaxPremium > 0 && premiumPaise > input.MaxPremium {
			inRange = false
		}
		if inRange {
			fmt.Printf("  Premium Range:   ✅ In range (₹%.0f-₹%.0f)\n", 
				input.MinPremium/100, input.MaxPremium/100)
		} else {
			fmt.Printf("  Premium Range:   ⚠️  Out of range (₹%.0f-₹%.0f)\n", 
				input.MinPremium/100, input.MaxPremium/100)
		}
	}
}

func createMockOptionChain24000() []orderexec.OptionContract {
	expiry := time.Now().AddDate(0, 0, 3) // 3 days from now
	
	strikes := []int64{
		23700, 23750, 23800, 23850, 23900, 23950,
		24000, // ATM
		24050, 24100, 24150, 24200, 24250, 24300,
	}

	var chain []orderexec.OptionContract

	for _, strike := range strikes {
		diff := strike - 24000
		
		// CALL options - more realistic premium decay
		callDelta := 0.50 - float64(diff)/1000.0
		if callDelta < 0.10 {
			callDelta = 0.10
		}
		if callDelta > 0.90 {
			callDelta = 0.90
		}
		
		// More realistic premium calculation
		// ATM: ~280, OTM1: ~220, OTM2: ~170, OTM3: ~130
		callPremium := 280.0
		if diff > 0 {
			// OTM: premium decreases
			callPremium = 280.0 - float64(diff)*2.2
		} else if diff < 0 {
			// ITM: premium increases
			callPremium = 280.0 - float64(diff)*2.2
		}
		if callPremium < 50 {
			callPremium = 50
		}

		chain = append(chain, orderexec.OptionContract{
			Token:          fmt.Sprintf("CE%d", strike),
			Symbol:         fmt.Sprintf("NIFTY%s%dCE", expiry.Format("02Jan06"), strike),
			Strike:         strike,
			Expiry:         expiry,
			OptionType:     orderexec.OptionTypeCall,
			Delta:          callDelta,
			Theta:          -0.05,
			Vega:           0.15,
			IV:             0.18,
			Premium:        callPremium,
			LiquidityScore: 1000,
		})

		// PUT options - more realistic premium decay
		putDelta := -0.50 + float64(diff)/1000.0
		if putDelta > -0.10 {
			putDelta = -0.10
		}
		if putDelta < -0.90 {
			putDelta = -0.90
		}
		
		// More realistic premium calculation
		// ATM: ~270, OTM1: ~210, OTM2: ~160, OTM3: ~120
		putPremium := 270.0
		if diff < 0 {
			// OTM: premium decreases
			putPremium = 270.0 + float64(diff)*2.2
		} else if diff > 0 {
			// ITM: premium increases
			putPremium = 270.0 + float64(diff)*2.2
		}
		if putPremium < 50 {
			putPremium = 50
		}

		chain = append(chain, orderexec.OptionContract{
			Token:          fmt.Sprintf("PE%d", strike),
			Symbol:         fmt.Sprintf("NIFTY%s%dPE", expiry.Format("02Jan06"), strike),
			Strike:         strike,
			Expiry:         expiry,
			OptionType:     orderexec.OptionTypePut,
			Delta:          putDelta,
			Theta:          -0.05,
			Vega:           0.15,
			IV:             0.18,
			Premium:        putPremium,
			LiquidityScore: 1000,
		})
	}

	return chain
}

func displayStrikeChain(chain []orderexec.OptionContract, spotPrice int64) {
	atmStrike := int64(24000)
	
	fmt.Println("\nCALL Options (CE):")
	fmt.Println("Strike | Type  | Premium | Delta  | In ₹200-250?")
	fmt.Println("───────┼───────┼─────────┼────────┼─────────────")
	
	for _, c := range chain {
		if c.OptionType != orderexec.OptionTypeCall {
			continue
		}
		
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
		
		fmt.Printf("%-6d | %-5s | ₹%-6.2f | %6.4f | %s\n", 
			c.Strike, strikeType, c.Premium, c.Delta, inRange)
	}
	
	fmt.Println("\nPUT Options (PE):")
	fmt.Println("Strike | Type  | Premium | Delta  | In ₹200-250?")
	fmt.Println("───────┼───────┼─────────┼────────┼─────────────")
	
	for _, c := range chain {
		if c.OptionType != orderexec.OptionTypePut {
			continue
		}
		
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
		
		fmt.Printf("%-6d | %-5s | ₹%-6.2f | %6.4f | %s\n", 
			c.Strike, strikeType, c.Premium, c.Delta, inRange)
	}
	
	fmt.Println("\n⭐ = Premium in ₹200-250 range (ideal for short strategies)")
}
