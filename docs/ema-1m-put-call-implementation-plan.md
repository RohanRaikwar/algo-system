# 1m EMA PUT/CALL Strategy - Brainstormed Design Draft

## 1) Context Reviewed

- Existing document: `docs/ema-1m-put-call-implementation-plan.md`
- Existing strategy engine already supports candle-driven signals and tick-driven stoploss checks.
- Existing architecture routes ticks and candles through `mdengine` and strategy components.
- Current request focuses on **design quality and clarity**, not immediate code changes.

## 2) Understanding Lock (Before Final Design)

### Understanding Summary

- Build a `1m` timeframe EMA-based options strategy using `EMA6`, `EMA9`, `EMA21`.
- Entry and normal exits are evaluated only on **closed 1m candles**.
- Dynamic stoploss must be based on the **second live/closed 1m candle after entry**.
- Stoploss trigger must be evaluated using **live LTP ticks** for faster exits.
- CALL and PUT positions are **mutually exclusive** per instrument.
- Plan must remain high-level and implementation-ready, without direct code behavior changes.
- Design must be replay-safe and duplicate-safe at signal/state level.

### Assumptions

- PUT buy requires: `EMA21` crossing below `EMA9` plus `EMA6 > EMA9`.
- PUT sell requires: `EMA6` crossing below `EMA9`.
- CALL buy requires: `EMA9 > EMA21` and `EMA6 > EMA21`.
- CALL sell requires: `EMA9` crossing below `EMA6`.
- Second-candle stoploss is **locked/fixed** after it is set.
- Position state is managed per `exchange:token`.

### Open Questions

- Should second-candle stoploss remain fixed until exit, or trail after each new candle?
- Should opposite-side entry be blocked at signal time only, or also at execution-ack state?
- Is one active position allowed per symbol only, or globally across all symbols?

### Confirmation Gate

Does this accurately reflect your intent? Please confirm or correct anything before implementation starts.

## 3) Non-Functional Requirements

- Performance: stoploss checks on tick path with low reaction latency.
- Scale: per-symbol isolated state with ordered event handling.
- Reliability: idempotent signals, monotonic candle timestamp gating, replay-safe restore.
- Security: strict event payload validation before strategy evaluation.
- Maintainability: strategy logic separated from transport and broker integration concerns.
- Observability: full reason logging and metrics for entries/exits/blocks/stoploss triggers.

## 4) Design Approaches

### Approach A (Lowest Refactor)

Keep separate PUT and CALL strategy classes and add a shared side-lock.

- Pros: minimal structural change.
- Cons: harder race control when two strategy instances contend for shared state.

### Approach B (Recommended)

Use one combined 1m side-aware strategy module with a single authoritative position state machine.

- Pros: strongest mutual-exclusion correctness, simpler dedup rules, cleaner stoploss handling.
- Cons: moderate refactor from split strategy layout.

### Approach C (Most Extensible)

Add an orchestrator that coordinates multiple sub-strategies plus a centralized risk gate.

- Pros: future extensibility for more strategies.
- Cons: additional complexity now (likely over-engineering for current scope).

Recommendation: **Approach B**.

## 5) Provisional Rule Contract (1m)

All EMA entry/exit checks use closed `TF=60` candles.

### PUT

- Buy PUT:
  - `prevEMA21 >= prevEMA9`
  - `currEMA21 < currEMA9`
  - `currEMA6 > currEMA9`
  - `positionSide == NONE`

- Sell PUT:
  - `prevEMA6 >= prevEMA9`
  - `currEMA6 < currEMA9`
  - `positionSide == PUT`

### CALL

- Buy CALL:
  - `currEMA9 > currEMA21`
  - `currEMA6 > currEMA21`
  - `positionSide == NONE`

- Sell CALL:
  - `prevEMA9 >= prevEMA6`
  - `currEMA9 < currEMA6`
  - `positionSide == CALL`

## 6) Provisional Dynamic Stoploss Contract

### CALL Stoploss

- After CALL entry, count completed 1m candles after entry.
- On candle #2, set `callSL = secondCandleLow`.
- On each tick: if `LTP <= callSL`, trigger CALL exit.

### PUT Stoploss

- After PUT entry, count completed 1m candles after entry.
- On candle #2, set `putSL = secondCandleHigh`.
- On each tick: if `LTP >= putSL`, trigger PUT exit.

### Conflict Handling

- EMA exit and SL exit are both valid.
- First successful transition to `positionSide=NONE` wins.
- Duplicate exits after state transition are dropped and logged.

## 7) High-Level Architecture Shape

- Candle Path:
  - closed 1m candle -> strategy evaluator -> BUY/EXIT intent.
- Tick Path:
  - live tick -> SL evaluator -> immediate EXIT intent if breached.
- State Path:
  - per-symbol state machine (`NONE | CALL | PUT`) + EMA state + SL state.
- Signal Path:
  - intent journal -> risk checks -> execution adapter.

## 8) State Model (Per Instrument)

- `positionSide` (`NONE | CALL | PUT`)
- `entryEMA6`, `entryEMA9`, `entryEMA21` + previous values
- `exitEMA6`, `exitEMA9` + previous values
- `candlesAfterEntry`
- `callSL`, `callSLSet`
- `putSL`, `putSLSet`
- `lastProcessedCloseTS`
- `lastSignalKey` (optional, for stronger idempotency)

## 9) Implementation Plan (After Confirmation)

1. Finalize contract and resolve open questions.
2. Consolidate strategy flow to strict 1m candle rules.
3. Enforce one-side-at-a-time state machine.
4. Implement second-candle stoploss state transitions.
5. Add tick-driven SL exits with dedup/idempotency guards.
6. Extend snapshot/restore with new state fields.
7. Add metrics/logging and run staged rollout.

## 10) Test Plan

- Deterministic EMA sequence tests for each entry/exit rule.
- Mutual-exclusion tests for blocked opposite-side entry.
- Stoploss setup timing tests (before and on second candle).
- Tick-trigger stoploss tests for both CALL and PUT.
- Race tests (EMA exit vs SL exit in same interval).
- Replay/restart tests validating no duplicate signals.
- Load tests for multi-symbol burst traffic.

## 11) Decision Log

- Decided: closed 1m candles for entry and normal exits.
  - Alternatives: mixed timeframe entry/exit.
  - Reason: simpler contract and easier validation.

- Decided: strict one-side-only position state per instrument.
  - Alternatives: independent CALL/PUT legs.
  - Reason: matches requested trading behavior and avoids conflict.

- Decided: second-candle high/low as stoploss anchor.
  - Alternatives: entry-candle anchor or rolling trailing anchor.
  - Reason: matches requested rule and keeps behavior deterministic.

- Decided: prefer single side-aware strategy design.
  - Alternatives: dual strategy classes with shared lock.
  - Reason: lower race risk and clearer ownership of state.
