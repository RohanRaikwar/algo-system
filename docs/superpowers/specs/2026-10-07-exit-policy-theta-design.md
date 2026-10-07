# Unified exit layer (`internal/exitpolicy`) — phase 1: framework + theta exit

## Context

Exits today are scattered: each strategy has its own stop/target/trail code (SR `nifty50_sr.go:768`, Range `nifty50_range.go:688`, Gamma `nifty50_gamma.go:337`, IC `nifty50_range_ic.go:388`), exitwatch injects exits via `cmd:exitwatch:exit` (SR only), stratengine does EOD/stale force-exits (`lifecycle.go:423-544`). No single arbiter, reasons are free text, and there is **no theta/time-decay exit** — an option that sits flat bleeds premium until stop/time exit.

The option picker already budgets theta at entry: `|θ| × HoldMinutes/375 ≤ ThetaMaxGainPct% × |δ| × TargetMove` (`optionpicker/selector.go:127`, `stratengine/sr_strike.go:33`). Nothing enforces that assumption after entry, and the pick's greeks are discarded once the order goes out.

Agreed design (approach A, approved in chat): an in-process exit layer inside stratengine — Rules → per-strategy Plan → per-position Arbiter → existing `SendSignal`/orderexec path. Phase 1 builds the framework and the **Theta rule** first; stop/target/trail migration comes in later phases.

Outcome: flat trades whose premium is being eaten by decay are closed after N minutes or once decay passes X% of the expected gain, with typed reasons and an audit trail. SR acts on it (paper only); Range runs shadow. NIFTY50_FNO never.

## Design

### Package `backend/internal/exitpolicy` (new, no stratengine/strategy imports)

- `types.go`
  - `Reason` typed code: `THETA`, `TIME` (later `STOP`, `TARGET`, `TRAIL`, `BREAKEVEN`, `ADVISORY`, `CUSTOM`).
  - `Priority` order constant table (hard risk > session > stop > target/trail > theta/time > advisory) — only THETA used in phase 1, table in place for later phases.
  - `EntryGreeks{DeltaMilli int64; DecayPerMinPaise int64; ExpGainPaise int64}` — float greeks converted **once** at entry by `NewEntryGreeks(delta, thetaRupeesPerDay float64, targetMovePaise int64)`; rest of the package is int64 paise only.
  - `Position{Key, Strategy, Side, FNOToken, IndexToken, EntryTS, IndexEntry, PremEntry int64, Greeks *EntryGreeks}`.
  - `Snapshot{Now, IndexLTP, PremLTP, PremBest int64, ProtectArmed bool}`.
  - `Decision{Reason, Text string, Shadow bool, Detail map[string]int64}`.
- `rule.go` — `Rule interface { Name() Reason; Evaluate(Position, Snapshot) (Decision, bool) }`.
- `theta.go` — `ThetaRule`:
  - `modeled = DecayPerMinPaise × minutesHeld`
  - `realized = max(0, PremEntry + DeltaMilli×(IndexLTP−IndexEntry)/1000 − PremLTP)` (signed delta handles PUT)
  - `decay = max(modeled, realized)`
  - `flat = (PremBest−PremEntry)×100 < FlatProgressPct × ExpGainPaise && !ProtectArmed`
  - fire when `flat && (minutesHeld ≥ MaxFlatMin || decay×100 ≥ DecayPctOfGain × ExpGainPaise)`, confirmed on `ConfirmMin` consecutive 1m evaluations.
  - Greeks nil (restored position / no pick) → time branch only.
  - Text: `THETA flat 22m decay=₹14.20 (31% of exp gain ₹45.80) realized`.
- `arbiter.go` — `Arbiter` (own mutex; called from candle and tick goroutines):
  - `Open(Position, Plan)`, `Close(key)`, `OnIndexTick`, `OnPremiumTick` (tracks LTP/best), `OnMinute(now) []Decision` (rules evaluated on 1m boundary to avoid tick noise).
  - Latch: once a position decides, no further decisions for it.
  - Plan is frozen at `Open` (reload never changes an open position's rules).
- `config.go` — JSON `backend/config/exitpolicy.json`, per strategy:
  ```json
  { "strategies": {
      "NIFTY50_SR":    { "mode": "act",    "theta": { "max_flat_min": 60, "decay_pct_of_gain": 25, "flat_progress_pct": 30, "confirm_min": 2 } },
      "NIFTY50_RANGE": { "mode": "shadow", "theta": { ... same defaults ... } } } }
  ```
  `mode`: `off|shadow|act`. `LoadConfig` = defaults then file overlay (copy `exitwatch/params.go:114` `LoadParams`). Hard-coded refusal: `model.RealOrderStrategy` can never be `act`.
- `recorder.go` — async SQLite `data/exitpolicy.db`, table `exit_decisions` (ts, strategy, side, fno_token, reason, shadow, acted, text, detail_json). Copy `exitwatch/recorder.go` (buffered chan, 1s batched tx, drop counter).
- `Sink` interface (`Decision` out) so arbiter is testable without Redis/SQLite — pattern of `exitwatch/engine.go:95` `Sink`.

### Capture greeks at entry

- Add `EntryGreeks *exitpolicy.EntryGreeks`-shaped data to the signal path: new field on `strategy.Signal` (`Delta, Theta float64` — raw, set only at strike resolution) **or** keep it in stratengine: `resolveEntryStrike` (`stratengine/option_select.go:86`) returns the pick's Delta/Theta alongside the contract.
  - Picker path: `pickEntry` (`picker_wiring.go:346`) — add `Theta`, `Gamma` to `pickerDecision` (`:207`) and return them.
  - SR old path: `pickSRStrike` (`sr_strike.go:72`) already has `c.Delta`, `c.Theta`.
  - Choice: keep it in stratengine (no change to `strategy.Signal`); signalLoop has both the resolved pick and `sig.TargetMove`.

### Wiring in stratengine

- `service.go` — construct `Arbiter` from config + recorder sink; `Run` starts recorder.
- signalLoop BUY (`service.go:725-745`): after entry maps set → `arbiter.Open(...)` with posKey `strategy|side`, `entryFNOPrices`, index entry (`sig.Price`/LTP), `EntryTS = now`, greeks.
- signalLoop EXIT (`:771-799`): `arbiter.Close(posKey)` — any exit (strategy, exitwatch, EOD) closes the arbiter position → no double exit.
- Ticks: `tickRouterLoop` after `updateLiveOrdersFromTick` (`service.go:476`) → `arbiter.OnIndexTick` / `OnPremiumTick` (cheap, mutex, no alloc).
- Minute: new goroutine `exitPolicyLoop` ticks at each minute boundary → `OnMinute` → for each decision:
  - shadow: record only, log `would exit`.
  - act: generalise `exitRequester` (`exit_request.go:21`) into a shared lookup; call owner's `ExitRequested(side, fnoToken, decision.Text)` → returned signal → `tfEngine.SendSignal`. Owner books P&L and resets (SR `nifty50_sr.go:970`). `nil` (already flat) → record `acted=false`.
- `ProtectArmed`: read from owner — SR `Breakeven`, Range `TrailArmed`. Add small `ExitPolicyState(side) (armed bool, ok bool)` method on SR and Range; arbiter snapshot asks via consumer-defined interface in stratengine.
- Range needs `ExitRequested` (mirror SR's, with token guard, resets via `exit()`).
- Reload: Redis `cmd:exitpolicy:reload` subscription modelled on `configUpdateLoop` (`controls.go:58`); swaps config for **new** positions only.
- Order gate in `orderexec/executor.go` untouched. Exits never blocked.

### Backtest

- `backtest/engine.go`: when option model on and strategy listed in config, open arbiter at entry with greeks from `optionmath.GreeksAt` (same inputs as `premiumTo`), feed `modelMidAt` premium + candle close each 1m bar before `OnTFCandle` (around `:745-787`); on decision call strategy `ExitRequested` and close trade with `ExitReason` = decision text. Shadow mode in backtest = report extra column "theta would-exit" without closing.
- Gives before/after evidence for SR before trusting live numbers.

## Rollout phases (this plan = phase 1)

1. **Phase 1 (this plan):** package, Theta rule, arbiter, recorder, stratengine + backtest wiring. SR `act`, Range `shadow`.
2. Phase 2: port stop/target/trail/breakeven into rules; run shadow vs strategy's own exits, assert parity in audit DB, then flip per strategy and delete strategy exit code.
3. Phase 3: exitwatch → `Advisory` rule; `pub:exit` channel + dashboard panel; EOD through arbiter.
4. NIFTY50_FNO: never in act without report evidence (matches exitwatch rule).

## Critical files

- New: `backend/internal/exitpolicy/{types,rule,theta,arbiter,config,recorder}.go` + tests, `backend/config/exitpolicy.json`
- Modified: `backend/internal/stratengine/service.go`, `exit_request.go`, `picker_wiring.go`, `option_select.go`, `sr_strike.go`, `config.go` (`STRAT_EXITPOLICY_FILE`, `STRAT_EXITPOLICY_DB`), `controls.go` (reload sub)
- Modified: `backend/internal/strategy/nifty50_range.go` (`ExitRequested`, `ExitPolicyState`), `nifty50_sr.go` (`ExitPolicyState`)
- Modified: `backend/internal/backtest/engine.go`
- Spec copy committed to `docs/superpowers/specs/2026-10-07-exit-policy-theta-design.md` as first implementation step.

## Verification

- TDD per unit (`superpowers:test-driven-development`):
  - `theta_test.go` table: flat + time → fire; flat + modeled decay ≥ X% → fire; realized decay (IV crush, index flat, premium down) → fire; trending trade → no fire; protect armed → no fire; nil greeks → time only; PUT sign; confirm_min debounce.
  - `arbiter_test.go`: latch (one decision), Close stops evaluation, plan frozen across reload, real-order strategy refused act.
  - `config_test.go`: shipped JSON matches defaults (copy `exitwatch/engine_test.go:124`).
  - stratengine test: BUY with pick greeks → flat premium ticks → minute tick → SR exit signal reason `THETA …`, SR state reset; Range shadow → recorded, no signal; strategy exit first → arbiter closed, no duplicate.
- `cd backend && go build ./... && go vet ./... && go test -race ./internal/exitpolicy/ ./internal/stratengine/ ./internal/strategy/ ./internal/backtest/`
- Backtest SR over historical.db with theta on vs off (method in memory `nifty50-sr-tuning`); compare P&L, win rate, avg hold, count of THETA exits.
- Staging run (`./scripts/air_staging.sh`): open SR paper position, watch `[exitpolicy]` logs and `data/exitpolicy.db` rows.
