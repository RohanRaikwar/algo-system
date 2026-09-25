# Latency Budget, SLOs and Capacity

Every number in this document comes from the code: constants, timeouts,
channel sizes and MAXLENs. Every metric name comes from
`backend/internal/metrics/metrics.go`, the only Prometheus registry. Where a
figure is an estimate rather than a measurement, the text says so.
The dashboard is `deploy/grafana/trading.json`.

## 1. What is actually scraped

| Process | Prometheus `/metrics` | Notes |
|---|---|---|
| mdengine | **yes**, `METRICS_ADDR` (`:9091` in `.env.prod` / `.env.staging`; the code default is `:9090`, which collides with `GATEWAY_ADDR`) | All market-data metrics. |
| indengine | no | Calls `metrics.NewMetrics()` and records `mdengine_indicator_compute_duration_seconds` / `indengine_pel_messages_reclaimed_total` into a registry nobody serves. Its HTTP (`:9095`) has `/reload` and `/healthz` only. |
| stratengine | **yes**, `STRAT_METRICS_ADDR` (default `:9096`) | Own registry (`metrics.NewOrderMetrics`): only the `orderexec_*` circuit-breaker/rate-limiter gauges plus Go/process metrics — no `mdengine_*` series. Gauges refresh on every order, on each breaker transition, and every 10 s. |
| api_gateway | no Prometheus endpoint | Registers its own copy of every metric and reads them into the `/api/metrics` JSON (`FillPipelineMetrics`). Those counters belong to the gateway process, so they are **always 0**: nothing in the gateway increments them. |

Metrics that are registered but never updated anywhere:

- `mdengine_sqlite_commit_duration_seconds`
- `mdengine_ringbuf_overflow_total`
- `mdengine_redis_circuit_breaker_state` / `_trips_total`
- `mdengine_redis_buffered_writes_total` (the store/redis `BufferedWriter` and circuit breaker are not wired into mdengine)
- `mdengine_watermark_delay_seconds`
- `mdengine_reorder_buffer_len`

## 2. Stage budget: exchange tick → fill confirmed

The fixed parts of the pipeline:

- **Tick router:** tickCh (10 000) → `routeTick`, which does two things per tick:
  - a non-blocking send to aggTickCh (10 000);
  - an inline `PublishTick`, with a 200 ms deadline.
- **Aggregator:** event-time watermark = max event second − 1 s (`ReorderBuffer` 300 ms rounds up to 1 s). There is a 100 ms flush ticker.
- **TF builder:** finalises on the next bucket's first 1s candle, or on its 500 ms timer once `now ≥ bucket + TF + FlushGrace (2 s)`.
- **Order path:** TF candle → Redis stream → stratengine `XREADGROUP` (Block 2 s, returns on data) → TFEngine → `signalLoop` → per-strategy order queue → `ExecuteSignal`.

| # | Stage | Mechanism / bound in code | Proposed p99 target | Measured by | Ours? |
|---|---|---|---|---|---|
| 1 | Exchange event → WS frame received | Angel One feed, network. `EventTS` = Angel `exchange_timestamp` (ms). Timestamps more than ±5 s from local time are replaced by receipt time (`maxEventSkew`). | ≤ 500 ms | `mdengine_e2e_latency_seconds` (TickTS − EventTS; buckets 5 ms … 1 s). Despite the name, this is **not** tick-to-browser. | No (exchange, Angel, network, clock sync) |
| 2 | Frame → tick in tickCh | parse + non-blocking send; a full channel means a drop | ≤ 1 ms | Drops only: `mdengine_pipeline_drops_total{stage="ws_tick"}`. **No latency metric.** | Yes |
| 3 | Tick → Redis `pub:tick:*` (stop-loss ticks, dashboard) | `PublishTick`, 200 ms deadline, inline in the router | ≤ 5 ms (hard cap 200 ms) | `mdengine_redis_write_duration_seconds` (observes PublishTick only; default buckets, so resolution below 5 ms is coarse); `mdengine_redis_write_failures_total{op="tick"}` | Yes |
| 4 | Second S closes → 1s candle emitted | Emitted on (a) the first tick of the same token in a later second, or (b) `flushOld` once watermark > S, i.e. any token has ticked in second ≥ S+2, checked every 100 ms. So 0–1.1 s after the bucket ends. | ≤ 1.2 s after bucket end (candle lag ≤ 2.2 s) | `mdengine_candle_lag_seconds` (gauge: now − bucket **start**, so healthy ≈ 1.0–2.1 s). Sampled on emit. It **freezes** when no candles are emitted, so pair it with the tick rate. | Yes (design choice: 1 s watermark) |
| 5 | 1s candle → TF builder | fan-out bus (5000 per subscriber) → `Run1Locked` | ≤ 1 ms | `mdengine_tf_build_duration_seconds` (1 µs … 1 ms buckets); drops `mdengine_fanout_drops_total{subscriber="2"}` | Yes |
| 6 | TF boundary → final TF candle emitted | The next bucket's first 1s candle arrives about 1–2.1 s after the boundary (stage 4); otherwise the timer fires at boundary + 2.0–2.5 s | ≤ 2.5 s after boundary | **No latency metric.** Counts: `mdengine_tf_candles_total{tf}`. | Yes (design choice) |
| 7 | TF candle → Redis stream | `routeTFCandle` → redisTFCandleCh (5000) → `writeTFCandle` (XADD + SET + PUBLISH). **No deadline** other than go-redis's default 3 s read timeout. | ≤ 5 ms | Drops only: `mdengine_pipeline_drops_total{stage="tf_redis"}`. Failures are logged (`TF candle pipeline error`), **not counted**. | Yes |
| 8 | Stream → strategy evaluates candle → signal | `XREADGROUP` → tfCandleCh (5000) → TFEngine | ≤ 50 ms | **None.** | Yes |
| 9 | Signal → order request sent | `signalLoop`: kill switch and market-hours gates, `applyOptionAutomation` for BUYs (may call broker APIs for strike selection), dispatch to the per-strategy queue (256), then rate limiter and circuit breaker | ≤ 100 ms, excluding strike automation | **None.** | Yes, apart from any broker calls in strike automation |
| 10 | Order sent → broker ack (orderid) | Angel REST `placeOrder`; HTTP client timeout 7 s (`smartconnect` `Config.Timeout`). An ambiguous result adds book lookups at 0, 1 and 2 s. | ≤ 1 s | **None.** Only `orderexec_*` circuit-breaker and rate-limiter gauges exist (served by stratengine on `:9096`, section 1); there is no placement-latency histogram yet. | No (Angel) |
| 11 | Ack → fill confirmed | `confirmFill` polls the book after 0.2, 0.3, 0.5, 1 and 1 s. Reads are serialised at ≥ 1 s apart (`orderBookMinInterval`), so the effective inline window is about 5 s. If still not terminal, background polling runs every 2 s for up to 2 min. | ≤ 3 s for MARKET orders | **None.** | No (exchange fill) + our poll cadence |

End to end, from the last tick of a TF bucket to broker ack, as a p99 sum of
the targets:

```
0.5 (feed) + 2.5 (TF close) + 0.005 (XADD) + 0.05 (strategy) + 0.1 (dispatch) + 1.0 (ack)  ≈ 4.2 s
```

The part we control is dominated by candle finalisation: the 1 s watermark
plus the TF flush grace, about 2.5 s of 4.2 s. That is a design trade-off:
out-of-order tolerance and complete candles, bought with latency. It is not a
bottleneck to optimise away without changing candle semantics. Everything
after the XADD is unmeasured today.

### Metric gaps (to close the budget)

The table below lists the metrics that would close the budget, and the hook
each one needs. None exist yet; each needs a change in a production package.

| Missing | Would measure | Hook needed |
|---|---|---|
| `ws_frame_to_tickch_seconds` | stage 2 | `ws.Ingest` / `wssim.Ingest` receipt → send |
| `tf_candle_emit_lag_seconds{tf}` | stage 6 | observe `now − (TS + TF)` in the `tfBuilder.OnTFCandle` callback (mdengine) |
| `redis_write_failures_total{op="candle_tf"}` + deadline | stage 7 | `store/redis` `writeTFCandle` |
| `strategy_signal_latency_seconds` | stages 7–8 | stratengine: `now − (candle.TS + TF)` at signal emit |
| `order_place_duration_seconds{result}` | stage 10 | `orderexec.placeOrder` |
| `order_fill_confirm_seconds{status}` | stage 11 | `orderexec.confirmFill` / `pollOrderAsync` |
| `/metrics` in indengine | indicator timing | stratengine is done (`:9096`); indengine still records into a registry nobody serves |

The gateway has its own in-process `LatencyTracker`, exposed as
`e2e_latency_p50/95/99_ms` in the `/api/metrics` JSON. It parses a top-level
`"ts"` out of every broadcast payload:

- ticks carry `tick_ts`, not `ts`, so they are skipped;
- candles carry the bucket **start**, so a 3600 s candle reports about an hour.

The number is not a meaningful latency. The tracker also runs a full
`json.Unmarshal` on every broadcast message.

## 3. Throughput

### Subscribed instruments (production, `.env.prod`)

- `SUBSCRIBE_TOKENS=1:99926000,2:57791,2:57790`: the NIFTY 50 index plus two NFO options.
- stratengine publishes dynamic CE/PE subscriptions from the strike picker, typically 2 more.
- There is no `CANDLE_TOKENS` in `.env.prod`, so **every** subscribed token gets 1s and TF candles. Staging restricts aggregation to `99926000`.

Working figure: **N = 5 tokens**, **TFs = 60, 120, 180, 300, 3600** (5 TFs), and **indicators = EMA 6, EMA 9, SMA 21** (3 indicators).

### Tick rate

The rate has not been measured here. Measure it with
`sum(rate(mdengine_ticks_total[1m]))`. The planning assumptions are:

| | Per token | × 5 tokens |
|---|---|---|
| typical (Quote mode: index about 1/s, liquid ATM options a few per second) | 3 /s | 15 ticks/s |
| design peak (burst at open or on news) | 20 /s | **100 ticks/s** |

The in-process path is nowhere near its limits at these rates. tickCh and
aggTickCh hold 10 000 each, which is 100 s of buffer at 100/s. The per-tick
cost is dominated by the inline `PublishTick` round trip, which is well under
1 ms to a local Redis.

### Message sizes (JSON, measured on `backend/cmd/replay/testdata`)

| Payload | Bytes |
|---|---|
| Angel WS frame (binary) | 51 B in LTP mode; **123 B** in Quote mode, which the feed now uses for volume (`feedSubscribeMode`, `mdengine/session.go`) |
| `model.Tick` | ≈ 132 in the fixture (`tick_ts` at 10 ms precision); ≈ **140** live (`tick_ts` RFC3339Nano, 9 fractional digits), plus ≈ 20 for `day_volume` on Quote ticks |
| `model.Candle` (1s) | ≈ **145** |
| `model.TFCandle` | ≈ **165** |
| `model.IndicatorResult` | ≈ **130** |
| Gateway envelope overhead | ≈ **135** (`channel` ≈ 22–35, `ts` RFC3339Nano 30, `seq`, `channel_seq`, `epoch` ≈ 20, keys and punctuation ≈ 55). A tick envelope is ≈ 275 B; a candle envelope ≈ 280–300 B. |

### Bus bandwidth at the design peak

| Stream | msgs/s | B/s |
|---|---|---|
| `pub:tick:*` (Redis in) | 100 | 100 × 140 = **14 KB/s** |
| 1s candles: SET + XADD + PUBLISH | 5 | 5 × 145 × 3 ops ≈ 2.2 KB/s |
| forming TF snapshots `pub:candle:{tf}s:*` (one per 1s candle per TF) | 5 × 5 = 25 | 25 × 165 ≈ 4.1 KB/s |
| gateway → each browser (ticks + candles + forming) | ≈ 130 | ≈ 130 × 280 ≈ **36 KB/s per client** |

Redis and the gateway are nowhere near a limit. The binding constraint on
the order side is Angel One's REST rate limits: the order book is read at
most once per second per executor, and the rate limiter allows 10 orders
per 60 s.

## 4. Memory

### Redis streams: bounded by MAXLEN, not by time

Memory = MAXLEN × bytes per entry. Retention = MAXLEN ÷ entry rate. All
trimming is approximate (`MAXLEN ~`), so a stream can exceed its cap by
about one listpack node (≈ 100 entries by default). Bytes per entry ≈
payload + about 25 B (field name `data`, ID delta, listpack overhead).

**1s candle streams** `candle:1s:{ex}:{tok}`, with `stream1sMaxLen = 12000`:

- per token: 12 000 × (145 + 25) = **2.04 MB**
- × 5 tokens = **10.2 MB**
- retention at 1 candle/s = 12 000 s = **3 h 20 min**, so it holds about half a 6 h 15 min session. Seconds with no tick produce no candle, which stretches this on thin options.

**TF candle streams** `candle:{tf}s:{ex}:{tok}`: MAXLEN = max(10800 / TF + 100, 200).

| TF | MAXLEN | retention |
|---|---|---|
| 60 | 280 | 4 h 40 min |
| 120 | 200 (190 → floor) | 6 h 40 min |
| 180 | 200 (160 → floor) | 10 h |
| 300 | 200 (136 → floor) | 16 h 40 min |
| 3600 | 200 (103 → floor) | 8.3 days |

- per token: 280 + 4 × 200 = 1 080 entries × (165 + 25) = **205 KB**
- × 5 tokens = **1.0 MB**

**Indicator streams** `ind:{name}:{tf}s:{ex}:{tok}` use the same MAXLEN
formula, for 3 indicators:

- per token: 3 × 1 080 = 3 240 × (130 + 25) = **502 KB**
- × 5 tokens = **2.5 MB** (upper bound; indengine computes only for its configured tokens)

**`*:latest` keys** (TTL 30 min): about 5 × (1 + 5 + 15) × 160 B ≈ 17 KB. Negligible.

**Total stream state ≈ 14 MB.** It is independent of tick rate, because
MAXLEN caps count, not time. A higher rate shortens retention; it does not
raise memory.

### Gateway replay buffers

The capacity is `NewReplayBuffer(500)` per channel, in `broadcaster.go`.
Each `Push` copies the envelope. Idle non-sticky channels are evicted after
30 minutes (`sweepIdleChannels`: every 5 min, TTL 30 min).

Channels in production:

| Group | Count |
|---|---|
| explicit candle channels: (5 TFs + 1s) × 3 `SUBSCRIBE_TOKENS` | 18 |
| `pub:tick:*` | 5 |
| `pub:ind:*`: 3 indicators × 5 TFs × up to 5 tokens | ≤ 75 |
| `pub:signal`, `pub:orders`, `pub:pnl`, `pub:strike` | 4 |
| `pub:analyst:*` | a few; say 4 |
| **Total** | **≈ 106** (≈ 45 if indicators are computed for the index only) |

Memory, full buffers:

- **typical:** 106 × 500 × 280 B = **14.8 MB** (6.3 MB for the 45-channel case)
- **extra for large payloads:** if `pub:orders` / `pub:pnl` envelopes are about 2 KB, those two channels add 2 × 500 × 2 KB ≈ 2 MB
- **overhead:** Go slice headers and seq, 32 B × 500 × 106 ≈ 1.7 MB

**Budget: ≈ 20 MB worst case.** That is well inside the systemd
`MemoryMax=512M` per unit.

### How far back a reconnecting client can backfill (500 envelopes)

| Channel | Rate | Backfill window |
|---|---|---|
| a tick channel | 20/s peak | 25 s |
| a tick channel | 3/s | 2 min 47 s |
| 1s candle channel | 1/s | 8 min 20 s |
| forming 60 s channel | 1/s | 8 min 20 s |
| final 60 s candles | — | 8 h 20 min |

A client that is gone longer than its channel's window gets
`complete=false` from `/api/missed` and must use `SNAPSHOT`.

## 5. SLO summary

| SLO | Target (p99, market hours) | Source |
|---|---|---|
| Feed freshness | exchange → receipt ≤ 500 ms | `mdengine_e2e_latency_seconds` |
| Tick publish | ≤ 5 ms; 0 failures per 15 min | `mdengine_redis_write_duration_seconds`, `mdengine_redis_write_failures_total{op="tick"}` |
| 1s candle timeliness | candle lag ≤ 2.2 s | `mdengine_candle_lag_seconds` |
| Loss | 0 drops per 15 min on every stage | `mdengine_pipeline_drops_total`, `mdengine_dropped_ticks_total`, `mdengine_fanout_drops_total`, plus late/clamped: `mdengine_late_ticks_total`, `mdengine_tf_late_candles_total`, `mdengine_event_ts_clamped_total` |
| Feed integrity | ≤ 50 missed seq per 5 min (index token) | `mdengine_feed_seq_missed_total` |
| TF close → signal | ≤ 2.6 s after boundary | **not measurable yet** (section 2 gaps) |
| Signal → broker ack | ≤ 1.1 s | **not measurable yet** |

What we control: stages 2–9 and our own poll cadence in stage 11.
What we do not control: exchange and Angel feed latency (stage 1), Angel
REST latency and availability (stage 10), and exchange fill time
(stage 11).

## 6. Candle alignment note

TF buckets are aligned to the Unix epoch in UTC: `ts − ts % TF` in
`tfbuilder`. IST is UTC+5:30, so:

- **120 s** buckets start on even UTC minutes. NSE opens at 09:15 IST = 03:45 UTC, so the first 2-minute candle is 09:14–09:16 IST and contains only 09:15–09:16. The golden file in `backend/cmd/replay/testdata` shows this: the first 120 s candle is stamped `03:44:00Z`.
- **3600 s** buckets run :30 → :30 IST. The first hourly candle is 08:30–09:30 IST and holds only 15 minutes of trading.
- **60, 180 and 300 s** align with 09:15.

If session-aligned candles are intended, this needs a change in `tfbuilder`.
