# Dynamic Stoploss - Implementation Steps

## Scope
This document defines **dynamic stoploss behavior** only.  
No code changes are included here.

## Architecture Pattern Alignment
Based on `architecture_patterns.md` guidance:
- Clear structure: separate `entry logic`, `SL update logic`, `SL trigger logic`.
- Logical separation: candle-close updates are separate from live-tick exit checks.
- Consistent naming: use one state model per instrument.
- Proper documentation: define exact timing of updates.

## Dynamic SL Rule

### CALL Position
- Initial SL at entry = **previous completed candle low**.
- On every new minute start, SL becomes the **just-completed candle low**.
- SL should be monotonic for CALL:
  - `newSL = max(oldSL, completedCandleLow)`
- Exit trigger (live tick):
  - if `LTP <= SL` then `EXIT`.

### PUT Position
- Initial SL at entry = **previous completed candle high**.
- On every new minute start, SL becomes the **just-completed candle high**.
- SL should be monotonic for PUT:
  - `newSL = min(oldSL, completedCandleHigh)`
- Exit trigger (live tick):
  - if `LTP >= SL` then `EXIT`.

## Your Example (CALL)

Given:
- `09:59` completed candle low = `50`
- During `10:00` live candle, CALL entry happens.

Flow:
1. Entry at `10:00` live -> initial `SL = 09:59 low = 50`.
2. `10:00` candle completes with low `60`.
3. At `10:01` candle start -> update `SL = max(50, 60) = 60`.
4. `10:01` candle completes with low `70`.
5. At `10:02` candle start -> update `SL = max(60, 70) = 70`.

So yes, as you described: SL becomes `50 -> 60 -> 70`.

## Event Timing Contract

### Candle-Close Events
- Only completed candles can move SL.
- Never use forming candle low/high for SL update.

### Tick Events
- SL hit check runs on each tick.
- If hit, emit exit immediately.

### Order of Operations per Minute
1. Candle `T-1` completes.
2. At candle `T` open, update SL from candle `T-1`.
3. During candle `T` live ticks, monitor for SL hit.

## State Model (Per Instrument)
Store:
- `positionSide`: `NONE | CALL | PUT`
- `inPosition`: bool
- `entryTime`
- `entryPrice`
- `stoplossPrice`
- `lastSLUpdateCandleTS` (dedup protection)

## Edge Cases
- If no previous completed candle exists, block entry or use fallback policy.
- If delayed/out-of-order candle arrives, ignore if `candleTS <= lastSLUpdateCandleTS`.
- If SL update and SL hit happen near same time, apply deterministic ordering:
  - process candle-close update first, then tick checks.

## Implementation Checklist
- [ ] Add SL update function that accepts only completed candle.
- [ ] Add monotonic SL rule (`max` for CALL, `min` for PUT).
- [ ] Add per-instrument dedup timestamp for SL updates.
- [ ] Ensure tick handler checks SL continuously.
- [ ] Add logs: `entry`, `sl_update`, `sl_hit_exit`.
- [ ] Add unit tests for the `09:59 -> 10:02` scenario.

## Test Scenario (Must Pass)
- Entry CALL during `10:00` live.
- 09:59 low=50 -> initial SL=50.
- 10:00 close low=60 -> at 10:01 SL=60.
- 10:01 close low=70 -> at 10:02 SL=70.
- Tick at 69 with SL=70 should exit immediately.
