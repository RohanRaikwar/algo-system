# NIFTY50_FNO Design: EMA6/SMA21 Entry + FNO SL Exit + Instant Reversal

Date: 2026-04-02
Status: Approved for planning
Scope: `backend/internal/strategy/nifty50_fno.go`, `backend/internal/strategy/nifty50_fno_state.go`,
`backend/internal/stratengine/service.go`, related tests

## 1. Objective

Refine `NIFTY50_FNO` behavior to:

1. Use only `EMA6` and `SMA21` for entry direction logic.
2. Stop using `EMA9` in entry and exit decisions (while keeping EMA9 fields for backward compatibility).
3. Use FNO stop-loss logic as the operational exit mechanism:
   - `FNOHardSLPct = 5.0`
   - `FNOTrailSLPct = 3.0`
   - `FNOTrailStartPct = 2.0`
4. Add opposite-side instant reversal when EMA6 crosses SMA21 against the active position.
5. Clean and standardize logs to remove ambiguity/noise.

## 2. In-Scope / Out-of-Scope

### In Scope

- `NIFTY50_FNO` strategy behavior and config defaults.
- Reverse handling for this strategy's signals.
- Log reason/message normalization for this strategy path.
- Unit/integration tests for updated behavior.

### Out of Scope

- Changes to `NIFTY50_FNO_SL`, `NIFTY50_FNO_SL2`, or `NIFTY50_10PTS` behavior.
- Option automation policy changes.
- Broker execution architecture rewrite.

## 3. Current Issues Being Addressed

1. Entry path still depends on EMA9-related conditions and guards.
2. Exit path includes candle-based EMA/candle-pattern exits, which conflicts with desired SL-driven behavior.
3. Opposite cross behavior is not currently modeled as deterministic reverse execution.
4. Logging mixes old/new semantics and is difficult to parse quickly.

## 4. Chosen Design (Recommended Option)

### 4.1 Entry Logic (EMA6/SMA21 only)

`CALL` entry trigger:
- EMA6 crosses above SMA21.

`PUT` entry trigger:
- EMA6 crosses below SMA21.

EMA9 compatibility policy:
- Keep `EMA9` config/state/snapshot fields intact.
- EMA9 remains updatable in state for compatibility and warm restore behavior.
- EMA9 is ignored for entry decisions in `NIFTY50_FNO`.

### 4.2 Exit Logic

Disable candle-driven exits for `NIFTY50_FNO`:
- No EMA6-vs-EMA9 candle exit.
- No 3-candle pattern exit.

Operational exits:
- FNO Hard SL and FNO Trail SL remain primary runtime exits on ticks.
- Defaults updated to `5% / 3% / 2%` (`hard / trail / trail start`).

### 4.3 Opposite Cross Instant Reversal

When in position and opposite EMA6/SMA21 cross appears on closed candle:

- If in `CALL` and EMA6 crosses below SMA21:
  - exit CALL and buy PUT as one logical reverse event.
- If in `PUT` and EMA6 crosses above SMA21:
  - exit PUT and buy CALL as one logical reverse event.

Implementation contract:
- Reverse must execute in ordered sequence: `EXIT` then opposite `BUY`.
- Reverse must not rely on EMA9 conditions.
- Reverse should keep existing strategy and instrument context intact for PnL/journal consistency.

## 5. Signal and Processing Design

### 5.1 Reverse Signal Representation

Add explicit reverse metadata to avoid brittle string parsing:

- Extend `strategy.Signal` with optional `ReverseTo PositionSide`.
- Reverse exit signal structure:
  - `Action = EXIT`
  - `Side = current side`
  - `ReverseTo = opposite side`
  - reason tokenized (`REVERSE_TO_CALL` or `REVERSE_TO_PUT`).

Backward compatibility:
- Existing consumers ignore empty `ReverseTo`.
- JSON payload remains compatible due to optional field.

### 5.2 Reverse Processing in `stratengine` signal loop

For `NIFTY50_FNO` reverse exit signals (`ActionExit` with `ReverseTo != NONE`):

1. Process exit event normally (journal/publish/notify).
2. Immediately generate paired synthetic buy signal (`ActionBuy`, `Side=ReverseTo`).
3. Enforce order at signal-loop level so paired buy is emitted after exit handling for the same strategy/side key.

Failure behavior:
- If exit path cannot be executed, paired buy must not be emitted.
- If running dry-run, reverse completes logically in-order.

## 6. Config and State Changes

### 6.1 Default config updates (`DefaultNifty50FnOConfig`)

- `FNOHardSLPct: 5.0`
- `FNOTrailSLPct: 3.0`
- `FNOTrailStartPct: 2.0`

No removal of EMA9 fields:
- Keep `EMA9Period` and EMA9 snapshots.
- Keep buffer schema unchanged for restore safety.

### 6.2 Logic flags retained

Existing optional gates (VWAP/ADX/time filter/sideways/trend/cooldown) remain available;
only EMA9-based trigger dependencies are removed for this strategy's entry/exit path.

## 7. Logging Standardization

### 7.1 Entry logs

Use consistent form:
- `ENTRY CALL ema6=24782 sma21=24760 close=24795 mode=normal`
- `ENTRY PUT ...`

### 7.2 Exit logs

- `EXIT CALL reason=FNO_HARD_SL ...`
- `EXIT CALL reason=FNO_TRAIL_SL ...`
- `EXIT CALL reason=REVERSE_TO_PUT ema6=24745 sma21=24760`
- symmetric PUT forms.

### 7.3 Reverse lifecycle logs

- `REVERSE START CALL->PUT ...`
- `REVERSE COMPLETE CALL->PUT ...`
- `REVERSE FAIL CALL->PUT cause=<...>`

### 7.4 Noise reduction

Keep repetitive per-candle deferral logs at debug or state-change-only style where possible.

## 8. Test Plan

### 8.1 Strategy unit tests (`nifty50_fno_test.go`)

1. CALL entry on EMA6>SMA21 cross with no EMA9 requirement.
2. PUT entry on EMA6<SMA21 cross with no EMA9 requirement.
3. EMA6/EMA9 candle exit does not fire.
4. 3-candle pattern exit does not fire.
5. Reverse CALL->PUT on opposite cross.
6. Reverse PUT->CALL on opposite cross.

### 8.2 SL tests

1. FNO hard SL exits at 5% loss from FNO entry premium.
2. FNO trailing SL exits at 3% pullback after 2% profit activation.

### 8.3 Signal-loop tests (`stratengine/service_test.go`)

1. Reverse exit produces ordered paired opposite buy.
2. Reverse preserves position keying and market-state continuity.
3. Reverse event logs and published payload include deterministic reason tokens.

### 8.4 Snapshot compatibility tests

- Existing snapshots containing EMA9 state restore without schema break.

## 9. Acceptance Criteria

1. `NIFTY50_FNO` entries are based on EMA6/SMA21 crosses only.
2. `NIFTY50_FNO` no longer exits from EMA9/candle-pattern logic.
3. FNO SL defaults are exactly `5% hard`, `3% trail`, `2% trail-start`.
4. Opposite EMA6/SMA21 cross triggers deterministic reverse (`EXIT` then opposite `BUY`).
5. EMA9 fields remain in config/state/snapshot for compatibility.
6. Logs are standardized and clearly indicate entry/exit/reverse causes.
7. Existing non-target strategies remain behaviorally unchanged.

## 10. Risk Notes and Mitigations

Risk: reverse sequencing can race with async order execution.
Mitigation: reverse flow processed with strict ordering in signal loop and explicit reverse metadata.

Risk: historical analytics parsing old reason strings.
Mitigation: retain stable key tokens (`ENTRY`, `EXIT`, `FNO_HARD_SL`, `FNO_TRAIL_SL`, `REVERSE_TO_*`).

Risk: compatibility regressions due to schema changes.
Mitigation: keep EMA9 fields and make reverse metadata optional.

## 11. Implementation Handoff Summary

Primary files expected:
- `backend/internal/strategy/nifty50_fno.go`
- `backend/internal/strategy/nifty50_fno_state.go`
- `backend/internal/strategy/engine.go` (if `Signal` metadata extended)
- `backend/internal/stratengine/service.go`
- `backend/internal/strategy/nifty50_fno_test.go`
- `backend/internal/stratengine/service_test.go`

This spec is approved to move to implementation planning.
