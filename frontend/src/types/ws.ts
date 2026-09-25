// ── WebSocket Message Types ──

export interface WSEnvelope {
    type?: string;        // 'metrics' | 'pong' | 'config_update' | 'SNAPSHOT' | 'LIVE' | 'ERROR' | 'signal'
    channel?: string;     // e.g. 'pub:candle:60s:NSE:99926000'
    data?: unknown;
    ts?: string;
    // metrics fields
    metrics?: SystemMetrics;
    marketOpen?: boolean;
    marketStatus?: string;
    // pong fields
    ping?: number;
    // config_update fields
    entries?: Array<{ name: string; tf: number; color?: string }>;
    // SNAPSHOT fields
    reqId?: string;
    symbol?: string;
    tf?: number;
    candles?: SnapshotCandle[];
    indicators?: Record<string, SnapshotIndPoint[]>;
    analystState?: import('./analyst').MarketStateEvent;
    analystLevels?: import('./analyst').LevelUpdateEvent;
    analystBreakout?: import('./analyst').BreakoutEvent;
    // SNAPSHOT full-state for resync; liveSeqs = their channel_seq (0 = unknown)
    orders?: unknown;
    pnl?: unknown;
    liveSeqs?: Record<string, number>;
    // SNAPSHOT: recent pub:signal envelopes (with channel_seq), oldest first
    signals?: unknown[];
    // LIVE fields
    candle?: SnapshotCandle;
    // ERROR fields
    error?: string;
    // Sequence fields for gap detection
    seq?: number;
    channel_seq?: number;
    // Gateway seq epoch (on data envelopes, SNAPSHOT and 'hello'); seqs only compare within one epoch
    epoch?: string;
    // 'hello' fields
    reason?: string;
}

/** mdengine market-data pipeline stats. */
export interface PipelineSnapshot {
    ticks_total: number;
    candles_total: number;
    dropped_ticks: number;
    late_ticks: number;
    stale_candles_rejected: number;
    ws_reconnects: number;
    feed_seq_gaps: number;
    feed_stale_alerts: number;
    candle_lag_sec: number;
    watermark_delay_sec: number;
    market_open: boolean;
    updated_at: string;
}

/** indengine stats. */
export interface IndicatorSnapshot {
    indicators_total: number;
    updated_at: string;
}

/** stratengine order-path guards. */
export interface OrderSnapshot {
    cb_state: number;     // 0=closed, 1=open, 2=half-open
    cb_failures: number;
    rl_count: number;
    rl_max: number;
    updated_at: string;
}

export interface ServiceHealth {
    name: string;
    status: 'up' | 'down';
    last_beat: number;
    age_ms: number;
}

export interface SystemMetrics {
    cpu_percent: number;
    cpu_cores: number;
    cpu_load_1: number;
    cpu_load_5: number;
    cpu_load_15: number;
    mem_percent: number;
    mem_used_mb: number;
    mem_total_mb: number;
    heap_alloc_mb: number;
    sys_mb: number;
    goroutines: number;
    gc_runs: number;
    uptime_sec: number;
    indicator_compute_ms?: number;
    e2e_latency_p50_ms?: number;
    e2e_latency_p95_ms?: number;
    e2e_latency_p99_ms?: number;

    e2e_latency_samples?: number;

    // Redis reachability from the gateway
    redis_ok?: boolean;
    redis_ping_ms?: number;

    // Per-process snapshots; null when that process is not reporting
    pipeline?: PipelineSnapshot | null;
    indicators?: IndicatorSnapshot | null;
    orders?: OrderSnapshot | null;

    // Service heartbeats
    services?: ServiceHealth[];
}

export interface ParsedChannel {
    type: 'indicator' | 'candle' | 'tick';
    name?: string;
    tf?: number;
    exchange?: string;
    token?: string;
}

export interface IndicatorPayload {
    name: string;
    tf: number;
    value: number;
    ts: string;
    ready: boolean;
    live?: boolean;
    exchange: string;
    token: string;
}

export interface CandlePayload {
    ts: string;
    open: number;
    high: number;
    low: number;
    close: number;
    volume: number;
    count: number;
    forming: boolean;
    exchange: string;
    token: string;
    tf: number;
}

export interface TickPayload {
    tick_ts?: string;
    ts?: string;
    price: number;
    qty: number;
    token: string;
    exchange: string;
}

// ── SUBSCRIBE Protocol Types ──

export interface SubscribeMsg {
    type: 'SUBSCRIBE';
    reqId: string;
    symbol: string;
    tf: number;
    history: { candles: number };
    indicators: IndicatorSpecMsg[];
}

export interface IndicatorSpecMsg {
    id: string;      // e.g. "smma", "ema", "sma"
    source: string;  // e.g. "close"
    params: Record<string, number>;  // e.g. { length: 21 }
    tf?: number;     // per-indicator TF override (seconds), omit to use subscription TF
}

export interface SnapshotCandle {
    ts: string;
    open: number;
    high: number;
    low: number;
    close: number;
    volume: number;
    count?: number;
}

export interface SnapshotIndPoint {
    ts: string;
    value: number;
    ready: boolean;
}

export interface UnsubscribeMsg {
    type: 'UNSUBSCRIBE';
    reqId: string;
    symbol: string;
    tf: number;
}
