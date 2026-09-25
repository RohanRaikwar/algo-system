# OTM Strike Selection Test

This test program demonstrates the enhanced strike picker with OTM (Out-of-The-Money) strike selection and premium filtering for short/sell strategies.

## Features

### 1. Strike Selection Modes

The strike picker now supports multiple selection modes:

- **ATM** (At-The-Money): Default mode, selects strikes closest to spot price
- **OTM1**: Selects 1 strike out-of-the-money
- **OTM2**: Selects 2 strikes out-of-the-money  
- **OTM3**: Selects 3 strikes out-of-the-money

### 2. Premium Range Filtering

For short/sell strategies, you can filter strikes by premium range:

```go
input := orderexec.AutomationInput{
    StrikeMode:  "OTM2",
    MinPremium:  20000,  // ₹200 in paise
    MaxPremium:  25000,  // ₹250 in paise
    // ... other fields
}
```

This is ideal for option selling strategies where you want to collect premiums in the ₹200-250 range.

### 3. Strike Ranking System

The strike picker now uses a comprehensive ranking system:

**For CALL Options:**
- Rank 0: ATM (strike = spot)
- Rank 1: 1 ITM (strike = spot - 50)
- Rank 2: 1 OTM (strike = spot + 50) ⭐ Good for short strategies
- Rank 3: 2 OTM (strike = spot + 100)
- Rank 4: 3 OTM (strike = spot + 150)
- Rank 5: Far OTM (strike > spot + 150)

**For PUT Options:**
- Rank 0: ATM (strike = spot)
- Rank 1: 1 ITM (strike = spot + 50)
- Rank 2: 1 OTM (strike = spot - 50) ⭐ Good for short strategies
- Rank 3: 2 OTM (strike = spot - 100)
- Rank 4: 3 OTM (strike = spot - 150)
- Rank 5: Far OTM (strike < spot - 150)

## Usage

### Run the Test

```bash
# Set environment variables
source .env

# Run the test
go run ./cmd/teststrike_otm
```

### Expected Output

The test will show:

1. **ATM Strike Selection**: Default behavior (buying strategies)
2. **OTM1 Strike Selection**: 1 strike out-of-the-money
3. **Short Strategy Selection**: OTM2 with ₹200-250 premium filter
4. **All Available Strikes**: Complete list with premiums marked

## Short/Sell Strategy Configuration

For a "sour system" (short/sell strategy) that collects ₹200-250 premiums:

```go
input := orderexec.AutomationInput{
    SignalType:       orderexec.SignalBuyPut,  // or SignalBuyCall
    StrikeMode:       "OTM2",                   // 2 strikes OTM
    MinPremium:       20000,                    // ₹200
    MaxPremium:       25000,                    // ₹250
    MarketState:      orderexec.MarketStateTrending,
    TrendStrength:    orderexec.StrengthMedium,
    MomentumStrength: orderexec.StrengthMedium,
    HoldType:         orderexec.HoldTypeIntraday,
    SpotPrice:        indexPrice,
    OptionChain:      chain,
}

pick, err := picker.SelectContract(input)
```

## Integration with Strategy Engine

To use OTM strikes in your strategy:

1. **Update AutomationInput** in your strategy's entry logic
2. **Set StrikeMode** to "OTM1", "OTM2", or "OTM3"
3. **Add Premium Filters** for short strategies
4. **Adjust Delta Filters** if needed (OTM strikes have lower delta)

Example integration:

```go
// In stratengine/service.go or strategy entry logic
automationInput := orderexec.AutomationInput{
    SignalType:       signalType,
    StrikeMode:       os.Getenv("STRAT_STRIKE_MODE"),      // "OTM2"
    MinPremium:       getEnvFloat("STRAT_MIN_PREMIUM", 0), // 20000
    MaxPremium:       getEnvFloat("STRAT_MAX_PREMIUM", 0), // 25000
    // ... other fields
}
```

## Environment Variables

Add these to your `.env` file:

```bash
# Strike selection mode: ATM, OTM1, OTM2, OTM3
STRAT_STRIKE_MODE=OTM2

# Premium range for short strategies (in paise)
STRAT_MIN_PREMIUM=20000  # ₹200
STRAT_MAX_PREMIUM=25000  # ₹250
```

## Benefits of OTM Strikes for Short Strategies

1. **Lower Risk**: OTM strikes are less likely to be exercised
2. **Premium Collection**: Collect ₹200-250 per lot
3. **Higher Probability**: OTM options have higher probability of expiring worthless
4. **Better Risk/Reward**: Lower margin requirements vs ITM/ATM

## Notes

- OTM strikes have **lower delta** (0.20-0.40 range)
- Premium decays faster as expiry approaches (**theta decay**)
- Best for **sideways to mildly trending** markets
- Requires **proper risk management** (stop losses, position sizing)

## Troubleshooting

If no strikes are found in the premium range:

1. Check if `MinPremium` and `MaxPremium` are realistic for current market
2. Try wider strike modes (OTM1, OTM2, OTM3)
3. Check option chain data is loaded correctly
4. Verify spot price is accurate

The system will automatically fall back to best available strikes if filters are too restrictive.
