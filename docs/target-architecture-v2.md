# Trading System V2 Target Architecture

## 1) Goal
Build a resilient, low-latency, horizontally scalable trading platform with clear service boundaries, explicit event contracts, and safer runtime operations.

Target outcomes:
- Stable realtime UX with coherent cross-timeframe updates.
- Deterministic replay/recovery after service restart.
- Faster feature delivery through modular boundaries and ADR-driven decisions.
- Production-safe runtime changes (config, strategy toggles, risk controls).

## 2) Architecture Principles
1. One bounded context per service.
2. Contract-first events (versioned schema, backward compatibility).
3. Stateful recovery with explicit checkpoints (not wall-clock inference).
4. Idempotent processing for all critical flows.
5. Observability via SLOs, tracing, and replay diagnostics.
6. Control-plane and data-plane are separated.

## 3) Target Service Topology

```text
External Market/Broker
   │
   ▼
[MS1 mdengine] ──▶ [Redis Streams + PubSub] ──▶ [MS2 indengine]
   │                          │                        │
   │                          └──────────────┬─────────┘
   ▼                                         ▼
[SQLite warm store]                    [MS3 stratengine]
                                              │
                                              ▼
                                      [Order Gateway Adapter]
                                              │
                                              ▼
                                         Broker REST

                               [MS4 api_gateway]
                                     │      │
                                     │      └─ REST snapshots/backfill
                                     ▼
                                  WebSocket
                                     │
                                     ▼
                                React Frontend

                     [Control Plane Service]
                  (runtime config, policy, audit)
```

## 4) Plane Separation

### Data Plane
- `mdengine`: ingestion, event-time candleing, TF aggregation, publish/store.
- `indengine`: indicator computation and checkpointed stream consumption.
- `stratengine`: signal generation, position/risk state, execution intents.
- `api_gateway`: transport fanout, replay API, query endpoints.

### Control Plane
- Central runtime config and policy distribution:
  - tokens / TFs / indicators
  - kill-switch / strategy enablement
  - risk parameter changes
- Audit trail for who changed what and when.

## 5) Canonical Event Contract

Every emitted event uses a versioned envelope:

```json
{
  "schema_version": 1,
  "event_id": "uuid-or-hash",
  "event_type": "candle|indicator|signal|pnl|order|market_state",
  "symbol": "NSE:99926000",
  "tf": 60,
  "seq": 12345,
  "channel_seq": 98,
  "event_ts": "2026-03-27T09:15:00.000Z",
  "source_ts": "2026-03-27T09:15:00.000Z",
  "is_final": true,
  "producer": "mdengine",
  "payload": {}
}
```

Rules:
- `event_ts`: logical event time (exchange/event-time).
- `source_ts`: producer observed time.
- `seq`: global sequence (optional compatibility).
- `channel_seq`: per-channel monotonic sequence for gap detection.
- `is_final`: forming vs finalized semantics.

## 6) Reliability Model

### Stream Consumption
- Consumer groups with at-least-once semantics.
- Idempotent processors keyed by `(stream, message-id)` or `event_id`.
- PEL reclaim for abandoned pending messages.

### Checkpointing
- Store per-stream checkpoints (`stream -> last_delivered_id`) in snapshots.
- Legacy single checkpoint field is fallback-only.
- Replay starts from explicit stream offsets.

### Backpressure
- Explicit policy per channel:
  - critical channels: block/retry with bounded queue
  - non-critical preview channels: drop with metrics

## 7) State and Storage Strategy

Hot storage:
- Redis Streams/PubSub for realtime and replay windows.

Warm storage:
- SQLite for bounded historical query, audit, and local recovery.

Cold storage (next step):
- Object storage snapshots for long retention and incident forensics.

## 8) Scalability Model

Shard key: `exchange:symbol`.

Sharded services:
- `mdengine` shard owner per symbol partition.
- `indengine` consumes aligned stream partitions.
- `stratengine` evaluates symbols in matching partitions.

Benefits:
- Reduced cross-service contention.
- Predictable horizontal scaling.
- Simpler failure isolation.

## 9) Security and Governance
- Service-to-service auth tokens for write endpoints.
- Restricted runtime mutation endpoints (role-based).
- Signed config bundles from control plane.
- Immutable audit log for config/order/signal lifecycle.

## 10) Observability and SLOs

Primary SLOs:
- Tick-to-WS emit latency p95 < 250 ms.
- Indicator compute latency p95 < 150 ms.
- Replay gap recovery success > 99.9%.
- Critical message drop rate < 0.01%.
- Checkpoint lag (stream head - last processed) bounded per service.

Instrumentation:
- Prometheus counters/histograms per stage.
- Trace propagation across mdengine -> indengine -> stratengine -> gateway.
- Structured logs with `event_id`, `symbol`, `tf`, `seq`.

## 11) Release and Rollout

Deployment strategy:
1. Dual-format publish (old + new envelope) where needed.
2. Canary one shard/service.
3. Validate SLOs and replay correctness.
4. Ramp to full shards.

Operational gates:
- Contract tests pass.
- Replay correctness checks pass.
- Rollback command and snapshot restore verified.

## 12) Phased Roadmap

Phase 1 (Foundations, 2-4 weeks):
- Canonical envelope + gateway transport unification.
- Per-stream checkpoint snapshots.
- Config validation hardening in service startup.

Phase 2 (Modularity and scale, 4-8 weeks):
- Stratengine orchestration split by module.
- Control plane MVP for runtime config and audit.
- Initial symbol sharding boundaries.

Phase 3 (Platform hardening, 8-12 weeks):
- SLO dashboards + tracing.
- Security hardening + policy enforcement.
- Canary/shadow replay rollout model.
