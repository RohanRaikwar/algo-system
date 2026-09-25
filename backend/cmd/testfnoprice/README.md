# FNO Price Picker - Using Angel One Live Data

This test program demonstrates how to fetch **live FNO (F&O option) prices** from Angel One and select OTM strikes with premium filtering.

## What It Does

1. **Fetches Current NIFTY Spot Price** from Angel One LTP API
2. **Loads Complete Option Chain** using Angel One OptionGreek API
3. **Selects OTM Strikes** with ₹200-250 premium filtering
4. **Displays Live Prices** with Greeks (Delta, Theta, Vega, IV)

## Features

✅ **Live Market Data** - All prices fetched from Angel One in real-time
✅ **Option Greeks** - Delta, Theta, Vega, IV from Angel One
✅ **OTM Strike Selection** - Automatic OTM1, OTM2, OTM3 selection
✅ **Premium Filtering** - Filter strikes by ₹200-250 range
✅ **Complete Strike Chain** - View all available strikes with live prices

## Usage

### Run the Test

```bash
cd backend
source ../.env
go run ./cmd/testfnoprice
```

### Expected Output

```
🔑 Logging in to Angel One...
✅ Logged in successfully

📊 Fetching current NIFTY spot price from Angel One...
✅ Current NIFTY Spot: ₹23,850.00 (paise: 2385000)

🔍 Loading option chain from Angel One (OptionGreek API)...
✅ Loaded 200 option contracts from Angel One

═══════════════════════════════════════════════════════════════
FNO PRICE PICKER - Using Angel One Live Data
═══════════════════════════════════════════════════════════════

Current NIFTY Spot: ₹23,850.00
ATM Strike: 23850
Total Contracts: 200

──────────────────────────────────────────────────────────────
TEST 1: ATM Strike Selection (Default)
──────────────────────────────────────────────────────────────

ATM CALL Results:
  Strike:        23850 (ATM)
  Symbol:        NIFTY17MAR2623850CE
  Token:         57791
  Premium:       ₹285.50 (from Angel One)
  Delta:         0.5123
  Theta:         -0.0456
  Vega:          0.1234
  IV:            18.45%
  Liquidity:     15000
  Option Type:   CE
  Expiry:        17-Mar-2026
  Reason:        session=early_session,strike_mode=ATM
  Used Fallback: false

──────────────────────────────────────────────────────────────
TEST 2: OTM1 CALL with ₹200-250 Premium Filter (Short Strategy)
──────────────────────────────────────────────────────────────

OTM1 CALL (₹200-250) Results:
  Strike:        23900 (OTM1)
  Symbol:        NIFTY17MAR2623900CE
  Token:         57792
  Premium:       ₹225.75 (from Angel One)
  Delta:         0.4567
  Theta:         -0.0423
  Vega:          0.1156
  IV:            17.89%
  Liquidity:     12500
  Option Type:   CE
  Expiry:        17-Mar-2026
  Reason:        session=early_session,strike_mode=OTM1,premium_filter_200-250
  Used Fallback: false
  ⭐ Premium in ₹200-250 range - Perfect for short strategies!

──────────────────────────────────────────────────────────────
Complete Strike Chain with Live FNO Prices from Angel One
──────────────────────────────────────────────────────────────

CALL Options (CE) - Live Prices from Angel One:
Strike | Type  | Premium | Delta  | Theta  | IV     | In ₹200-250?
───────────────────────────────────────────────────────────────────────
23700  | ITM3  | ₹485.25 | 0.6789 | -0.0512 | 19.23% | 
23750  | ITM2  | ₹435.50 | 0.6234 | -0.0489 | 18.95% | 
23800  | ITM1  | ₹385.75 | 0.5678 | -0.0467 | 18.67% | 
23850  | ATM   | ₹285.50 | 0.5123 | -0.0456 | 18.45% | 
23900  | OTM1  | ₹225.75 | 0.4567 | -0.0423 | 17.89% | ⭐ YES
23950  | OTM2  | ₹175.25 | 0.4012 | -0.0389 | 17.34% | 
24000  | OTM3  | ₹135.50 | 0.3456 | -0.0356 | 16.78% | 

PUT Options (PE) - Live Prices from Angel One:
Strike | Type  | Premium | Delta  | Theta  | IV     | In ₹200-250?
───────────────────────────────────────────────────────────────────────
23700  | OTM3  | ₹125.50 | -0.3456 | -0.0356 | 16.78% | 
23750  | OTM2  | ₹165.25 | -0.4012 | -0.0389 | 17.34% | 
23800  | OTM1  | ₹215.75 | -0.4567 | -0.0423 | 17.89% | ⭐ YES
23850  | ATM   | ₹275.50 | -0.5123 | -0.0456 | 18.45% | 
23900  | ITM1  | ₹375.75 | -0.5678 | -0.0467 | 18.67% | 
23950  | ITM2  | ₹425.50 | -0.6234 | -0.0489 | 18.95% | 
24000  | ITM3  | ₹475.25 | -0.6789 | -0.0512 | 19.23% | 

⭐ = Premium in ₹200-250 range (ideal for short strategies)
📊 All prices, Greeks, and IV fetched live from Angel One OptionGreek API

═══════════════════════════════════════════════════════════════
✅ All tests completed successfully!
📊 All prices fetched from Angel One OptionGreek API
═══════════════════════════════════════════════════════════════
```

## How It Works

### 1. Fetch NIFTY Spot Price

```go
// Get LTP (Last Traded Price) for NIFTY
ltpRes, err := sc.GetLTP(map[string]any{
    "exchange":      "NSE",
    "symboltoken":   "99926000",
    "tradingsymbol": "NIFTY 50",
})

// Parse spot price
spotPrice := int64(ltp * 100) // Convert to paise
```

### 2. Load Option Chain from Angel One

```go
// Create strike picker
picker := orderexec.NewStrikePicker(sc)

// Load option chain using OptionGreek API
chain, err := picker.LoadOptionChain(time.Now())
// Returns: []OptionContract with live prices, Greeks, IV
```

### 3. Select OTM Strike with Premium Filter

```go
input := orderexec.AutomationInput{
    SignalType:  orderexec.SignalBuyCall,
    StrikeMode:  "OTM1",        // 1 strike OTM
    MinPremium:  20000,         // ₹200 in paise
    MaxPremium:  25000,         // ₹250 in paise
    SpotPrice:   spotPrice,
    OptionChain: chain,         // Live data from Angel One
}

pick, err := picker.SelectContract(input)
// Returns: Selected strike with live premium from Angel One
```

### 4. Get Token and Price

```go
// Selected strike information
token := pick.Contract.Token          // e.g., "57792"
symbol := pick.Contract.Symbol        // e.g., "NIFTY17MAR2623900CE"
premium := pick.Contract.Premium      // e.g., 225.75 (from Angel One)
delta := pick.Contract.Delta          // e.g., 0.4567
strike := pick.Contract.Strike        // e.g., 23900
```

## Angel One APIs Used

### 1. GetLTP API
Fetches current spot price for NIFTY index.

**Request:**
```json
{
  "exchange": "NSE",
  "symboltoken": "99926000",
  "tradingsymbol": "NIFTY 50"
}
```

**Response:**
```json
{
  "data": {
    "ltp": 23850.00
  }
}
```

### 2. OptionGreek API
Fetches complete option chain with prices and Greeks.

**Request:**
```json
{
  "name": "NIFTY",
  "expirydate": "17MAR2026"
}
```

**Response:**
```json
{
  "data": [
    {
      "symbolToken": "57792",
      "tradingSymbol": "NIFTY17MAR2623900CE",
      "strikePrice": 23900,
      "optionType": "CE",
      "ltp": 225.75,
      "delta": 0.4567,
      "theta": -0.0423,
      "vega": 0.1156,
      "impliedVolatility": 0.1789,
      "tradeVolume": 12500,
      "openInterest": 15000
    }
  ]
}
```

## Configuration

### Environment Variables

```bash
# Angel One credentials (required)
ANGEL_API_KEY=your_api_key
ANGEL_CLIENT_CODE=your_client_code
ANGEL_PASSWORD=your_password
ANGEL_TOTP_SECRET=your_totp_secret

# Strike selection mode
STRAT_STRIKE_MODE=OTM1

# Premium range filter (in paise)
STRAT_MIN_PREMIUM=20000  # ₹200
STRAT_MAX_PREMIUM=25000  # ₹250
```

## Use Cases

### Use Case 1: Find OTM Strikes for Short Strategy

```bash
# Run test to see which OTM strikes have ₹200-250 premiums
go run ./cmd/testfnoprice
```

Look for strikes marked with ⭐ in the output.

### Use Case 2: Check Current Market Premiums

```bash
# See live premiums for all strikes
go run ./cmd/testfnoprice
```

Review the complete strike chain section.

### Use Case 3: Verify Strike Selection Logic

```bash
# Test if OTM1/OTM2 selection works with live data
go run ./cmd/testfnoprice
```

Check if selected strikes match expected OTM levels.

## Troubleshooting

### Error: "Login failed"

**Solution:** Check your Angel One credentials in `.env`

```bash
# Verify credentials
echo $ANGEL_API_KEY
echo $ANGEL_CLIENT_CODE
```

### Error: "Failed to load option chain"

**Solution:** Check if market is open and Angel One API is accessible

```bash
# Test Angel One connectivity
curl -X POST https://apiconnect.angelbroking.com/rest/auth/angelbroking/user/v1/loginByPassword
```

### No strikes in ₹200-250 range

**Solution:** Adjust premium range based on current market volatility

```bash
# Widen range
export STRAT_MIN_PREMIUM=15000  # ₹150
export STRAT_MAX_PREMIUM=30000  # ₹300
```

## Benefits

✅ **Real Market Data** - No mock data, all prices from Angel One
✅ **Live Greeks** - Delta, Theta, Vega, IV updated in real-time
✅ **Accurate Selection** - OTM strikes based on actual market conditions
✅ **Premium Filtering** - Find strikes in your target premium range
✅ **Complete Visibility** - See all available strikes with live prices

## Next Steps

1. **Run the test** to see live FNO prices
2. **Review the output** to understand strike selection
3. **Adjust premium range** based on market conditions
4. **Integrate with your strategy** using the selected tokens

## Summary

This test demonstrates how the system:
- ✅ Fetches live NIFTY spot price from Angel One
- ✅ Loads complete option chain with OptionGreek API
- ✅ Selects OTM strikes with premium filtering
- ✅ Returns live prices, Greeks, and tokens
- ✅ Works with real market data (no mocks)

**Your "sour system" now uses live Angel One data to pick FNO prices!** 📊🎯
