# Test Position Tool

Automated test tool that places a 1-quantity REAL order and auto-sells when price increases by ₹0.30.

## Features

- **Automatic Strike Selection**: Uses current NIFTY index to find ATM strike (no manual token/symbol needed)
- **Minimal Risk**: Only 1 quantity (not full lot size)
- **Real Orders**: Tests actual broker connectivity
- **Auto-Sell**: Monitors price every 2 seconds, sells when +₹0.30 profit reached
- **Timeout Protection**: Auto-sells at market price if target not reached in 10 minutes

## Usage

```bash
# Test CALL option (uses .env.prod automatically)
go run backend/cmd/testposition/main.go CALL

# Test PUT option (uses .env.prod automatically)
go run backend/cmd/testposition/main.go PUT
```

**The tool will:**
1. Load `.env.prod` if it exists (otherwise `.env`)
2. Show a 5-second countdown (press Ctrl+C to cancel)
3. Create a new Angel One session
4. Execute the test with 1 quantity only

## How It Works

1. **Login** to Angel One with your credentials
2. **Fetch NIFTY Index** price (e.g., ₹23,367.00)
3. **Resolve ATM Strike** automatically (e.g., 23,350 or 23,400)
4. **Place BUY Order** for 1 quantity at market price
5. **Monitor Price** every 2 seconds
6. **Auto-Sell** when price increases by ₹0.30
7. **Show P&L** summary

## Environment Variables Required

The tool automatically uses `.env.prod` if available, otherwise falls back to `.env`.

```bash
# Angel One credentials (from .env.prod)
ANGEL_API_KEY=your_api_key
ANGEL_CLIENT_CODE=your_client_code
ANGEL_PASSWORD=your_password
ANGEL_TOTP_SECRET=your_totp_secret

# Optional: NIFTY index token (defaults to 99926000)
NIFTY_INDEX_TOKEN=99926000
```

## ⚠️ Important: WebSocket Compatibility

**Can I run this while WebSocket is running?**

✅ **YES** - Angel One allows multiple sessions:
- Your WebSocket uses one session for live data
- This test tool creates a separate session for orders
- Both can run simultaneously without issues

**Safety measures:**
- Tool uses only 1 quantity (minimal risk)
- 5-second countdown before starting (can cancel with Ctrl+C)
- Clear warnings before placing real orders
- Separate login session (doesn't affect WebSocket)

## Example Output

```
📁 Using environment file: .env.prod

⚠️  IMPORTANT SAFETY NOTICE:
   • This tool creates a NEW Angel One session
   • If your WebSocket is running, you'll have 2 sessions
   • Angel One allows multiple sessions, but be aware
   • The test uses 1 qty only (minimal risk)

   Press Ctrl+C to cancel, or wait 5 seconds to continue...

╔════════════════════════════════════════════════════════╗
║    Test Position: BUY 1 qty → Auto-SELL at +₹0.30     ║
╚════════════════════════════════════════════════════════╝
Side: CALL
Quantity: 1 (minimal risk)
Target Profit: ₹0.30

🔐 Logging in to Angel One...
✅ Login successful: R54100304

📊 Fetching NIFTY index price...
NIFTY Index: ₹23,367.00

🎯 Resolving ATM strike...
✅ resolved ATM=23350: CE=NIFTY17MAR2623350CE (token=57791, lot=65)

� Trading: NIFTY17MAR2623350CE (token: 57791, strike: 23350)
Current LTP: ₹125.50

🟢 Step 1: Placing BUY order (1 quantity)...
⚠️  This will place a REAL order with REAL money!
⚠️  Risk: ~₹125.50 (1 qty × entry price)
✅ BUY order placed

📊 Position opened:
   Entry Price: ₹125.50
   Target Price: ₹125.80 (+₹0.30)

📈 Monitoring price for auto-sell...
   [1/300] Current: ₹125.55 | P&L: +0.05 | Target: ₹125.80
   [2/300] Current: ₹125.65 | P&L: +0.15 | Target: ₹125.80
   [3/300] Current: ₹125.85 | P&L: +0.35 | Target: ₹125.80

🎯 Target reached! Current price ₹125.85 >= Target ₹125.80

🔴 Step 3: Placing SELL order...
✅ SELL order placed

╔════════════════════════════════════════════════════════╗
║                    Test Complete                       ║
╚════════════════════════════════════════════════════════╝
Entry Price:  ₹125.50
Exit Price:   ₹125.85
Price Change: ₹0.35
Quantity:     1
Total P&L:    ₹0.35

✅ Profit: +₹0.35
🎯 Target profit reached!
```

## Risk Analysis

With ₹2,200 in your account:

| Option Price | Max Risk | % of Balance | Safe? |
|--------------|----------|--------------|-------|
| ₹50 | ₹50 | 2.3% | ✅ Very Safe |
| ₹100 | ₹100 | 4.5% | ✅ Safe |
| ₹150 | ₹150 | 6.8% | ✅ Safe |
| ₹200 | ₹200 | 9.1% | ✅ Safe |
| ₹300 | ₹300 | 13.6% | ⚠️ Acceptable |

**Expected Outcome:**
- Target profit: ₹0.30
- Most likely: Small profit or small loss (₹0.30 to ₹1.00)
- Max risk: Option premium × 1 quantity

## Safety Features

- Only 1 quantity traded (minimal risk)
- Clear warnings before placing real orders
- Shows max risk amount before trading
- Timeout protection (won't wait forever)
- Error handling for network failures
- Can cancel with Ctrl+C before order placement

## Troubleshooting

### Error: "Missing Angel One credentials"
Check your `.env` file has all required variables:
```bash
cat .env | grep ANGEL
```

### Error: "Login failed"
- Verify TOTP secret is correct
- Check Angel One account is active
- Ensure API key is valid

### Error: "Failed to resolve ATM strike"
- Check market hours (9:15 AM - 3:30 PM IST)
- Verify NIFTY index token is correct
- Check Angel One API rate limits

### Error: "Failed to get LTP"
- Verify market is open
- Check network connectivity
- Ensure Angel One API is accessible

## Best Practices

1. **Test during market hours** (9:15 AM - 3:30 PM IST)
2. **Check your balance** before running (need at least ₹100-300)
3. **Monitor the output** carefully for any errors
4. **Verify orders** in Angel One app after test
5. **Start with CALL** if unsure (usually cheaper than PUT)

## Next Steps

After successful test:

1. ✅ Verify order in Angel One account
2. ✅ Check P&L matches the tool output
3. ✅ Review logs for any warnings
4. ✅ Test with opposite side (CALL/PUT)
5. ✅ Ready to use main trading system!
