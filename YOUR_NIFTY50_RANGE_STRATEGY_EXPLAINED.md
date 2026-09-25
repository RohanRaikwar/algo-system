# Your Nifty50 Range Strategy - Complete Study Guide

## 📋 Table of Contents
1. [Strategy Overview](#strategy-overview)
2. [How It Works](#how-it-works)
3. [Configuration Parameters](#configuration-parameters)
4. [Entry Logic](#entry-logic)
5. [Exit Logic](#exit-logic)
6. [Support & Resistance Detection](#support--resistance-detection)
7. [Real Trading Examples](#real-trading-examples)
8. [Optimization Tips](#optimization-tips)

---

## Strategy Overview

### What is This Strategy?

Your **NIFTY50_RANGE** strategy is a **Support/Resistance Range Trading System** that:

✅ **Automatically detects** support and resistance levels from price action  
✅ **Trades within ranges** by buying near support and selling near resistance  
✅ **Exits on breakouts** when the range is violated  
✅ **Uses FNO options** (Calls and Puts) for leveraged returns  

### Core Philosophy

```
📊 Market spends 60-70% of time in ranges
💡 Buy low (support), sell high (resistance)
🎯 Exit when range breaks (trend starts)
```

---

## How It Works

### Step-by-Step Process

#### 1. **Detect Swing Points** (Highs and Lows)
```
The strategy looks at the last 20 candles (configurable)
and identifies swing highs and swing lows:

Swing High: A candle whose high is higher than all
            surrounding candles in the lookback period

Swing Low:  A candle whose low is lower than all
            surrounding candles in the lookback period
```

**Example:**
```
Price Action:
24,050 ← Swing High (highest in 20 candles)
24,020
23,990
23,960
23,930 ← Swing Low (lowest in 20 candles)
23,950
23,980
```

#### 2. **Cluster Swing Points into Levels**
```
Multiple swing highs near each other = RESISTANCE level
Multiple swing lows near each other = SUPPORT level
```

**Clustering Logic:**
- If two swing highs are within 0.15% of each other → Same resistance level
- If two swing lows are within 0.15% of each other → Same support level
- More touches = Stronger level

**Example:**
```
Swing Highs: 24,048, 24,052, 24,050
→ Clustered into: Resistance at 24,050

Swing Lows: 23,928, 23,932, 23,930
→ Clustered into: Support at 23,930
```

#### 3. **Confirm Support/Resistance Levels**
```
A level is CONFIRMED when:
✓ Touched at least 2 times (MinTouchesForLevel = 2)
✓ Not expired (touched within last 50 candles)
✓ Has sufficient strength (based on touches + recency)
```

#### 4. **Detect Active Range**
```
Active Range = Nearest support below + Nearest resistance above

Requirements:
✓ Support exists below current price
✓ Resistance exists above current price
✓ Range size >= 10 points (MinRangeSizePts = 1000 paise)
✓ Both levels confirmed (2+ touches each)
```

**Example:**
```
Current Price: 24,000

Support:    23,930 (below, 3 touches) ✓
Resistance: 24,070 (above, 2 touches) ✓
Range Size: 140 points ✓ (>= 10 points)

→ Active Range CREATED: 23,930 - 24,070
```

#### 5. **Wait for Entry Zone**
```
Don't trade in the middle of the range!
Only trade near the extremes:

CALL Entry Zone: Support to Support + 0.1%
PUT Entry Zone:  Resistance - 0.1% to Resistance
```

**Visual:**
```
24,070 ████████████████████ Resistance
       ↑ PUT Entry Zone (24,046 - 24,070)
       |
24,000 ❌ Don't trade here (middle)
       |
       ↓ CALL Entry Zone (23,930 - 23,953)
23,930 ████████████████████ Support
```

#### 6. **Enter Trade**

**CALL Entry (Near Support):**
```
When:
✓ Price drops to support zone (23,930 - 23,953)
✓ Active range exists and is confirmed
✓ Not in cooldown period

Action:
→ Buy CALL option (ATM or slightly OTM)
→ Target: Resistance level (24,070)
→ Stop: Below support (23,883)
```

**PUT Entry (Near Resistance):**
```
When:
✓ Price rises to resistance zone (24,046 - 24,070)
✓ Active range exists and is confirmed
✓ Not in cooldown period

Action:
→ Buy PUT option (ATM or slightly OTM)
→ Target: Support level (23,930)
→ Stop: Above resistance (24,118)
```

#### 7. **Exit Trade**

**Exit Conditions:**

**A. Target Profit (FNO)**
```
Exit when option premium gains 1.5% (FNOTargetProfitPct)

Example:
Entry: 24,000 CE @ ₹80
Target: ₹81.20 (1.5% gain)
```

**B. Trailing Stop Loss**
```
After 2% profit, trail by 1.5%

Example:
Entry: ₹80
Best: ₹82.40 (3% gain, trailing active)
Exit: ₹80.96 (1.5% drop from best)
```

**C. Hard Stop Loss**
```
Exit if option premium drops 3% (FNOHardSLPct)

Example:
Entry: ₹80
Stop: ₹77.60 (3% loss)
```

**D. Range Breakout**
```
Exit if price breaks range by 0.2%

CALL: Exit if price < Support - 0.2%
PUT:  Exit if price > Resistance + 0.2%
```

---

## Configuration Parameters

### Your Current Settings

```go
// Support/Resistance Detection
SwingLookback:      20    // Look back 20 candles for swings
MinTouchesForLevel: 2     // Need 2 touches to confirm level
TouchTolerancePct:  0.15  // 0.15% tolerance for level touch
LevelExpiryCandles: 50    // Level expires after 50 candles

// Range Detection
MinRangeSizePts:    1000  // Min 10 points (1000 paise = 10 NIFTY points)
MaxRangeSizePts:    50000 // Max 500 points

// Entry Zones
SupportEntryZonePct:    0.1  // Enter CALL within 0.1% above support
ResistanceEntryZonePct: 0.1  // Enter PUT within 0.1% below resistance

// Breakout Detection
BreakoutConfirmPct: 0.2  // 0.2% beyond level confirms breakout

// FNO Targets & Stops
FNOTargetProfitPct: 1.5  // Target 1.5% profit
FNOHardSLPct:       3.0  // Hard stop at 3% loss
FNOTrailSLPct:      1.5  // Trail by 1.5%
FNOTrailStartPct:   2.0  // Start trailing after 2% profit

// Cooldown
CooldownCandles: 0  // No cooldown (can re-enter immediately)
```

### What Each Parameter Does

#### 1. **SwingLookback (20)**
```
How many candles to look back when detecting swing highs/lows

Smaller (10-15): More sensitive, more swings detected
Larger (25-30):  Less sensitive, only major swings
```

#### 2. **MinTouchesForLevel (2)**
```
Minimum touches required to confirm a S/R level

2 touches: More trades, less reliable levels
3-4 touches: Fewer trades, stronger levels
```

#### 3. **TouchTolerancePct (0.15%)**
```
How close prices must be to cluster into same level

0.10%: Tighter clustering, more levels
0.20%: Looser clustering, fewer levels
```

#### 4. **MinRangeSizePts (1000 = 10 points)**
```
Minimum range size to trade

Smaller (500 = 5 pts): More trades, tighter ranges
Larger (2000 = 20 pts): Fewer trades, wider ranges
```

#### 5. **Entry Zone Percentages (0.1%)**
```
How close to S/R you must be to enter

0.05%: Very tight, fewer entries
0.15%: Looser, more entries
```

#### 6. **FNO Targets & Stops**
```
FNOTargetProfitPct: 1.5%  → Quick profits
FNOHardSLPct: 3.0%        → Risk 2x the reward (1.5:3 = 1:2)
FNOTrailSLPct: 1.5%       → Lock in profits
FNOTrailStartPct: 2.0%    → Start trailing after 2% gain
```

---

## Entry Logic

### CALL Entry (Buy Near Support)

**Conditions:**
```go
1. Active range exists and is confirmed
2. Current price <= Support + 0.1%
3. Current price >= Support
4. Not already in a position
5. Not in cooldown period
```

**Example:**
```
Active Range: 23,930 (Support) - 24,070 (Resistance)
Current Price: 23,945

Check:
✓ Range exists: YES
✓ Price in entry zone: 23,945 <= 23,953 (support + 0.1%) ✓
✓ Price above support: 23,945 >= 23,930 ✓
✓ No position: YES
✓ Not in cooldown: YES

→ BUY CALL SIGNAL GENERATED
```

**Signal Details:**
```
Action: BUY
Side: CALL
Token: NSE:99926000 (Nifty 50)
Qty: (configured quantity)
Reason: "RANGE CALL entry: price=2394500 near support=23930.00,
         target=24070.00 (range=0.58%)"
```

### PUT Entry (Buy Near Resistance)

**Conditions:**
```go
1. Active range exists and is confirmed
2. Current price >= Resistance - 0.1%
3. Current price <= Resistance
4. Not already in a position
5. Not in cooldown period
```

**Example:**
```
Active Range: 23,930 (Support) - 24,070 (Resistance)
Current Price: 24,055

Check:
✓ Range exists: YES
✓ Price in entry zone: 24,055 >= 24,046 (resistance - 0.1%) ✓
✓ Price below resistance: 24,055 <= 24,070 ✓
✓ No position: YES
✓ Not in cooldown: YES

→ BUY PUT SIGNAL GENERATED
```

---

## Exit Logic

### 1. **FNO Target Profit Exit**

**Logic:**
```go
if (current_price - entry_price) / entry_price * 100 >= 1.5% {
    EXIT with profit
}
```

**Example:**
```
Entry: 24,000 CE @ ₹80
Current: ₹81.50
Gain: (81.50 - 80) / 80 * 100 = 1.875%

1.875% >= 1.5% → EXIT ✓

Reason: "FNO TARGET PROFIT CALL: price=8150 gained 1.88%
         (target=1.50%)"
```

### 2. **FNO Trailing Stop Loss**

**Logic:**
```go
if best_price >= entry_price * 1.02 {  // 2% profit reached
    if current_price <= best_price * 0.985 {  // 1.5% drop from best
        EXIT with trailing SL
    }
}
```

**Example:**
```
Entry: ₹80
Best: ₹84 (5% gain, trailing active)
Current: ₹82.50

Drop from best: (84 - 82.50) / 84 * 100 = 1.79%
1.79% >= 1.5% → EXIT ✓

Reason: "FNO TRAIL SL CALL: price=8250 fell 1.79%"
```

### 3. **FNO Hard Stop Loss**

**Logic:**
```go
if (entry_price - current_price) / entry_price * 100 >= 3.0% {
    EXIT with loss
}
```

**Example:**
```
Entry: ₹80
Current: ₹77

Loss: (80 - 77) / 80 * 100 = 3.75%
3.75% >= 3.0% → EXIT ✓

Reason: "FNO HARD SL CALL: price=7700 fell 3.75%"
```

### 4. **Range Breakout Exit**

**CALL Breakout (Support Broken):**
```go
if current_price < support * (1 - 0.002) {  // 0.2% below support
    EXIT (range broken)
}
```

**Example:**
```
Support: 23,930
Breakout Level: 23,930 * 0.998 = 23,882
Current: 23,870

23,870 < 23,882 → EXIT ✓

Reason: "RANGE CALL exit: breakout below support
         (price=2387000 < 23882.00)"
```

**PUT Breakout (Resistance Broken):**
```go
if current_price > resistance * (1 + 0.002) {  // 0.2% above resistance
    EXIT (range broken)
}
```

---

## Support & Resistance Detection

### How Levels Are Detected

#### Step 1: Find Swing Points

**Swing High Detection:**
```
For each candle, check if its high is the highest
in the surrounding 20 candles (10 before, 10 after)

Example:
Candles: [24,010, 24,020, 24,050, 24,030, 24,015]
                           ↑
                    Swing High at 24,050
```

**Swing Low Detection:**
```
For each candle, check if its low is the lowest
in the surrounding 20 candles

Example:
Candles: [23,950, 23,940, 23,920, 23,935, 23,945]
                           ↑
                    Swing Low at 23,920
```

#### Step 2: Cluster Swing Points

**Clustering Algorithm:**
```
For each swing high:
    Check if it's within 0.15% of an existing resistance level
    If YES: Add to that level (update average price, increment touches)
    If NO:  Create new resistance level

For each swing low:
    Check if it's within 0.15% of an existing support level
    If YES: Add to that level
    If NO:  Create new support level
```

**Example:**
```
Swing Highs: 24,048, 24,052, 24,050, 24,120

Check 24,048: No existing level → Create Resistance #1 at 24,048
Check 24,052: Within 0.15% of 24,048 → Add to Resistance #1
              → Update to 24,050 (average), 2 touches
Check 24,050: Within 0.15% of 24,050 → Add to Resistance #1
              → Update to 24,050 (average), 3 touches
Check 24,120: NOT within 0.15% of 24,050 → Create Resistance #2 at 24,120

Result:
Resistance #1: 24,050 (3 touches) ✓ CONFIRMED
Resistance #2: 24,120 (1 touch)   ✗ Not confirmed yet
```

#### Step 3: Calculate Level Strength

**Strength Formula:**
```
Touch Score = min(touches / 5, 1.0)
Recency Score = 1.0 - (minutes_since_last_touch / 120)
Strength = Touch Score * 0.6 + Recency Score * 0.4
```

**Example:**
```
Level: 24,050
Touches: 3
Last Touch: 30 minutes ago

Touch Score: 3 / 5 = 0.6
Recency Score: 1.0 - (30 / 120) = 0.75
Strength: 0.6 * 0.6 + 0.75 * 0.4 = 0.66 (Strong level)
```

#### Step 4: Expire Old Levels

**Expiry Logic:**
```
If level not touched in last 50 candles (50 minutes):
    Remove from active levels
```

---

## Real Trading Examples

### Example 1: Successful CALL Trade

**Market Setup:**
```
Time: 10:30 AM
Nifty: 24,000
Active Range: 23,930 (Support) - 24,070 (Resistance)
Range Size: 140 points (0.58%)
```

**Entry:**
```
10:35 AM: Nifty drops to 23,945
→ In CALL entry zone (23,930 - 23,953) ✓
→ BUY 24,000 CE @ ₹85

Entry Details:
- Strike: 24,000 CE
- Premium: ₹85
- Target: ₹86.28 (1.5% profit)
- Stop: ₹82.45 (3% loss)
```

**Trade Progress:**
```
10:40 AM: Nifty 23,960, Premium ₹86
10:50 AM: Nifty 23,990, Premium ₹88
11:05 AM: Nifty 24,020, Premium ₹91
11:15 AM: Nifty 24,050, Premium ₹94
```

**Exit:**
```
11:20 AM: Premium reaches ₹94
Gain: (94 - 85) / 85 * 100 = 10.59%
10.59% >= 1.5% → TARGET HIT ✓

EXIT 24,000 CE @ ₹94
Profit: ₹9 per lot (10.59%)
```

**Result:** ✅ **Profit: ₹9 per lot (10.59%)**

---

### Example 2: Successful PUT Trade

**Market Setup:**
```
Time: 2:00 PM
Nifty: 24,000
Active Range: 23,930 (Support) - 24,070 (Resistance)
```

**Entry:**
```
2:15 PM: Nifty rises to 24,060
→ In PUT entry zone (24,046 - 24,070) ✓
→ BUY 24,000 PE @ ₹78

Entry Details:
- Strike: 24,000 PE
- Premium: ₹78
- Target: ₹79.17 (1.5% profit)
- Stop: ₹75.66 (3% loss)
```

**Trade Progress:**
```
2:20 PM: Nifty 24,040, Premium ₹80
2:30 PM: Nifty 24,010, Premium ₹83
2:45 PM: Nifty 23,980, Premium ₹87
```

**Exit:**
```
2:50 PM: Premium reaches ₹87
Gain: (87 - 78) / 78 * 100 = 11.54%
11.54% >= 1.5% → TARGET HIT ✓

EXIT 24,000 PE @ ₹87
Profit: ₹9 per lot (11.54%)
```

**Result:** ✅ **Profit: ₹9 per lot (11.54%)**

---

### Example 3: Breakout Exit (Loss)

**Market Setup:**
```
Time: 11:00 AM
Nifty: 24,000
Active Range: 23,930 (Support) - 24,070 (Resistance)
Position: CALL (entered at 23,945)
Entry Premium: ₹85
```

**Trade Progress:**
```
11:05 AM: Nifty 23,920, Premium ₹83 (support holding)
11:10 AM: Nifty 23,910, Premium ₹81 (support tested)
11:15 AM: Nifty 23,880, Premium ₹78 (support broken!)
```

**Exit:**
```
11:15 AM: Nifty breaks below 23,882 (support - 0.2%)
→ BREAKOUT DETECTED ✓

EXIT 24,000 CE @ ₹78
Loss: (78 - 85) / 85 * 100 = -8.24%

Reason: "RANGE CALL exit: breakout below support"
```

**Result:** ❌ **Loss: ₹7 per lot (-8.24%)**

**Lesson:** Range broke, trend started. Exit quickly to limit loss.

---

### Example 4: Trailing Stop Exit (Profit)

**Market Setup:**
```
Time: 10:00 AM
Position: CALL @ ₹80
```

**Trade Progress:**
```
10:15 AM: Premium ₹82 (2.5% gain, trailing active)
10:30 AM: Premium ₹85 (6.25% gain, best price)
10:45 AM: Premium ₹84 (still above trail)
11:00 AM: Premium ₹83.50 (drop from best = 1.76%)
```

**Exit:**
```
11:00 AM: Drop from best = (85 - 83.50) / 85 = 1.76%
1.76% >= 1.5% → TRAIL SL HIT ✓

EXIT @ ₹83.50
Profit: (83.50 - 80) / 80 * 100 = 4.38%
```

**Result:** ✅ **Profit: ₹3.50 per lot (4.38%)**

**Lesson:** Trailing stop locked in profits when momentum faded.

---

## Optimization Tips

### 1. **Adjust Range Size Requirements**

**Current:** MinRangeSizePts = 1000 (10 points)

**If too many trades (low quality):**
```
Increase to 1500-2000 (15-20 points)
→ Fewer trades, better quality ranges
```

**If too few trades:**
```
Decrease to 500-800 (5-8 points)
→ More trades, tighter ranges
```

### 2. **Tune Entry Zones**

**Current:** 0.1% entry zone

**For tighter entries (better prices):**
```
Decrease to 0.05%
→ Enter closer to S/R, better risk:reward
→ Fewer entries (more selective)
```

**For more entries:**
```
Increase to 0.15-0.20%
→ More entries, slightly worse prices
```

### 3. **Adjust Targets & Stops**

**Current:** 1.5% target, 3% stop (1:2 risk:reward)

**For higher win rate (conservative):**
```
Target: 1.0%
Stop: 2.5%
→ Easier to hit target, smaller losses
```

**For better risk:reward (aggressive):**
```
Target: 2.0%
Stop: 3.0%
→ 1:1.5 risk:reward, harder to hit
```

### 4. **Add Cooldown Period**

**Current:** CooldownCandles = 0 (no cooldown)

**To avoid overtrading:**
```
Set CooldownCandles = 5-10
→ Wait 5-10 minutes after exit before re-entering
→ Prevents revenge trading
```

### 5. **Increase Level Confirmation**

**Current:** MinTouchesForLevel = 2

**For stronger levels (fewer trades):**
```
Increase to 3-4 touches
→ Only trade very strong S/R levels
→ Higher win rate, fewer opportunities
```

### 6. **Optimize Swing Lookback**

**Current:** SwingLookback = 20

**For more sensitive detection:**
```
Decrease to 15
→ Detect more swings, more levels
→ More trades, potentially noisier
```

**For major swings only:**
```
Increase to 25-30
→ Only major swings detected
→ Fewer but stronger levels
```

---

## Performance Metrics to Track

### 1. **Win Rate**
```
Target: 60-70% for range trading
If < 60%: Tighten entry zones or increase min touches
If > 75%: You might be exiting too early (increase target)
```

### 2. **Average Win vs Average Loss**
```
Target: Avg Win >= 1.5x Avg Loss
If lower: Adjust target/stop ratio
```

### 3. **Trades Per Day**
```
Typical: 2-5 trades per day
If > 10: Range size too small or entry zones too wide
If < 1: Range size too large or entry zones too tight
```

### 4. **Breakout Exit Rate**
```
Target: < 20% of exits should be breakouts
If > 30%: Ranges not strong enough (increase min touches)
```

### 5. **Target Hit Rate**
```
Target: 40-50% of trades hit target profit
If < 30%: Target too ambitious or ranges too small
If > 60%: Target too conservative (leave money on table)
```

---

## Quick Reference Card

### Entry Checklist
- [ ] Active range exists
- [ ] Range size >= 10 points
- [ ] Both S/R levels confirmed (2+ touches)
- [ ] Price in entry zone (within 0.1% of S/R)
- [ ] No existing position
- [ ] Not in cooldown

### Exit Checklist
- [ ] Target profit hit (1.5%)
- [ ] Trailing stop hit (1.5% from best after 2% gain)
- [ ] Hard stop hit (3% loss)
- [ ] Range breakout (0.2% beyond level)

### Key Levels to Monitor
- Support level (buy CALL here)
- Resistance level (buy PUT here)
- Breakout levels (exit if crossed)
- Entry zones (0.1% from S/R)

---

## Summary

Your **NIFTY50_RANGE** strategy is a sophisticated range trading system that:

✅ **Automatically detects** support and resistance from swing points  
✅ **Confirms levels** with multiple touches and strength scoring  
✅ **Trades only strong ranges** (>= 10 points, 2+ touches)  
✅ **Enters near extremes** (within 0.1% of S/R)  
✅ **Exits on targets** (1.5% profit) or breakouts  
✅ **Manages risk** with trailing stops and hard stops  

**Best For:**
- Range-bound markets (60-70% of the time)
- Intraday trading (1-4 hour holds)
- Moderate risk tolerance (1:2 risk:reward)

**Not Suitable For:**
- Strong trending markets (use breakout strategy instead)
- Very low volatility (ranges too small)
- News-driven volatile days (ranges break frequently)

**Expected Performance:**
- Win Rate: 60-70%
- Risk:Reward: 1:2 (1.5% target, 3% stop)
- Trades/Day: 2-5
- Avg Hold Time: 1-3 hours

---

## Next Steps

1. **Backtest** the strategy on historical data
2. **Paper trade** for 1-2 weeks to validate
3. **Start small** with 1 lot when going live
4. **Track metrics** (win rate, avg win/loss, trades/day)
5. **Optimize** parameters based on results
6. **Scale up** gradually as confidence builds

Good luck with your range trading! 🎯
