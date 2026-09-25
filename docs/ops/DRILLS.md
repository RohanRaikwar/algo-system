# Fault Drills

Drills come in two kinds:

- **Automated drills** are Go tests behind the `drills` build tag, in `backend/internal/drills/`.
- **Manual drills** are for faults that cannot be injected from outside a production package without adding a hook. They are run by hand in staging.

The expected behaviour in each drill is taken from the current code. Where
the code has a known gap, the drill pins today's behaviour and says so.

```bash
cd backend
go test -tags drills -v ./internal/drills/          # needs redis-server on PATH
```

Each automated drill starts its own throwaway `redis-server` on a free port,
with `--save '' --appendonly no`. It never touches `REDIS_ADDR`. `miniredis`
is not in `go.mod` and no dependency was added: a real Redis process is needed
anyway to test SIGSTOP stalls and real restarts. If `redis-server` is missing,
the drills skip.

---

## Automated

### R1: Redis killed and stalled mid-stream (`TestDrill_RedisKillAndStall_HotPathStaysBounded`)

**Inject:**

- `SIGSTOP` on redis-server for 2 s: sockets stay open and nothing replies.
- Then `SIGKILL`, so connections are refused.
- Meanwhile, drive `Writer.PublishTick` at about 50 Hz and 1s candles through `Writer.Run`.

**Expected:**

- Every `PublishTick` returns in ≤ 200 ms (`tickPublishTimeout`) plus slack, whether Redis is stalled or dead. The mdengine tick router calls it inline, so tickCh cannot back up behind Redis.
- Each failure reaches `OnWriteError`:
  - `op="tick"` for ticks;
  - `op="candle_1s"` for 1s candle pipelines (200 ms `candleWriteTimeout`).
- Once Redis is back, the **same** client recovers with no process restart (go-redis reconnects).

**Verify in a real run:**

- `mdengine_redis_write_failures_total{op}` rises.
- `mdengine_redis_write_duration_seconds` p99 goes to 0.2 s.
- `mdengine_pipeline_drops_total{stage="ws_tick"|"agg_tick"}` stays flat.
- The log shows `[redis] pipeline error for NSE:… context deadline exceeded`, or `connection refused`.

**Last run:** stalled, 10 of 10 calls failed, slowest 201 ms. Killed, 10 of 10 failed, slowest 122 ms (dial). Recovered.

**Not covered** (no deadline in the code, so no bound to assert):

- `writeTFCandle`, `RunFormingTFCandles` and the indicator writes. They block for up to go-redis's default 3 s read timeout on a stall.
- redisTFCandleCh (50000 final TF candles) and redisFormingCh (5000) absorb that, and overflow is counted in `mdengine_pipeline_drops_total{stage="tf_redis"|"tf_forming"}`. The TF router never blocks on a sink, so a Redis stall does not delay SQLite (`tf_sqlite` stays flat) and final TF candles are never dropped at the TF builder (`mdengine_tf_emit_drops_total{kind="final"}` stays 0).

### R2: Redis restarted without persistence (`TestDrill_RedisRestart_ConsumerGroupLost`)

**Inject:**

1. Start a stream consumer: `Reader.EnsureConsumerGroup` + `ConsumeTFCandles`, as indengine, stratengine and analyst do at startup.
2. Restart Redis with no persistence.
3. Keep writing TF candles with `Writer.RunTFCandles`.

**Expected:**

- XADD recreates the stream, but the consumer group is gone, so `XREADGROUP` returns `NOGROUP`.
- The reader logs `🚨 consumer group "…" missing — recreating at $` and recreates the group itself. No service restart is needed.
- Consumption resumes within about 1s. Candles written between the restart and the recreation are skipped by design, because replaying from the start would feed old candles to strategies.

**Verify in a real run:**

- A single `consumer group … missing — recreating` line per consumer, not a flood of `NOGROUP`.
- Signals and dashboard indicators continue after at most one missed candle.

The same restart also loses:

- `snapshot:stratengine` (executor positions);
- `trading:config` (the dashboard kill switch);
- `pnl:summary`;
- `orders:live`;
- `market:state`;
- every `*:latest` key.

If Redis instead restarts from an old RDB file (the default Debian `save`
config), the groups come back with a stale last-delivered ID. Consumers then
receive old TF candles again. That path is not drilled here.

### W1: tick WebSocket killed (`TestDrill_WSKill_IngestReconnects`)

**Inject:**

- A local tick server (the same wire format as `cmd/tickserver`, i.e. `model.Tick` JSON) streams at 50 Hz to a `wssim.Ingest`.
- The drill closes every connection, answers 503 for 1.5 s, then accepts again.

**Expected:**

- `OnReconnect` fires on each failed attempt. That feeds `mdengine_ws_reconnects_total`.
- Backoff is exponential from `ReconnectDelay`, capped at `MaxReconnectDelay`. mdengine staging uses 2 s → 30 s; the drill uses 100 → 400 ms.
- No ticks arrive while the server is down.
- Ticks resume on the same tickCh within one backoff step of the source coming back.
- Ticks sent during the outage are lost; there is no feed replay.

**Last run:** 6 reconnect attempts; ticks resumed 410 ms after the source returned.

This covers the **staging** ingest. The production Angel ingest is manual drill M1.

---

## Manual (need hooks, or real broker / feed)

Run these in staging (`./scripts/air_staging.sh`) unless stated otherwise.
Watch the Grafana dashboard (`deploy/grafana/trading.json`, mdengine
`:9091`) and the service logs.

### M1: Angel One WS killed (production feed)

**Why manual:** `smartconnect.RootURI` is a constant, and the `SmartWebSocketV3` used by `ws.Ingest` is unexported. A drill cannot point it at a fake server. The `SmartWebSocketV3` reconnect loop itself is unit-tested in `pkg/smartconnect/websocket_test.go`.

**Procedure:** run during market hours with `STRAT_LIVE_ORDERS=false` or the kill switch on.

1. Find mdengine's socket: `ss -tnp | grep mdengine | grep :443`.
2. Kill it with `sudo ss -K dst <angel-ip> dport = 443`, or drop it for 90 s with `sudo iptables -I OUTPUT -p tcp -d <angel-ip> --dport 443 -j DROP`.
3. Remove the rule.

**Expected:**

- `[ws] connection closed`. With the iptables DROP (half-open socket, nothing reaches us) this comes from the read deadline: `read error: … i/o timeout` about 30 s after the drop, not a hang.
- `mdengine_ws_reconnects_total` +1 per close.
- If no tick and no frame (not even a pong) arrives for 30 s during market hours: `[mdengine] 🚨 STALE FEED: no tick for 30s and socket silent — forcing reconnect/re-login (1/3)`, `mdengine_feed_stale_alerts_total` +1, `/healthz` `feed_stale=true` (503), then `feed lost mid-session — re-login in 5s`. It repeats every 30 s while the DROP rule is in place, up to 3 forced re-logins; then `… no longer forcing` (every 10 min). After recovery: `feed recovered — ticks flowing again`, `feed_stale=false`.
- Reconnect attempts back off 5 s, 10 s, 20 s, 40 s, 60 s, 60 s, …
- A warning `Max retry attempt reached` after 5 attempts, while retrying continues.
- Without the watchdog firing (e.g. market closed), the same socket keeps retrying for up to 5 min. After that, or on an HTTP 401/403 at dial, it returns `ErrFeedLost`. mdengine then logs `feed lost mid-session — re-login in 5s` and `mdengine_session_transitions_total{type="ws_disconnect"}` +1, and it logs in again with a new TOTP.
- After recovery, the per-token seq tracker resets, so no false gap is counted across the reconnect.
- `/healthz` `ws_connected` goes false, then true. `tick_age` grows, then resets.
- stratengine keeps running. Its in-process stop loss sees no ticks while the feed is down; GTTs remain the protection.

**Pass if:** ticks resume within one backoff step after the rule is removed, and 1s candles resume. The seconds with no ticks have no candles, which is correct.

### M2: Broker timeouts on order placement

**Why manual:** the executor's `broker` interface is unexported. Injection is covered by in-package tests:

- `orderexec/reconcile_test.go`, e.g. `TestOrderAttempt_TimeoutButOrderInBook_NoDuplicate`, `TestOrderAttempt_TimeoutAndBookUnavailable_DoesNotRetry`, `TestOrderAttempt_RetryAlsoTimesOutAndNotInBook_StateUnknown`;
- `orderexec/position_state_test.go`, e.g. `TestRealBuy_UnknownState_AdoptedFromBook`;
- `pkg/smartconnect/client_test.go`: a timeout or a non-JSON 5xx becomes `ErrOutcomeUnknown`, and a JSON rejection is definitive.

`go test ./internal/orderexec/ ./pkg/smartconnect/` runs these. An end-to-end drill needs a hook (see "Hooks" below).

**Procedure** (use a paper or test Angel account. `STRAT_LIVE_ORDERS=true` is required for the real order path. Check `PaperTrade` wiring in `stratengine/config.go` before relying on Angel's paper mode):

1. Get a `NIFTY50_FNO` BUY signal to fire. `cmd/teststrategyorder` exists for injecting one; check its flags first.
2. While it is in flight, blackhole the REST host for about 10 s: `sudo iptables -I OUTPUT -p tcp -d <apiconnect.angelone.in IP> --dport 443 -j DROP`.
3. Remove the rule.

**Expected:**

- `placeOrder` times out at 7 s, giving `ErrOutcomeUnknown`.
- Book lookups at 0, 1 and 2 s fail.
- `🚨 ORDER STATE UNKNOWN — BUY … ordertag=<K>/<K>R` (RUNBOOK §1). There is **no** second placement.
- A provisional entry blocks further BUYs.
- An exit signal in this window is **deferred**: log `EXIT for … deferred — BUY still settling`. It is sent automatically once the BUY settles as filled (log `sending deferred EXIT`), or dropped with the BUY if it never filled.
- After the rule is removed, the background poll (every 2 s, for 2 min) finds `<K>` and logs `unknown BUY … settled: FILLED … adopting with GTT protection` (GTTs are then placed), or `… never reached broker — clearing`.
- `orderexec_circuit_breaker_failures` +1 on stratengine's `/metrics` (`:9096`). The stratengine log shows `🔌 Circuit breaker` transitions after 3 failures, and `orderexec_circuit_breaker_state` follows each transition immediately.

**Pass if:**

- the order book shows exactly one order for `<K>` (or `<K>` rejected plus `<K>R`), never two live orders;
- the executor snapshot agrees with the broker;
- no `POSITION MISMATCH` appears within 60 s.

**Variant M2b, exit under timeout:** the same, with an open position and an exit signal. Expected:

- the exit is sent regardless of an open breaker (`ExecuteAlways`);
- `ExitOrderID="tag:<K>"` is set, so no second SELL;
- it settles through `applyExitResult`, or alerts `unknown SELL … never reached broker` (RUNBOOK §6).

### M3: Order-book outage after placement

Blackhole the REST host **after** the order ack: start the iptables DROP right after `🔑 idempotency_key`, with a delay of about 300 ms.

**Expected:**

- `confirmFill` cannot read the book, so `BUY … not confirmed filled yet … following in background`.
- The entry is committed with GTTs placed at signal LTP, if GTT creation gets through; otherwise RUNBOOK §9 fires.
- After 2 min without a readable book: `order book unreadable for 2m0s` (RUNBOOK §7).

### M4: Redis outage with a live stratengine

This is R1/R2 at system level.

1. `sudo systemctl kill -s SIGSTOP redis-server` for 10 s, then `SIGCONT`.
2. Separately: `systemctl kill redis-server` (a crash; systemd restarts it).

**Expected:**

- mdengine keeps ingesting (no drops at `ws_tick` / `agg_tick`) and keeps writing SQLite candles. `mdengine_redis_write_failures_total` rises.
- The gateway logs `PubSub … subscribe failed … retry in` with backoff 0.5 s → 30 s. On resubscribe it bumps the epoch, so clients resync.
- stratengine's tick PubSub reconnects inside go-redis.
- After a **crash with no persistence**, R2 applies: stream consumers recreate their groups on `NOGROUP` and resume without a restart (at most ~1s of candles skipped).
- An explicit `systemctl restart redis-server` does restart them: the units declare `Requires=redis-server.service`.
- The executor snapshot is gone. Follow RUNBOOK §15 step 3 before restarting stratengine.

### M5: Gateway client reconnect and backfill

1. Open the dashboard.
2. Block the browser for 30 s (DevTools → Network → Offline), then go back online.

**Expected:**

- Tick channels are latest-value-only: they are **not** backfilled; the next tick replaces the stale one.
- Candle, indicator and signal channels with a `channel_seq` gap are backfilled via `/api/missed` (at most 4 in flight). If the replay buffer no longer holds the range (`complete=false`), the client takes a `SNAPSHOT`.
- The SNAPSHOT carries the latest orders, P&L and the last 20 signals, so those recover even without backfill.
- While offline, the gateway keeps only the newest `pub:orders` / `pub:pnl` / `pub:strike` / `pub:analyst:*` frame for this client instead of disconnecting it; only a `pub:signal` overflow disconnects.

---

## Hooks that would allow automating M1–M3

These are not added, because they are changes to production packages:

1. **`pkg/smartconnect`:** make the WebSocket URL injectable. `SmartWebSocketV3.url` already exists internally, so it needs an exported setter or a field. Also let `ws.IngestConfig` pass it through. Then M1 becomes an automated drill against a fake Angel binary-frame server.
2. **`internal/orderexec`:** an exported constructor or option that accepts the `broker` interface, e.g. `NewOrderExecutorWithBroker(cfg, b)`, with the interface exported or an exported equivalent. Then M2 and M3 can drive the real `ExecuteSignal` path against a fake broker with injected latency, from `internal/drills`.
3. **`pkg/smartconnect.NewSmartConnect`:** today it performs a public-IP HTTP lookup (`api.ipify.org`) and redirects the global logger to `logs/…/app.log` whenever it is constructed. An option to skip both would let drills build a real client against an `httptest` server.
4. **indengine:** serve `/metrics` (stratengine already does, on `:9096`).
