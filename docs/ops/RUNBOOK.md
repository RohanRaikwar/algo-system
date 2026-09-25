# Operator Runbook

One section per operator alert. Each section is written from the code that
emits the alert, and each names that code. If the code changes, the section
can go stale, so check the reference before acting on anything unusual.

Only `NIFTY50_FNO` sends real orders (`internal/orderexec/executor.go`,
`isRealOrderStrategy`). Every other strategy is paper, so an order alert that
names a different strategy can only be about paper state.

---

## 0. Shared procedures

### 0.1 Where alerts appear

| Channel | What goes there |
|---|---|
| stratengine log | Every `🚨` line. Order alerts go through `OrderExecutor.alertf` (`orderexec/position_state.go`), which logs `<prefix> 🚨 <msg>`. |
| Notifier (`STRAT_NOTIFY_WEBHOOK`) | Everything passed to `alertf`, plus `POSITION MISMATCH`, `DROPPED EXIT` and `order queue full`, all sent as CRITICAL by `Service.operatorAlert` (`stratengine/controls.go`). Sending has a 5 s timeout. A failed send is logged as `alert delivery failed`. With no webhook configured, nothing is sent. |
| mdengine `/metrics` (`METRICS_ADDR`, `:9091` in `.env.*`) | Market-data alerts only (seq gaps, drops, Redis write failures). These are **not** sent to the notifier. |

Where the logs live:

- On a systemd deployment: `journalctl -u trading-stratengine -f`, and likewise `trading-mdengine`, `trading-indengine`, `trading-api-gateway` (see `deploy/deploy-production.sh`).
- Running locally under air: the air terminal.
- Any process that builds a SmartConnect client also appends the std logger to `logs/YYYY-MM-DD/app.log` under its working directory (`pkg/smartconnect/client.go`, `NewSmartConnect`).

### 0.2 Check the broker (source of truth)

- **Order book:** Angel One web or app → Orders. Or run `cd backend && go run ./cmd/listorders` (it reads `.env.prod`). Or call `GET /api/account/orders` on the gateway.
  - Every real order carries `ordertag` = the 16-hex idempotency key logged as `🔑 idempotency_key=<K>`.
  - The single allowed resend carries `<K>R` (`orderexec/reconcile.go`, `retryTag`).
- **Positions:** Angel One → Positions. Check `netqty` for the option symbol. Quantity is `STRAT_QTY` (65 in `.env.prod`).
- **GTT rules:** Angel One → GTT.
  - Each real entry gets a TARGET rule (entry × 1.015) and, when `FNOStopLossPaise > 0`, a STOPLOSS rule (trigger = entry − SL, limit = trigger − 50 paise). See `placeTargetGTT` / `placeStopLossGTT`.

### 0.3 Check what the system thinks

```bash
# Executor state, persisted every STRAT_SNAPSHOT_INTERVAL_S (default 30s)
redis-cli --raw GET snapshot:stratengine | jq '.order_executor'
#   .entry_orders["NIFTY50_FNO|CALL"] → OrderID, ClientOrderID (ordertag), Token, Symbol,
#     Quantity, Price (paise), GttRuleID / GttSLRuleID, Real, Pending (BUY unsettled),
#     ExitOrderID (SELL in flight; "tag:<K>" when ambiguous)
#   .position_inst, .in_call_position / .in_put_position (informational; state is derived from entries)
redis-cli --raw GET orders:live | jq .
redis-cli --raw GET trading:config | jq .           # dashboard kill switch lives here
redis-cli --raw GET market:state                    # open|closed (mdengine)
```

Note: the snapshot's top-level keys are snake_case (`entry_orders`, `position_inst`, `in_call_position`, `in_put_position`). The fields inside each entry are Go field names, because `OrderRecord` has no JSON tags.

### 0.4 Kill switch

The kill switch blocks **new entries only**: BUY signals are dropped in `stratengine.signalLoop`. Exits, the EOD auto-exit (`STRAT_EOD_EXIT_TIME`, default 15:20 IST), and background settlement all keep running. It never closes a position.

| Method | Effect | Persistence |
|---|---|---|
| `STRAT_KILL_SWITCH=true` in the env file, then restart stratengine | A floor that the dashboard cannot lift (`killSwitchActive`) | Survives restarts |
| `redis-cli PUBLISH cmd:config_update '{"killSwitch":true}'` | Immediate. stratengine only honours `killSwitch` from this payload. | **Not** persisted, so it is lost on stratengine restart |
| Dashboard / `POST /api/trading/config` | Immediate. It also `SET`s `trading:config`, which stratengine reloads on start. | Persisted in Redis. Lost if Redis restarts without persistence. |

The POST endpoint validates the whole config (quantity 1–10, target 0.5–10 %, hard SL 5–50 %), so a bare `{"killSwitch":true}` is rejected. Use read-modify-write:

```bash
curl -s localhost:9090/api/trading/config | jq '.killSwitch=true' \
  | curl -s -X POST -H 'Content-Type: application/json' -d @- localhost:9090/api/trading/config
```

To stop entries fast and durably, do both: PUBLISH now, and set the env var before the next restart. Confirm with the stratengine log line `🛑 dashboard kill switch → true (effective=true)`, then `KILL SWITCH: blocked entry signal ...` on the next BUY.

**Flip it whenever you are about to act on the broker by hand.** Leave it on until the executor and the broker agree (see 0.5).

### 0.5 Closing a real position by hand, and what the executor does next

1. Engage the kill switch (0.4).
2. Angel One → Positions → exit the option symbol (`netqty` → 0).
3. Angel One → GTT → cancel **both** paired rules for that symbol. The executor cancels GTTs only when it closes the entry itself (`cancelPairedGTTs`, synchronous, one retry, alert on failure — section 21). A leftover SELL GTT that triggers on a flat position becomes a short sell if the account has margin for it.
4. The executor still holds the entry. From here:
   - The reconcile loop logs `POSITION MISMATCH <sym>: executor=65 broker=0` every 60 s.
   - A new BUY on the same side stays blocked.
   - The next exit signal, or the EOD auto-exit (default 15:20), sends a SELL. The broker should reject it as "no holdings" (`isPositionFlatError`: `AB1018`, `AB1014`, "no holdings", "net quantity", …). The executor then checks the broker's net position: if it is 0, the exit is complete and the entry is cleared; otherwise the entry and GTTs are kept and section 18 fires.
   - **Risk:** if the broker instead *accepts* a SELL on a flat option position, which it can do when margin allows, that SELL opens a short. Do not leave the account margined for option shorts while a stale entry exists.
5. To clear the stale entry at once instead:
   1. Stop stratengine.
   2. Edit `snapshot:stratengine`: in `.order_executor`, delete `entry_orders[<key>]` and `position_inst[<key>]`. (`in_call_position` / `in_put_position` are informational only; CALL/PUT state is derived from the entries on restore.)
   3. `SET` it back, then start stratengine.

   Restore runs before any exit can fire (`lifecycle.go`).

---

## 1. `ORDER STATE UNKNOWN — <BUY|SELL> <sym> ordertag=<K>/<K>R; following order book in background`

**Source:** `executor.go` `settleUnknown`, reached when `orderAttempt.run` (`reconcile.go`) returns `ErrOrderStateUnknown`.

**Meaning:** a placement's outcome was ambiguous (`smartconnect.ErrOutcomeUnknown`: HTTP timeout at 7 s, connection reset, a 5xx with no broker JSON, Angel `AB1004`, or `status:true` without an `orderid`), and the order book could not settle it inline. An order book answering `status:false` counts as unreadable, never as empty. Any other definite rejection (margin, bad params) is **not** resent; only an expired-session rejection is resent after a refresh. Inline settling means:

- book lookups at 0 s, 1 s and 2 s;
- at most one resend, tagged `<K>R`, and only if the book proves the first attempt is absent.

This alert fires in either of two cases:

- the book was unreadable during settling, or before the resend;
- the resend was also ambiguous and neither tag appears in the book.

The system then **does not resend again**. It follows the book in the background by tag: every 2 s, 60 tries, about 2 min (`asyncPollEvery` / `asyncPollTries`). Book reads are spaced at least 1 s apart (`orderBookMinInterval`).

- **BUY:** a provisional entry (`Pending=true`) is recorded. It blocks a second BUY on that key and defers exits for it (section 2). Possible outcomes:
  - `✅ unknown BUY … settled: FILLED … adopting with GTT protection`: the entry is committed and GTTs are placed.
  - `settled: rejected|cancelled … clearing`, or `never reached broker — clearing`: the entry is dropped.
  - Unresolved after about 2 min: section 7 fires.
- **SELL:** the entry is marked `ExitOrderID="tag:<K>"`, so no second SELL is sent. Possible outcomes:
  - `applyExitResult`: complete closes the position; a partial fill goes to section 5; a rejection goes to section 5.
  - Absent from the book: section 6.

**Blast radius:** one position key (`NIFTY50_FNO|CALL` or `|PUT`).

- A BUY may be live at the broker for up to about 2 min **without GTT protection**, because GTTs are placed only when the entry is adopted.
- A SELL may be half done.
- Each ambiguous placement also counts as one failure on the order circuit breaker.

**Check:**

1. Order book: search ordertag `<K>` and `<K>R`. Is there one live order, two, or none?
2. Positions: `netqty` for `<sym>`.
3. stratengine log for the settle outcome line, within about 2 min.
4. Angel API health: are other broker calls also timing out? Look for `CIRCUIT OPEN` and `Circuit breaker: closed → open`.

**Remediate:**

- Wait up to 2 min for the settle line. If it matches the broker, nothing more to do.
- **BUY filled but not adopted** (section 7 fired): the position is unprotected.
  - Either place a stop-loss GTT by hand in Angel One,
  - or close the position by hand (0.5).
- **Two BUYs filled** (`<K>` and `<K>R` both complete): the executor tracks one quantity. Close the extra lot by hand at once. Expect a `POSITION MISMATCH`.
- **SELL:** see sections 5 and 6.

**Kill switch:** yes, if more than one of these fires in a session, or if the broker API is degraded. Entries during a broker incident are how duplicates and unprotected positions happen.

---

## 2. Deferred exit: log `EXIT for <key> deferred — BUY still settling`, alert `deferred EXIT for <key> <sym> cannot be sent: no live broker session — close manually`, and alert `BUY <id> settled (<status>) but its entry is gone — check Angel positions for an untracked <side>`

**Source:** `executor.go` `ExecuteSignal` exit routing (when `entry.Pending`); `position_state.go` `onBuySettled` / `sendDeferredExit`.

**Meaning:** an exit arrived while the BUY was still settling: accepted but not yet confirmed filled, or in unknown state (section 1). The exit is **remembered** (`ExitRequested`), not sent. When the BUY settles:

- filled → it is adopted with GTT protection and the deferred SELL is sent at once, sized to the filled quantity (log `sending deferred EXIT`);
- rejected / cancelled with nothing filled → the entry is dropped; nothing to exit.

The alerts fire only when that can't happen: no live broker session at settle time, or the entry vanished before the BUY settled.

**Blast radius:** a filled position the strategy already considers closed.

**Check:** positions for the symbol; order book for the BUY's `orderid` / ordertag.

**Remediate:** close by hand (0.5) if the broker holds the position; restore the session first if possible so the executor can do it.

**Kill switch:** yes, until the position is flat.

---

## 3. `EXIT for REAL position <key> <sym> but no live broker session — position left OPEN, close manually`

**Source:** `executor.go` `ExecuteSignal`, when `realEntry && !live`. Here `liveSession()` = `live && sessionOK && api != nil`.

**Meaning:** a real entry is tracked, but the executor has no usable broker session. Either the session failed or expired, or `live` was downgraded because credentials were missing or login failed at start. The SELL is not sent, and the signal is consumed.

**Blast radius:** the position stays open. Its GTTs, if they were placed, still protect it at the exchange.

**Check:**

- stratengine log around startup, for `falling back to dry-run`, `SmartConnect login failed` and session-manager refresh errors.
- `GET /api/sessions/health` on the gateway.
- Angel positions and GTTs.

**Remediate:**

1. Close by hand (0.5), or confirm that the GTT stop loss is in place.
2. Fix credentials or the session, then restart stratengine. The restored snapshot keeps the entry, and `ResumeSettlement` resumes any in-flight order.

**Kill switch:** yes. Without a session, no real entry can be sent anyway, but paper entries would diverge from reality.

---

## 4. `EXIT FAILED — <sym> position still OPEN at broker, close manually: <err>`

**Source:** `executor.go` `executeReal`, the default branch after `cb.ExecuteAlways`. Exits bypass the open circuit breaker and the rate limiter; see `exit_gates_test.go`.

**Meaning:** the SELL was definitively refused. The sequence in `orderAttempt.run` was:

1. The first placement was rejected by broker JSON.
2. The session was refreshed.
3. The resend with tag `<K>R` was also rejected.

Alternatively, the session refresh itself failed (`order+refresh failed`). A "position already flat" rejection does **not** land here; that case is a completed exit.

**Blast radius:** one open real position. It is still covered by its GTT target and stop loss, if those were placed.

**Check:**

- The broker rejection text in `<err>`: margin, freeze quantity, instrument not allowed, token/session expired.
- The order book for `<K>` and `<K>R`.
- Positions.

**Remediate:** close by hand (0.5). If the cause is the session, fix it and restart stratengine.

**Kill switch:** yes.

---

## 5. `EXIT <orderID> <rejected|cancelled> by broker (<text>) — <sym> position still OPEN, close manually`, and `EXIT <orderID> only partly filled (<f> of <q>) — <left> of <sym> still OPEN; paired GTTs still sized <q>, close the rest manually`

**Source:** `position_state.go` `applyExitResult`. It is reached from the inline fill check, from background polling of a SELL that was accepted but not complete, from unknown-SELL settlement, and from a restored SELL after a restart.

**Meaning:**

- **Rejected or cancelled:** the SELL was accepted, then refused (for example by RMS). `ExitOrderID` is cleared, so the next exit signal will send a new SELL.
- **Partial:** part of the lot sold. The tracked quantity is reduced to `<left>`, but the **GTT rules still carry the original quantity** (`<q>`).

**Blast radius:**

- Rejected: the full position is still open.
- Partial: `<left>` units are open, and a GTT sized `<q>` can oversell by `<f>` units, which opens a short.

**Check:** order book row for `<orderID>` (status, text, filled qty); positions `netqty`; GTT rules for the symbol.

**Remediate:**

- **Rejected:** close by hand (0.5), or let the next exit signal retry it.
- **Partial:**
  1. Cancel both paired GTTs.
  2. Sell the remaining `<left>` by hand.
  3. If you keep the remainder instead, create new GTTs sized `<left>`.

**Kill switch:** yes.

---

## 6. `unknown SELL <K> never reached broker — <sym> position still OPEN, close manually`, and `restored EXIT for <key> never reached broker — position still OPEN, close manually`

**Source:** `executor.go` `settleUnknown`, the absent callback; `position_state.go` `ResumeSettlement`.

**Meaning:** an ambiguous SELL (or one in flight across a restart) never appeared in the order book in about 2 min. The SELL did not happen. `ExitOrderID` is cleared.

**Blast radius:** the position is open. GTTs still protect it.

**Check:** order book by `<K>` / `<K>R`; positions.

**Remediate:** close by hand (0.5), or leave it to the next exit signal or the EOD exit, which are now allowed to send.

**Kill switch:** yes, if broker calls are still ambiguous (section 1 cause).

---

## 7. `<what>: order book unreadable for 2m0s — check Angel order book manually`, `<what>: order still "<status>" after 2m0s — …`, and `<what>: no broker session to settle order — …`

**Source:** `position_state.go` `pollOrderAsync`. `<what>` is one of:

- `BUY <id>`, `SELL <id>`
- `unknown BUY <K>`, `unknown SELL <K>`
- `restored unknown BUY <K>`, `restored SELL <id>`

**Meaning:** background settlement gave up. State is left as is for an operator:

- a pending BUY stays `Pending`: it blocks entries and refuses exits for the key;
- a pending SELL keeps `ExitOrderID`: no further SELL is sent for the key.

`"<status>"` is a non-terminal Angel status, such as `open`, `trigger pending` or `validation pending`.

**Blast radius:** the position key is frozen until a human acts. For `unknown BUY`, the position may be live **with no GTT**.

**Check:** the order book row. Is it still open? Is it a LIMIT order that is not marketable?

**Remediate:**

- Cancel the stuck order at the broker, or let it fill.
- Then reconcile the executor by restarting stratengine: `ResumeSettlement` re-polls restored `Pending` / `ExitOrderID` entries.
- Or clear the entry as in 0.5 step 5.
- If a BUY is filled and unprotected, place a stop loss by hand or close the position.

**Kill switch:** yes.

---

## 8. Partial BUY fills (no alert)

**Source:** `position_state.go` `filledEntry` / `onBuySettled`.

**Behaviour:** a BUY that ends `cancelled` after filling part (`filledshares` > 0) is treated as a position of the filled quantity. The entry, both GTT rules and the eventual SELL are all sized to that quantity. No operator action is needed. If a partly filled BUY is still `open` after settling (~2 min), section 7 applies.

---

## 9. `GTT STOPLOSS failed for <key>: <err> — position has NO exchange-side stop loss`, and `GTT STOPLOSS skipped for <key>: trigger would be ≤0 …`

**Source:** `executor.go` `placeStopLossGTT`, which runs asynchronously after every committed real entry.

**Meaning:** the exchange-side stop loss is missing:

- **failed:** `GTTCreateRule` returned an error;
- **skipped:** entry − `FNOStopLossPaise` ≤ 0.

The in-process stop-loss check in the strategy (the FNO Fixed SL in `strategy/nifty50_fno.go`) still runs, but it depends on:

- the tick feed,
- Redis,
- stratengine staying up.

**Blast radius:** one open position with no protection outside the process. A feed stall, Redis outage or crash while the price falls means an unbounded loss until someone acts.

**Check:** Angel → GTT for the symbol; `<err>` (session, invalid price, GTT limit); stratengine log for `🛡️ GTT STOPLOSS placed` on a retry (the code does not retry).

**Remediate:**

1. Place the stop-loss GTT by hand in Angel One: SELL, qty = position qty, trigger = entry − SL, limit about 0.50 below the trigger.
2. Check whether `GTT TARGET` (section 10) also failed.
3. If you cannot place it, close the position (0.5).

**Kill switch:** yes, if the failure is systemic (session or API). Every new entry would be unprotected in the same way.

---

## 10. `GTT TARGET failed for <key>: <err> — position has no exchange-side target`

**Source:** `executor.go` `placeTargetGTT`.

**Meaning:** the +1.5 % exchange-side take-profit rule was not created. The system SELL on exit signals still works.

**Blast radius:** lost upside automation only. This is not a loss exposure.

**Remediate:** optionally create it by hand at entry × 1.015. The executor will not know its ID, so cancel it by hand after the position closes.

**Kill switch:** no, unless it comes with section 9.

---

## 11. `POSITION MISMATCH <sym> (<token>): executor=<e> broker=<b> — check broker and close/adjust manually`

**Source:** `stratengine/controls.go` `positionReconcileLoop` → `orderexec/positions.go` `ReconcilePositions`. It runs every 60 s while `markethours.IsMarketOpen` is true and only when the executor is live. It **only reports**; it never trades.

**Meaning:** for some token, the executor's real open quantity (the sum of `Real` entries) differs from the broker's summed `netqty`.

| Pattern | Likely cause |
|---|---|
| `executor=65 broker=0` | A GTT fired (target or SL) and no system SELL has run yet. Or the position was closed by hand. Or an exit settled wrongly. |
| `executor=0 broker=65` | An untracked position: a manual trade, a duplicate BUY (section 1), or an executor snapshot lost on restart. A lost snapshot happens when Redis restarted without persistence before stratengine did. **Exits for it are then handled as paper, so nothing will ever close it.** |
| `executor=65 broker=130` | A duplicate fill (`<K>` and `<K>R`), or a manual add. |
| Negative broker qty | A short position. A GTT or SELL oversold (sections 5 and 8). Treat as urgent. |

**Blast radius:** real money the system is not managing correctly. Untracked or short positions are the worst case.

**Check:**

- Angel positions and order book for the day.
- `snapshot:stratengine` → `.order_executor.entry_orders`.
- stratengine log since the last trade.

**Remediate:**

- Make the broker right first: close untracked or extra quantity, and cover shorts.
- Then make the executor agree with 0.5 steps 4–5.
- The case "GTT booked, executor still open" heals by itself: the next SELL is rejected as flat, which clears the entry. Section 0.5 step 4 has the caveat about margined shorts.

**Kill switch:** yes, until reconcile runs clean (no mismatch line for one 60 s interval).

---

## 12. `[tfengine] 🚨 signal channel full for 5s — DROPPED EXIT <strategy> <action> <side>, position may still be open`

**Source:** `strategy/engine.go` `TFEngine.emit`. It is also sent to the notifier through `OnExitDropped`.

**Meaning:** the strategy produced an exit, but the engine's signal channel stayed full for `exitSendTimeout` (5 s), so the exit was dropped. Entries are dropped immediately when the channel is full, logged as `⚠️ signal channel full — dropped entry`. A full channel means `signalLoop` in stratengine is stuck or very slow.

**Blast radius:** the strategy's in-memory state already says flat, but the executor and broker still hold the position. It will not be exited by this strategy again. The EOD auto-exit covers it: 30 s after the strategies' `ForceExitAll`, it sends an exit for every position the executor still holds, and alerts at cutoff + 5 min if anything is still open. Until then the GTTs are the remaining protection.

**Check:**

- Is stratengine's signal loop stuck? Look at the log timestamps of the last `[stratengine]` signal lines and at goroutine dumps (`SIGQUIT` prints stacks).
- Is the order path blocking? Look for an `order queue full` alert, and broker latency.
- Angel positions.

**Remediate:**

1. Close the position by hand (0.5).
2. Restart stratengine if the loop is wedged.

**Kill switch:** yes, immediately.

---

## 13. Notifier-only: `order queue full for <strategy> — entry dropped; executor may be stuck` / `— exit waiting; executor may be stuck`

**Source:** `stratengine/controls.go` `dispatchOrder`. It has **no log line**; it goes to the notifier only.

**Meaning:** that strategy's order queue, 256 deep, is full. Its worker is stuck in `ExecuteSignal`, for example on broker calls or settle waits. Entries are dropped. Exits block the dispatcher until there is room.

**Blast radius:**

- this strategy's orders are delayed without bound;
- while an exit waits, `signalLoop` for all strategies is blocked behind it. That leads to section 12.

**Check:** broker API latency and errors; a stratengine goroutine dump (`kill -QUIT <pid>`, output in the journal).

**Remediate:** engage the kill switch; restore broker connectivity or restart stratengine; reconcile positions (section 11).

**Kill switch:** yes.

---

## 14. Feed sequence gap: `[ws] feed sequence gap: token=<t> missed=<n> seq=<s>`

**Source:** `marketdata/ws/ingest.go`. The log line is rate-limited to one per 5 s. The metrics count every gap: `mdengine_feed_seq_gaps_total` and `mdengine_feed_seq_missed_total`.

This is production only. The tracker resets on every reconnect (`OnOpen`), and backward jumps and duplicates are not counted.

**Meaning:** Angel's per-token `sequence_number` skipped. Ticks were lost upstream of us, or in the SmartConnect read path. The feed has no replay.

**Blast radius:**

- 1s candles for those seconds are missing or have a wrong OHLC;
- TF candles and indicators computed from them are slightly wrong;
- a stop loss evaluated on ticks may miss a print.

**Check:**

- `increase(mdengine_feed_seq_missed_total[5m])`.
- Is it all tokens (connection or Angel side) or one (thin instrument)?
- WS reconnects around the same time.
- `mdengine_pipeline_drops_total{stage="ws_tick"}`, to rule out our own drops, which are counted separately and are *not* seq gaps.

**Remediate:**

- Isolated small gaps: nothing to do.
- Sustained gaps: restart mdengine. It logs in again and reconnects.
- If gaps line up with strategy entries, review those trades. Use the replay tool (`backend/cmd/replay`) with recorded ticks to see what candles would have been.

**Kill switch:** yes, if sustained (for example more than 50 missed per 5 min on the index token during market hours). Signals are built from these candles.

---

## 15. Redis write failures: `mdengine_redis_write_failures_total{op}` / `[redis] pipeline error for <key>: <err>`

**Source:** `store/redis/writer.go`. Two hot-path writes are bounded at 200 ms and report through `OnWriteError`:

- `PublishTick` (`op="tick"`, metric only, **no log line**);
- the 1s candle pipeline (`op="candle_1s"`: SET latest, XADD `candle:1s:*` MAXLEN 12000, PUBLISH; logged as `pipeline error`).

Related writes that are **not** counted in this metric:

- TF candle writes (`[redis] TF candle pipeline error for …`, no deadline beyond go-redis's default 3 s read timeout);
- forming-candle publishes;
- indicator writes (`indicator batch pipeline error`).

**Meaning:** Redis is down, stalled, or slow. Drills R1 and R2 in `DRILLS.md` show the behaviour:

- The tick router is never blocked for longer than about 200 ms per tick.
- Ticks and candles published during the outage are **lost** for every subscriber: stratengine stop-loss ticks, the gateway, indengine.
- SQLite still gets the 1s and TF candles.
- On recovery, the same clients reconnect by themselves.

**Blast radius:** the whole bus.

- stratengine sees no ticks, so the in-process stop loss is blind and only GTTs protect open positions.
- stratengine sees no TF candles, so no signals.
- The dashboard goes stale.
- If Redis restarted **without persistence**:
  - consumer groups are gone: indengine, stratengine and analyst get `NOGROUP` once, log `consumer group … missing — recreating at $`, and resume on their own (candles from the ~1s gap are skipped);
  - `snapshot:stratengine` and `trading:config` (the dashboard kill switch) are gone.

**Check:**

- `redis-cli PING`, `INFO persistence`, `CONFIG GET save`, `CONFIG GET appendonly`, `INFO memory`.
- `journalctl -u redis-server`.
- `mdengine_redis_write_duration_seconds` p99 (this measures PublishTick).

**Remediate:**

1. Restore Redis.
2. If it restarted empty or from an old RDB, stream consumers recreate their groups on their own: look for one `consumer group … missing — recreating at $` line per consumer (indengine, stratengine, analyst). Restart a consumer only if that line doesn't appear and `NOGROUP` errors keep repeating.
3. Before restarting stratengine after Redis lost data, note the open real positions from Angel. The executor snapshot may be gone or stale. See section 11, row `executor=0 broker=65`.

**Kill switch:** yes, for the duration of any outage longer than a few seconds, and until stratengine is confirmed consuming candles again (log lines for TF candles and signals resume).

### 15a. `[redis] 🔧 migrated legacy auto-ID TF stream <key> -> <key>:legacy:<ms>` (one-time, at mdengine start)

**Meaning:** a `candle:<tf>s:<ex>:<token>` stream still had an auto (write-time) ID at its top, which blocks the explicit `<bucket ms>-0` IDs (retry dedup, gateway `before` pagination). mdengine renames it aside and recreates its consumer groups at `0` on the fresh key, in one transaction, before any writer starts. Idempotent: later starts find explicit IDs and do nothing.

**Blast radius:** one-time loss of historical backfill on the original key. Chart history and indicator warm-up from the stream restart from the first candle written after the migration; SQLite is untouched. The old entries stay on the `:legacy:` key for inspection.

**Check:** `redis-cli --scan --pattern 'candle:*:legacy:*'`. Consumers must not log `NOGROUP` afterwards (their groups are recreated in the same transaction).

**Remediate:** none needed. Delete the `:legacy:` keys once nobody needs them.

### 15b. `mdengine_tf_duplicate_candles_total` / `[redis] TF candle <key> tf=<n> ts=<t> already written with a different payload: skipped`

**Meaning:** a final TF candle arrived for a bucket already in the stream with different contents (a tail-only re-finalisation, or a partial bucket after a restart mid-bucket). The stored candle is kept: the latest key is not overwritten and nothing is published. An occasional increment after a mid-bucket restart is expected; a steady rate means the TF builder is finalising buckets twice, so investigate.

---

## 16. WS feed lost / reconnect storm, and `[mdengine] 🚨 STALE FEED: no tick for <d> …` (`— forcing reconnect/re-login (n/3)`, `but socket alive … — NOT re-logging`, `after 3 forced re-logins — no longer forcing`)

These signals come from `mdengine_ws_reconnects_total`, `mdengine_session_transitions_total{type="ws_disconnect"}`, `mdengine_feed_stale_alerts_total`, and the log lines `[ws] connection closed`, `read error: … i/o timeout`, `[ws] error: code=…`, `[mdengine] 🚨 STALE FEED: …`, `[mdengine] ⚠️ feed lost mid-session — re-login in 5s` and `[mdengine] ✅ feed recovered — ticks flowing again`.

**Source:** `pkg/smartconnect/websocket.go`, `marketdata/ws/ingest.go`, `mdengine/session.go`. The behaviour at the time of writing:

- A half-open socket (no data and no pong) hits the read deadline after 30 s (3 × the 10 s heartbeat), logs `read error: … i/o timeout` and goes through the normal reconnect. Every write (subscribe, resubscribe, heartbeat) has a 10 s deadline, so a wedged peer cannot stall a reconnect.
- The socket reconnects on its own, with backoff 5 s × 2^(n−1), capped at 60 s.
- Stale-feed watchdog: during market hours, if no tick has arrived for 30 s (counted from the open when no tick was seen yet), mdengine increments `mdengine_feed_stale_alerts_total` and sets `/healthz` `feed_stale=true` (status `degraded`, HTTP 503). It fires again every further 30 s while no tick arrives. What it does depends on the socket:
  - Socket silent too (no frame, not even a heartbeat pong, for 30 s): logs `🚨 STALE FEED: … socket silent — forcing reconnect/re-login (n/3)` and ends the session as feed-lost: re-login with a new TOTP after the feed backoff.
  - Socket alive (pongs/frames still arriving) but no ticks — a market-wide halt, an unlisted holiday, or only illiquid tokens subscribed: logs `🚨 STALE FEED: … but socket alive … — NOT re-logging` once, then at most every 10 min. It does not re-login.
  - After 3 consecutive forced re-logins with no tick in between it stops forcing and logs `… after 3 forced re-logins — no longer forcing, needs attention` (every 10 min).
  - The first tick afterwards clears `feed_stale`, resets the count and logs `feed recovered`. Staging (sim feed) alerts only; it does not force a reconnect.
- A connect/start failure on a trading day before 15:30 (including the 9:14–9:15 pre-open window) is treated as feed-lost with backoff (5 s doubling to 60 s); the market-close path (`closed` state, session flush) runs only at/after the close.
- Every successful connect (first connect and every reconnect) sends all stored subscriptions: the configured list plus dynamic `cmd:subscribe_token` tokens, including tokens requested while the socket was down.
- An auth rejection (HTTP 401/403 on dial), or retrying for longer than 5 minutes, surfaces as `ErrFeedLost`. mdengine then logs in again and reconnects after 5 s.
- Candles keep forming from whatever ticks arrive.

**Blast radius:** no ticks means:

- no candles, so no signals;
- no in-process stop loss;
- only GTTs protect open positions.

The dashboard's tick age shows it (`/healthz` `tick_age`, `last_tick_time` and `feed_stale` on the mdengine metrics port; `last_tick_time` is refreshed every second).

**Check:**

- `curl -s localhost:9091/healthz | jq`.
- Angel One status.
- Network.
- Whether the TOTP / session login succeeds (`[mdengine] login failed`).

**Remediate:** if it does not recover within about 2 min during market hours, restart mdengine. `STALE FEED … but socket alive … NOT re-logging` means Angel accepts the socket and answers heartbeats but sends no ticks: check for a market halt or holiday, the subscribed tokens (`[ws] connected, subscribing …`) and Angel status; restart mdengine if ticks should be flowing. `no longer forcing` needs a manual restart once the cause is fixed.

**Kill switch:** yes, while the feed is down during market hours.

---

## 17. Order circuit breaker open: `🔌 Circuit breaker: closed → open`, `🔌 CIRCUIT OPEN: BUY … — broker unreachable, skipping order`

**Source:** `internal/circuitbreaker`, used in `executor.go`. It opens after 3 consecutive failures (`CBMaxFailures`) and half-opens after 30 s (`CBResetTimeout`).

**Meaning:** broker calls keep failing. **Entries** are skipped. Exits still go through, because they use `ExecuteAlways`.

**Blast radius:** missed entries only. Expect sections 1 and 4 for any exits sent during the incident.

**Check:** the broker errors in the preceding `❌ ORDER FAILED` lines.

**Remediate:** restore broker connectivity or the session; it closes again after a successful half-open probe.

**Kill switch:** optional. The breaker already blocks entries, but it reopens every 30 s for a probe entry.

---

## 18. `SELL <sym> rejected as flat but broker still holds <n> — <key> kept OPEN with GTT protection; close manually`, and `… but broker positions unreadable (<err>) — … verify manually`

**Source:** `executor.go` `executeReal`, the "position flat" branch (`isPositionFlatError`: AB1018, AB1014, "no holdings", "net quantity", …).

**Meaning:** the broker refused the SELL with a "flat / net quantity" error, but its net position for the token is not 0 (or could not be read). Before this check, such a rejection closed the position and cancelled its GTTs. Now the entry and both GTTs are kept.

**Blast radius:** a real position the strategy tried to exit is still open.

**Check:** positions `netqty` for the token; whether a GTT already fired partly; the entry's tracked quantity vs `netqty`.

**Remediate:** close the remaining `netqty` by hand (0.5). If quantities disagree, section 11 (POSITION MISMATCH) will also fire within 60 s.

**Kill switch:** yes, until flat.

---

## 19. `GTT TARGET not placed for <key>: entry price unknown (<p>) …` / `GTT STOPLOSS not placed for <key>: entry price unknown (<p>) — position has NO exchange-side stop loss`

**Source:** `executor.go` `placeTargetGTT` / `placeStopLossGTT`.

**Meaning:** the entry filled but neither the broker's average price nor an LTP was known, so no GTT could be priced. GTTs are placed synchronously when the entry is committed; there is no later retry.

**Blast radius:** the position has no exchange-side protection; only the in-process stop loss (needs live ticks) guards it.

**Check:** the fill price in the order book / trade book.

**Remediate:** create the target and stop-loss GTTs by hand in Angel One for the filled quantity, or close the position.

**Kill switch:** optional; engage if you can't add protection immediately.

---

## 20. `pending BUY <key> from a previous day: …` (three variants)

**Source:** `position_state.go` `settleStalePending`, run by `ResumeSettlement` at startup.

**Meaning:** a BUY was still unsettled when the service stopped, and the service restarted on a later day. Angel's order book only covers the current day, so the BUY is settled from **positions** instead:

- `broker holds <n> <sym> — kept OPEN without GTT protection (fill price unknown)` → the entry is kept at `<n>` with no GTTs;
- `broker holds none of <sym> — dropped` → nothing to do;
- `could not be checked (<err>) — kept pending` → it stays pending and blocks entries on that key.

**Remediate:** for "kept OPEN": add GTT protection by hand or close (0.5). For "could not be checked": verify positions, then restart stratengine once the broker is reachable.

**Kill switch:** yes for "kept OPEN" until protected or closed.

---

## 21. `GTT <TARGET|STOPLOSS> rule <id> for <key> <sym> not cancelled (<err>) — cancel it manually in Angel One: if it triggers on a flat position it sells short`

**Source:** `executor.go` `cancelPairedGTTs`, run when an entry is closed or dropped.

**Meaning:** the position is closed in the executor, but the broker refused or didn't answer the cancel of one paired GTT rule, twice. A refused cancel (HTTP 200, `status:false`) now counts as a failure. A rule that already triggered or no longer exists is expected (it may be what closed the position) and is only logged.

**Blast radius:** a live SELL rule on a flat position. If it triggers and the account has margin, it opens a **naked short**.

**Check:** Angel One → GTT for rule `<id>`.

**Remediate:** cancel the rule by hand at once. If it already fired, close the resulting short (0.5).

**Kill switch:** not required; act on the rule.

---

## 22. `<what>: not settled after 2m0s (last status …) — still following it slowly every 30s`, and `<what>: gave up after … — check Angel order book and positions manually`

**Source:** `position_state.go` `pollOrderAsync`.

**Meaning:** a pending BUY, an unconfirmed exit or an unknown-state order did not reach a terminal status in the fast window (60 × 2 s). Settling continues every 30 s for about 4 h (`asyncSlowEvery` / `asyncSlowTries`); the first alert says so. The position stays Pending or keeps its `ExitOrderID` meanwhile, so exits for it are deferred or refused. "gave up" fires only after the slow phase too.

**Check:** order book for the order (`orderid` or ordertag `<K>` / `<K>R`); whether the book endpoint is failing (`order book status=false` / errors in the log).

**Remediate:** if the order is filled or rejected at the broker but the executor still shows it pending, the book read is failing — fix connectivity; settling resumes on its own. After "gave up", reconcile by hand (0.5) and restart stratengine.

**Kill switch:** yes while any position is stuck.
