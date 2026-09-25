# AI Agent Handoff — Trading System v1

## Goal

Use this document to onboard another AI agent quickly into the current state of the trading system, the production VM, the strategy logic, and the highest-value next actions.

Repo root:

`/home/agile/Desktop/trading-systemv1`

Current review date:

`2026-03-20`

Production VM reviewed:

`ec2-13-63-139-253.eu-north-1.compute.amazonaws.com`

SSH key in repo:

`deploy/myVM.pem`

Default SSH user:

`ubuntu`

## System Overview

This is a Go-based intraday trading system for NIFTY via Angel One SmartAPI.

Main backend services:

- `mdengine`: market-data ingest and candle building
- `indengine`: indicator engine
- `api_gateway`: REST + WebSocket API
- `stratengine`: main live strategy engine

Frontend:

- Vite/React app served separately

Primary backend code areas:

- `backend/internal/strategy`
- `backend/internal/stratengine`
- `backend/internal/orderexec`
- `backend/internal/gateway`
- `backend/internal/markethours`

## Strategies Present In Repo

Main strategies in current code:

- `NIFTY50_FNO`
- `NIFTY50_FNO_SL`
- `EMA3_HYBRID_FNO`

Important strategy files:

- `backend/internal/strategy/nifty50_fno.go`
- `backend/internal/strategy/nifty50_fno_state.go`
- `backend/internal/strategy/nifty50_fno_sl.go`
- `backend/internal/strategy/nifty50_fno_sl_state.go`
- `backend/internal/strategy/ema3_hybrid_fno.go`
- `backend/internal/strategy/ema3_hybrid_fno_state.go`

## Production VM State Observed

The VM was reachable and running these services during inspection:

- `trading-mdengine.service`
- `trading-indengine.service`
- `trading-api-gateway.service`
- `trading-stratengine.service`
- `trading-stratengine-ind.service`
- `trading-frontend.service`

Important production config observations from `/opt/trading-backend/.env`:

- `STRAT_LIVE_ORDERS=false`
- `STRAT_DYNAMIC_STRIKES=true`
- `STRAT_QTY=1`
- `STRAT_JOURNAL_PATH=data/signals.db`
- static fallback tokens still set:
  - `STRAT_CALL_FNO_TOKEN=57791`
  - `STRAT_PUT_FNO_TOKEN=57790`

## Critical Deployment Drift

The production VM is running a stale service:

- `trading-stratengine-ind.service`

That service points to:

- `/opt/trading-backend/bin/stratengine_ind`

But the current repo no longer contains:

- `backend/cmd/stratengine_ind`

This means:

- the VM still has an old binary from an older deployment
- current repo and production runtime are not aligned
- future debugging gets polluted by stale behavior unless this service is removed

Relevant stale deployment references still existed in repo before cleanup:

- `deploy/setup-vm.sh`
- `deploy/deploy-production.sh`
- `deploy/backend/deploy.sh`

## Important Code Drift Between Local and VM

The VM is behind local code in `backend/internal/stratengine/service.go`.

Notable local-only logic that was missing on the VM:

- live order publication/runtime tracking
- strategy FNO token syncing into strategies
- entry instrument normalization for exit bookkeeping
- live stoploss payload generation for UI
- newer support files like `backend/internal/strategy/live_order_state.go`

Practical symptom:

- Redis key `orders:live` was empty during review
- VM source did not contain the local live-order plumbing

## Verified Runtime Bug

On `2026-03-20`, the VM logs showed:

1. `stratengine` triggered EOD auto-exit at `15:30 IST`
2. then fresh `BUY CALL` signals were still accepted at `15:30:02 IST`

This is wrong. It means delayed signals can reopen positions after the intended end-of-day flattening.

Local fix was added in:

- `backend/internal/stratengine/service.go`

Specifically:

- new entry guard blocks `BUY` signals when `markethours.IsMarketOpen(now)` is false

Supporting test added in:

- `backend/internal/stratengine/service_test.go`

## Strategy Performance Findings From Production Journals

Production signal journals were copied and analyzed from:

- `/opt/trading-backend/data/signals.db`
- `/opt/trading-backend/data/signals_ind.db`

Reviewed window:

- `2026-03-17` through `2026-03-20`

Approx signal-level premium P&L from paired `BUY` / `EXIT` rows:

Main `stratengine` journal:

- `NIFTY50_FNO`: about `+₹113.35`
- `EMA3_HYBRID_FNO`: about `-₹0.60`
- `NIFTY50_FNO_SL`: about `-₹20.90`

Stale `stratengine_ind` journal:

- `NIFTY50_FNO`: about `-₹45.55`
- `EMA_1M_INDENGINE`: about `-₹20.08`

Conclusion:

- best currently deployed strategy is `NIFTY50_FNO` on the main `stratengine`
- `NIFTY50_FNO_SL` underperformed
- `stratengine_ind` should not be trusted as the baseline

## Important Behavior Findings

### 1. Hard-stop exits are the main drag

In the main journal, hard-stop exits were net negative and materially harmful.

### 2. SL variant is not yet the better system

`NIFTY50_FNO_SL` had some good trailing-stop exits, but overall it still lost money in the inspected live window.

### 3. The stale indicator-consumer path has broken-looking pricing

`stratengine_ind` repeatedly logged unrealistic static premium values like:

- `15`
- `25560`

This strongly suggests its option-token / pricing path is stale or unreliable for current use.

## Local Backtest Findings

Important files:

- `backend/results/daily_summary.csv`
- `backend/results/trades.csv`
- `backend/results/backtest_result.json`

Longer historical sample in `backend/results`:

- `124` days
- `2697` trades
- `31.55%` win rate
- total P&L about `-₹716.80`

Short-duration churn is a major problem:

- `563` trades closed within `<=3m`
- average P&L for those trades was about `-₹10.37`

Recent March backtest in `backend/results/backtest_result.json` was also negative:

- profit factor about `0.68`
- net P&L about `-₹28.70`

Conclusion:

- local stop tweaks alone do not prove profitability
- the system still overtrades and gets chopped up

## Local Fixes Already Applied

These repo-side fixes were made during the review:

1. Added market-close guard for fresh entries
2. Added tests covering market-hours edge behavior
3. Removed stale `stratengine_ind` build/deploy references from deploy scripts
4. Added deploy cleanup logic to stop/remove stale `trading-stratengine-ind.service`

Modified files:

- `backend/internal/stratengine/service.go`
- `backend/internal/stratengine/service_test.go`
- `deploy/setup-vm.sh`
- `deploy/deploy-production.sh`
- `deploy/backend/deploy.sh`

## Validation Already Run

Executed successfully:

```bash
gofmt -w backend/internal/stratengine/service.go backend/internal/stratengine/service_test.go
bash -n deploy/setup-vm.sh deploy/deploy-production.sh deploy/backend/deploy.sh
cd backend && go test ./internal/stratengine ./internal/strategy
```

## Highest-Value Next Actions

### Immediate operational fixes

1. Deploy the current repo to the VM.
2. Stop and remove `trading-stratengine-ind.service`.
3. Confirm only these backend services remain:
   - `trading-mdengine`
   - `trading-indengine`
   - `trading-api-gateway`
   - `trading-stratengine`
4. Confirm post-close entries no longer happen after `15:30 IST`.

### Strategy work

1. Treat `NIFTY50_FNO` as the baseline strategy.
2. Keep `NIFTY50_FNO_SL` experimental until it beats the baseline in a larger sample.
3. Rework anti-churn logic before trying more stoploss tuning.
4. Focus on reducing short holding-time losers.

### Research priorities for another AI agent

1. Quantify how many losing trades are caused by immediate re-entry after exits.
2. Test stricter entry filters:
   - larger momentum threshold
   - wider MA21 distance requirement
   - stronger sideways filter
   - minimum hold time before non-emergency exits
3. Compare:
   - pure signal exit
   - hard SL only
   - trail SL only
   - candle-structure exit only
4. Check whether `ReEntryAfterSL` should be disabled or delayed.
5. Verify whether `SkipLastMinutes` should be non-zero for all strategies.

## Recommended Baseline For Further Optimization

Use this as the current baseline unless new evidence beats it:

- run only `NIFTY50_FNO`
- disable stale `stratengine_ind`
- keep `STRAT_LIVE_ORDERS=false` until post-deploy behavior is verified
- validate that live-order UI state works after the local newer `stratengine` code is deployed

## Suggested Prompt For Another AI Agent

Use this prompt directly:

```text
You are auditing and improving a Go-based NIFTY intraday trading system in:
/home/agile/Desktop/trading-systemv1

Read first:
- docs/ai-agent-handoff.md
- backend/internal/strategy/nifty50_fno.go
- backend/internal/strategy/nifty50_fno_state.go
- backend/internal/strategy/nifty50_fno_sl.go
- backend/internal/strategy/nifty50_fno_sl_state.go
- backend/internal/strategy/ema3_hybrid_fno.go
- backend/internal/stratengine/service.go
- deploy/setup-vm.sh
- deploy/deploy-production.sh
- deploy/backend/deploy.sh

Key facts:
- Production VM reviewed on 2026-03-20 was ec2-13-63-139-253.eu-north-1.compute.amazonaws.com
- Main live strategy baseline is NIFTY50_FNO on stratengine
- Stale trading-stratengine-ind.service existed on VM and should be removed
- A post-close re-entry bug was found and fixed locally
- Local backtests are still negative overall, with severe short-duration churn

Your tasks:
1. Verify current repo state and local tests
2. Design the next round of profitability improvements focused on reducing overtrading and bad exits
3. Propose or implement measurable strategy changes with tests/backtests
4. Keep deployment aligned so stale stratengine_ind cannot return

Primary success criteria:
- fewer low-quality short-duration losing trades
- better profit factor than current baseline
- no stale-service drift
- no fresh entries after market close
```

## Notes For The Next Agent

- Be careful with existing uncommitted changes in the repo. The worktree is not clean.
- Do not revert unrelated edits.
- If you query SQLite journals copied locally, prefer the snapshot files if WAL consistency matters.
- If you inspect the VM again, use absolute dates and times in findings.
