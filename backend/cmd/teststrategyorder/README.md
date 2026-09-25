# Strategy Order Execution Test

## Purpose

This test simulates the **COMPLETE production order execution flow** through the strategy engine:

```
Strategy Signal → Order Executor → Session Manager → Angel One API → Real Order
```

This is exactly how orders are placed in production when `STRAT_LIVE_ORDERS=true`.

## What It Tests

1. ✅ **Session Manager** - Auto-refresh, proactive refresh, circuit breaker
2. ✅ **Strike Picker** - Dynamic ATM strike resolution
3. ✅ **Order Executor** - Using session manager (not creating own session)
4. ✅ **Strategy Signals** - BUY and SELL signal generation
5. ✅ **Complete Flow** - End-to-end order placement
6. ✅ **Timing** - Measures order execution time

## Differences from `testposition`

| Feature | testposition | teststrategyorder |
|---------|-------------|-------------------|
| Session | Creates own | Uses SessionManager |
| Auto-refresh | ❌ No | ✅ Yes |
| Strategy | ❌ No | ✅ Yes |
| Flow | Direct API | Complete production flow |
| Purpose | API testing | Production flow testing |

## Usage

```bash
# From backend directory
cd backend

# Build
go build -o tmp/teststrategyorder ./cmd/teststrategyorder

# Run (will load .env.prod or .env from backend directory)
./tmp/teststrategyorder

# Or run from repo root
cd ..
./backend/tmp/teststrategyorder
```

**Note**: The test loads `.env.prod` (or `.env` as fallback) from the **current working directory**. Make sure you're in the `backend/` directory or the repo root where the `.env` files are located.

## Expected Output

```
═══════════════════════════════════════════════════════
  STRATEGY ORDER EXECUTION TEST
  Testing complete flow: Strategy → OrderExecutor → Angel One
═══════════════════════════════════════════════════════

📋 Product Type: INTRADAY

STEP 1: Creating Session Manager...
[session_manager] 🔑 logging in to Angel One...
[session_manager] ✅ session established at 18:50:23 (took 429ms)
✅ Session manager initialized with auto-refresh

STEP 2: Initializing Strike Picker...
✅ CALL: NIFTY28APR2624600CE (token: 72329)
✅ PUT:  NIFTY28APR2624600PE (token: 72330)

STEP 3: Creating Order Executor...
[test_strategy_order] ✅ Using session manager for order execution (auto-refresh enabled)
[test_strategy_order] 🔴 LIVE ORDER MODE (real money)
✅ Order executor initialized with session manager

STEP 4: Creating Strategy (NIFTY50_FNO)...
✅ Strategy created

STEP 5: Simulating Market Tick...
✅ LTP updated for all instruments

STEP 6: Generating BUY CALL Signal...
⏰ Timestamp: 18:50:25.123
[test_strategy_order] 📊 BUY CALL NIFTY28APR2624600CE @ ₹240.50 (65 qty)
[test_strategy_order] ✅ ORDER PLACED: 042171256958AO
⏱️  BUY order execution time: 78ms

⏳ Waiting 5 seconds before SELL...

STEP 7: Generating SELL CALL Signal...
⏰ Timestamp: 18:50:30.456
[test_strategy_order] 📊 SELL CALL NIFTY28APR2624600CE @ ₹228.80 (65 qty)
[test_strategy_order] ✅ ORDER PLACED: 04215bd847a7AO
⏱️  SELL order execution time: 75ms

═══════════════════════════════════════════════════════
  TEST SUMMARY
═══════════════════════════════════════════════════════
✅ Session Manager: Active with auto-refresh
✅ Strike Picker: Resolved ATM strikes
✅ Order Executor: Using session manager
✅ Strategy: Generated signals
⏱️  BUY execution time: 78ms
⏱️  SELL execution time: 75ms
📋 Product Type: INTRADAY

🎯 Complete strategy order flow tested successfully!
═══════════════════════════════════════════════════════

📊 Session Manager Status:
   Valid: true
   Age: 0.12 minutes
   Total Refreshes: 1
   Circuit Breaker: false

✅ Test completed successfully!
```

## What Gets Tested

### 1. Session Manager Integration
- ✅ Login with TOTP
- ✅ Proactive refresh enabled
- ✅ Session passed to order executor
- ✅ No session expiry during test

### 2. Strike Picker
- ✅ Resolves ATM CALL and PUT strikes
- ✅ Gets correct tokens and symbols
- ✅ Works with session manager

### 3. Order Executor
- ✅ Uses session manager (not creating own session)
- ✅ Receives strategy signals
- ✅ Places orders through Angel One
- ✅ Measures execution time

### 4. Strategy Flow
- ✅ Generates BUY signal
- ✅ Generates SELL signal
- ✅ Signals trigger order execution
- ✅ Complete production flow

## Configuration

Uses `.env.prod` or `.env`:

```bash
# Required
ANGEL_API_KEY=your_key
ANGEL_CLIENT_CODE=your_client_id
ANGEL_PASSWORD=your_password
ANGEL_TOTP_SECRET=your_totp_secret

# Product type
STRAT_FNO_PRODUCT_TYPE=INTRADAY  # or CARRYFORWARD
```

## After Running

**IMPORTANT**: Cancel the test orders!

```bash
# List orders
go run ./cmd/listorders/main.go

# Cancel orders
go run ./cmd/cancelorder/main.go <order_id>
```

## Production Readiness Check

This test verifies:

- ✅ Session manager works with order executor
- ✅ No session expiry during order execution
- ✅ Strategy signals trigger orders correctly
- ✅ Complete flow matches production
- ✅ Execution time is acceptable (<100ms)
- ✅ All components integrated properly

## Comparison with Production

This test uses the **EXACT SAME CODE PATH** as production:

```go
// Production (stratengine)
sessionManager := smartconnect.NewSessionManager(...)
sessionManager.Login()
sessionManager.Start()

executor := orderexec.NewOrderExecutor(orderexec.Config{
    SessionManager: sessionManager,  // ← Same
    ...
})

executor.ExecuteSignal(signal)  // ← Same

// Test (teststrategyorder)
sessionManager := smartconnect.NewSessionManager(...)
sessionManager.Login()
sessionManager.Start()

executor := orderexec.NewOrderExecutor(orderexec.Config{
    SessionManager: sessionManager,  // ← Same
    ...
})

executor.ExecuteSignal(signal)  // ← Same
```

## Success Criteria

✅ Session manager initializes
✅ Strike picker resolves ATM strikes
✅ Order executor uses session manager
✅ BUY order placed successfully
✅ SELL order placed successfully
✅ Execution time < 100ms per order
✅ No session expiry errors
✅ Session health shows valid status

## Next Steps

After successful test:

1. Cancel test orders
2. Enable live orders in production: `STRAT_LIVE_ORDERS=true`
3. Monitor session manager logs
4. Verify orders execute without session expiry
