# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

All Go commands run from `backend/`. The Go module is `trading-systemv1`, so imports are `trading-systemv1/internal/...` even though the code lives under `backend/`.

```bash
# Backend
cd backend
go build ./...
go test ./...
go test ./internal/orderexec/ -run TestName -v      # single test
go test -race ./...
go vet ./...

# Frontend
cd frontend
npm run dev        # vite dev server
npm test           # vitest run
npm run build      # tsc -b && vite build
```

### Running the stack

Both scripts run five services under `air` live-reload and source their own env file. Run from the repo root:

```bash
./scripts/air_staging.sh      # .env.staging, STAGING_MODE=true  — simulated feed
./scripts/air_production.sh   # .env.prod,    STAGING_MODE=false — Angel One live feed
```

Frontend is started separately (`cd frontend && npm run dev`).

Individual services: `cd backend && go run ./cmd/<name>/`. There are ~30 binaries in `backend/cmd/`; five are the running system (below) and the rest are one-off diagnostic tools (`testbalance`, `listorders`, `checkscrip`, `teststrike`, …).

## Architecture

### Five processes, Redis is the only bus

The system is **not** one binary. Five separate processes communicate exclusively through Redis pub/sub and keys. No process holds a pointer into another's memory, so any new cross-service data path is a Redis channel, not a Go interface.

```
Angel One WS ──▶ mdengine ──▶ ticks + 1s candles ──▶ Redis
                                                       │
                          indengine ◀──────────────────┤   (indicator computation)
                          stratengine ◀────────────────┤   (signals, orders, P&L)
                          analyst ◀────────────────────┤   (market state, S/R)
                          api_gateway ◀────────────────┘   (REST + WS to browser)
                                  │
                                  ▼
                            frontend (vite/React)
```

| Service | Role |
|---|---|
| `mdengine` | Angel One WebSocket ingest, tick normalization, 1s OHLC aggregation, fan-out to Redis + SQLite |
| `indengine` | Indicator computation (:9095) |
| `stratengine` | Strategy evaluation, order execution, portfolio and P&L tracking |
| `analyst` | Market-state and support/resistance detection |
| `api_gateway` | REST + WebSocket to the browser (`GATEWAY_ADDR`, default `:9090`) |

`api_gateway` never calls into `stratengine`. State reaches the dashboard by `stratengine` writing a Redis key (for REST) and publishing the same payload (for WS push); the gateway subscribes and fans out. Follow `publishPnLSummary` in `internal/stratengine/lifecycle.go` when adding new dashboard state.

### Redis channels

Channels the gateway forwards to the browser are listed in `dynamicPubSubPatterns` (`internal/gateway/pubsub.go`). Note which are wildcard patterns — those carry a dynamic suffix (token, timeframe):

| Pattern | Published by |
|---|---|
| `pub:tick:*` | store/redis |
| `pub:ind:*` | stratengine |
| `pub:analyst:*` | analyst |
| `pub:signal` | stratengine |
| `pub:orders` | stratengine |
| `pub:pnl` | stratengine |
| `pub:strike` | stratengine |
| `pub:sr` | stratengine |
| `pub:refused` | stratengine |
| `pub:strikesel` | stratengine |

`pub:candle` and `pub:market` are published but are **not** in that list — they are internal-only and never reach the browser. A new channel must be added to `dynamicPubSubPatterns` before the hub will fan it out; the frontend then consumes it via `envelope.channel` switching in `frontend/src/hooks/useWebSocket.ts`.

### Gateway delivery guarantees

`internal/gateway` implements per-channel monotonic sequence numbers (`channel_seq`) for gap detection, per-channel replay ring buffers (`replay_buffer.go`) for backfill, and `SNAPSHOT` for full resync. Clients reconnect frequently, so any new channel should carry a sequence number and fit this replay contract rather than assuming a stable connection.

## Conventions that bite

**Money is `int64` paise, never `float64`.** `1 INR = 100 paise`. This holds across `model.Tick`, candles, P&L, and order prices. Introducing a float intermediate anywhere in this chain is a bug.

**Timestamps have named provenance.** `model.Tick` carries both `EventTS` (exchange-provided canonical time) and `TickTS` (local arrival). Use `Tick.CanonicalTS()`, which prefers `EventTS`. Do not collapse the two.

**Storage is accessed through ports.** `internal/model/ports.go` defines `CandleWriter`, `CandleReader`, etc. Redis and SQLite implementations satisfy them. Business logic depends on the interface, not the concrete store.

**Consumer-defined interfaces.** Packages define narrow interfaces where they consume, rather than importing the producer's concrete type (see `orderexec.PnLTracker` and `SetPnLTracker`). Preserve this when wiring new components — it is what keeps `orderexec` from importing `portfolio`.

## Order safety

Real orders require **all three** conditions in `internal/orderexec/executor.go`:

```go
isRealOrderStrategy := oe.IsRealStrategy(sig.StrategyName)   // STRAT_LIVE_STRATEGIES allowlist
sendReal := isRealOrderStrategy && live                      // live = oe.live && oe.sessionOK && oe.api != nil
```

The allowlist defaults to `NIFTY50_FNO` (which no registered strategy uses), so nothing goes live unless `STRAT_LIVE_STRATEGIES` names a running strategy. Everything else falls through to paper mode with a synthetic `PAPER_*` order ID. `oe.live` comes from `LiveOrders` config and is force-downgraded to `false` if Angel credentials are missing or the session fails to establish. Any change touching this gate changes whether real money moves — treat it accordingly. Automated closers (exitwatch, exit policy) never act on a real strategy (`Service.isRealStrategy`).

Real entries additionally pass, in `ExecuteSignal`: rate limiter, circuit breaker (`cb.Allow()`, so an open breaker recovers; broker business rejections such as margin do not count as failures, see `isBrokerFault`), daily max loss (`STRAT_LIVE_MAX_DAY_LOSS_PAISE`), and lot sizing (qty = `STRAT_LIVE_LOTS` × instrument-master lot size, refused if unknown or above `STRAT_FREEZE_QTY`). `STRAT_QTY` sizes paper trades only. Protection (`STRAT_PROTECTION`, `internal/orderexec/protection.go`): default `sl` places one resting SELL `STOPLOSS_LIMIT` order (`STRAT_GTT_SL_PAISE` below entry) after a fill. Every real exit cancels the stop and confirms it in the order book **before** selling; a stop that already filled closes the position with no SELL, and an unconfirmed cancel holds the exit back (retried) rather than risk selling twice. Failed/partial exits re-arm the stop. `SweepProtection` (called from the reconcile loop in market hours) books filled stops and re-places missing or expired ones (stops are DAY orders). `gtt` keeps the older GTT rules; their target is off by default (`STRAT_GTT_TARGET_BPS=0`) because GTT rules are not OCO.

Execution (`STRAT_EXEC_MODE`, `internal/orderexec/chase.go`): default `chase` sends a marketable LIMIT (quote/LTP ± `STRAT_CHASE_TICKS`), re-priced up to `STRAT_CHASE_REPRICES` times. An unfilled BUY is cancelled (partial fill kept) and never chased more than `STRAT_CHASE_MAX_BPS` above its first price; a SELL ends as MARKET. A BUY with no price is refused. `market` uses `STRAT_FNO_ORDER_TYPE` as before.

Margin (`internal/orderexec/margin.go`): a real BUY is refused when the cached RMS margin (`RefreshMargin`, refreshed by the reconcile loop) is below price × qty × (100+`STRAT_MARGIN_BUFFER_PCT`)/100. An unknown or stale (>3 min) cache lets the BUY through — the broker still checks. No network call on the order path.

Reconcile (`ReconcileAndFix`, every 60s in market hours): when the broker holds **less** of a tracked position on two consecutive passes, the executor books the outside sale at LTP and closes/reduces the position (resizing the stop). Anything else — broker holds more, an unknown position, a short, a `status=false` read — is unsafe: alert and halt new entries (`reconcileHalt`, same effect as the kill switch) until a pass is clean. It never places a trade.

Order-path changes are proven with fake-broker tests (`fakeAPI` in `fakebroker_test.go`), not paper/shadow runs.

Exits must never be blocked. Existing gates (profit cap, position mutual-exclusion) apply to entries only, so an open real position can always be closed.

Every real order is journaled through `internal/oms` (state machine + ledger + SQLite `STRAT_ORDER_JOURNAL_PATH`, default `data/orders.db`, `synchronous=FULL`). A real BUY claims its intent and is journaled `SUBMITTING` before it is sent; if either fails, or the intent was already claimed (a replayed signal), the BUY is not sent. With `STRAT_LIVE_ORDERS=true` a missing journal refuses real entries. Exits are journaled best-effort and always sent. Entry keys are deterministic from `Signal.SignalTS` (stamped by `TFEngine` with the candle close or tick time); exit and retry keys stay unique. The Redis snapshot is still the executor's restore source; the journal is the audit trail and the base for the future standalone OMS.

`internal/orderexec` also holds a sliding-window `RateLimiter` and `internal/circuitbreaker` guards the broker connection. New checks on the order path belong next to these in `ExecuteSignal`, not scattered.

## Staging vs production

`STAGING_MODE` is read in `internal/mdengine/config.go` and changes two behaviors: the market data source (`runStagingSession` with the simulated feed in `internal/marketdata/wssim/` vs `runProductionSession` against Angel One), and `MarketCloseGate` on the aggregator, which is disabled in staging so candles keep forming outside market hours.

Market-hours and holiday logic lives in `internal/markethours` (with a holiday fetcher), not inline in services.

## Note on README.md

`README.md` describes an older layout: it shows `cmd/` and `internal/` at the repo root (they are under `backend/`), lists `internal/execution` (actually `internal/orderexec`), and references `./scripts/run_dev.sh` (does not exist — use the `air_*.sh` scripts). Its env-var table and the tick→aggregator→fan-out description are still accurate. Prefer this file for structure.
