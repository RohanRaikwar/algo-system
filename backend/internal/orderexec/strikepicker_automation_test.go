package orderexec

import (
	"testing"
	"time"
)

func TestSelectContract_CarryUsesNextExpiry(t *testing.T) {
	input := testAutomationInput()
	input.HoldType = HoldTypeCarry

	picker := NewStrikePicker(nil)
	pick, err := picker.SelectContract(input)
	if err != nil {
		t.Fatalf("SelectContract error: %v", err)
	}

	if !sameExpiryDay(pick.SelectedExpiry, mustIST("2026-03-24 00:00")) {
		t.Fatalf("expected next expiry, got %s", pick.SelectedExpiry.Format(time.RFC3339))
	}
}

func TestSelectContract_ExpiryDaySidewaysUsesNextExpiry(t *testing.T) {
	input := testAutomationInput()
	input.IsExpiryDay = true
	input.MarketState = MarketStateSideways

	picker := NewStrikePicker(nil)
	pick, err := picker.SelectContract(input)
	if err != nil {
		t.Fatalf("SelectContract error: %v", err)
	}

	if !sameExpiryDay(pick.SelectedExpiry, mustIST("2026-03-24 00:00")) {
		t.Fatalf("expected next expiry on sideways expiry day, got %s", pick.SelectedExpiry.Format(time.RFC3339))
	}
}

func TestSelectContract_ExpiryDayEarlyTrendingUsesTodayExpiry(t *testing.T) {
	input := testAutomationInput()
	input.IsExpiryDay = true
	input.EntryTime = mustIST("2026-03-17 10:15")
	input.MarketState = MarketStateTrending
	input.TrendStrength = StrengthHigh
	input.MomentumStrength = StrengthHigh

	picker := NewStrikePicker(nil)
	pick, err := picker.SelectContract(input)
	if err != nil {
		t.Fatalf("SelectContract error: %v", err)
	}

	if !sameExpiryDay(pick.SelectedExpiry, mustIST("2026-03-17 00:00")) {
		t.Fatalf("expected today expiry, got %s", pick.SelectedExpiry.Format(time.RFC3339))
	}
	if pick.Contract.OptionType != OptionTypeCall {
		t.Fatalf("expected CE pick, got %s", pick.Contract.OptionType)
	}
}

func TestSelectContract_LateHighThetaForcesNextExpiry(t *testing.T) {
	input := testAutomationInput()
	input.IsExpiryDay = true
	input.EntryTime = mustIST("2026-03-17 13:45")

	picker := NewStrikePicker(nil)
	pick, err := picker.SelectContract(input)
	if err != nil {
		t.Fatalf("SelectContract error: %v", err)
	}

	if !sameExpiryDay(pick.SelectedExpiry, mustIST("2026-03-24 00:00")) {
		t.Fatalf("expected next expiry on late high-theta selection, got %s", pick.SelectedExpiry.Format(time.RFC3339))
	}
}

func TestSelectContract_PrefersATMThenOneITM(t *testing.T) {
	input := testAutomationInput()
	input.IsExpiryDay = false

	picker := NewStrikePicker(nil)
	pick, err := picker.SelectContract(input)
	if err != nil {
		t.Fatalf("SelectContract error: %v", err)
	}

	if pick.Contract.Strike != 23500 {
		t.Fatalf("expected ATM strike 23500, got %d", pick.Contract.Strike)
	}
}

func TestSelectContract_PrefersLowerVegaWhenIVNotRising(t *testing.T) {
	input := testAutomationInput()
	input.IsExpiryDay = false
	input.OptionChain = []OptionContract{
		{
			Strike:         23500,
			Expiry:         mustIST("2026-03-24 00:00"),
			OptionType:     OptionTypeCall,
			Delta:          0.55,
			Theta:          -3,
			Vega:           28,
			IV:             34,
			Premium:        185,
			LiquidityScore: 10,
		},
		{
			Strike:         23500,
			Expiry:         mustIST("2026-03-24 00:00"),
			OptionType:     OptionTypeCall,
			Delta:          0.56,
			Theta:          -3,
			Vega:           12,
			IV:             19,
			Premium:        182,
			LiquidityScore: 10,
		},
	}

	picker := NewStrikePicker(nil)
	pick, err := picker.SelectContract(input)
	if err != nil {
		t.Fatalf("SelectContract error: %v", err)
	}

	if pick.Contract.Vega != 12 {
		t.Fatalf("expected lower-vega contract, got %.2f", pick.Contract.Vega)
	}
}

func testAutomationInput() AutomationInput {
	return AutomationInput{
		SignalType:       SignalBuyCall,
		IsExpiryDay:      false,
		EntryTime:        mustIST("2026-03-17 10:30"),
		MarketState:      MarketStateTrending,
		TrendStrength:    StrengthMedium,
		MomentumStrength: StrengthMedium,
		HoldType:         HoldTypeIntraday,
		ExpectedIVMove:   IVMoveNeutral,
		SpotPrice:        2350000,
		OptionChain: []OptionContract{
			{
				Strike:         23500,
				Expiry:         mustIST("2026-03-17 00:00"),
				OptionType:     OptionTypeCall,
				Delta:          0.55,
				Theta:          -13,
				Vega:           10,
				IV:             18,
				Premium:        100,
				LiquidityScore: 12,
			},
			{
				Strike:         23450,
				Expiry:         mustIST("2026-03-17 00:00"),
				OptionType:     OptionTypeCall,
				Delta:          0.62,
				Theta:          -11,
				Vega:           9,
				IV:             18,
				Premium:        130,
				LiquidityScore: 11,
			},
			{
				Strike:         23600,
				Expiry:         mustIST("2026-03-17 00:00"),
				OptionType:     OptionTypeCall,
				Delta:          0.50,
				Theta:          -8,
				Vega:           10,
				IV:             19,
				Premium:        70,
				LiquidityScore: 9,
			},
			{
				Strike:         23500,
				Expiry:         mustIST("2026-03-24 00:00"),
				OptionType:     OptionTypeCall,
				Delta:          0.54,
				Theta:          -3,
				Vega:           18,
				IV:             17,
				Premium:        180,
				LiquidityScore: 10,
			},
			{
				Strike:         23450,
				Expiry:         mustIST("2026-03-24 00:00"),
				OptionType:     OptionTypeCall,
				Delta:          0.60,
				Theta:          -4,
				Vega:           16,
				IV:             17,
				Premium:        210,
				LiquidityScore: 11,
			},
			{
				Strike:         23500,
				Expiry:         mustIST("2026-03-17 00:00"),
				OptionType:     OptionTypePut,
				Delta:          -0.55,
				Theta:          -12,
				Vega:           10,
				IV:             18,
				Premium:        110,
				LiquidityScore: 12,
			},
			{
				Strike:         23550,
				Expiry:         mustIST("2026-03-24 00:00"),
				OptionType:     OptionTypePut,
				Delta:          -0.60,
				Theta:          -4,
				Vega:           15,
				IV:             17,
				Premium:        215,
				LiquidityScore: 11,
			},
		},
	}
}

func mustIST(value string) time.Time {
	ts, err := time.ParseInLocation("2006-01-02 15:04", value, istZone)
	if err != nil {
		panic(err)
	}
	return ts
}
