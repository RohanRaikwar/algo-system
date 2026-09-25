import type { SystemMetrics } from '../../types/ws';

/** Metrics older than this are treated as stale (gateway sends every 2s). */
export const STALE_MS = 6000;

export const SERVICE_LABELS: Record<string, string> = {
    mdengine: 'Market data',
    indengine: 'Indicators',
    stratengine: 'Strategy & orders',
    analyst: 'Analyst',
    api_gateway: 'API gateway',
};

/** True when the page must not present metrics as current. */
export function isStale(now: number, metricsAt: number, connected: boolean): boolean {
    return !connected || now - metricsAt > STALE_MS;
}

export interface Issue {
    key: string;
    text: string;
    tone: 'warn' | 'bad';
}

export function collectIssues(m: SystemMetrics, ticking: boolean): Issue[] {
    const issues: Issue[] = [];
    if (m.services === undefined) {
        issues.push({ key: 'old-gw', text: 'The running API gateway is an older build that cannot report service stats. Restart the stack to load it.', tone: 'warn' });
        return issues;
    }
    if (m.redis_ok === false) {
        issues.push({ key: 'redis', text: 'The gateway cannot reach Redis. Live data and service stats are unavailable.', tone: 'bad' });
        return issues;
    }
    for (const s of m.services ?? []) {
        if (s.status === 'down') {
            issues.push({ key: `svc-${s.name}`, text: `${SERVICE_LABELS[s.name] ?? s.name} (${s.name}) is not sending heartbeats.`, tone: 'bad' });
        }
    }
    if (m.orders && m.orders.cb_state === 1) {
        issues.push({ key: 'cb', text: 'Order circuit breaker is open: new real entries are rejected after repeated broker failures. Exits still go through.', tone: 'bad' });
    } else if (m.orders && m.orders.cb_state === 2) {
        issues.push({ key: 'cb', text: 'Order circuit breaker is testing the broker connection (half-open).', tone: 'warn' });
    }
    const p = m.pipeline;
    if (p && ticking && p.candle_lag_sec >= 2) {
        issues.push({ key: 'lag', text: `Candles are running ${p.candle_lag_sec.toFixed(1)}s behind real time (normal is about 1s).`, tone: p.candle_lag_sec >= 5 ? 'bad' : 'warn' });
    }
    if (p && p.market_open && !ticking) {
        issues.push({ key: 'feed', text: 'Market is open but no ticks are arriving. Check the market data feed.', tone: 'bad' });
    }
    return issues;
}

