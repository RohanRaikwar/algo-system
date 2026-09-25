import { describe, it, expect } from 'vitest';
import { collectIssues, isStale, STALE_MS } from '../healthStatus';
import type { SystemMetrics, PipelineSnapshot } from '../../../types/ws';

function base(over: Partial<SystemMetrics> = {}): SystemMetrics {
    return {
        redis_ok: true,
        services: [{ name: 'mdengine', status: 'up', last_beat: 0, age_ms: 1000 }],
        orders: { cb_state: 0, cb_failures: 0, rl_count: 0, rl_max: 10, updated_at: '' },
        pipeline: { candle_lag_sec: 1, market_open: false } as PipelineSnapshot,
        ...over,
    } as SystemMetrics;
}

describe('isStale', () => {
    it('is fresh within the window while connected', () => {
        expect(isStale(10_000, 10_000 - STALE_MS, true)).toBe(false);
    });
    it('is stale once the window passes', () => {
        expect(isStale(10_000, 10_000 - STALE_MS - 1, true)).toBe(true);
    });
    it('is stale whenever disconnected', () => {
        expect(isStale(10_000, 10_000, false)).toBe(true);
    });
});

describe('collectIssues', () => {
    it('reports nothing when healthy', () => {
        expect(collectIssues(base(), true)).toEqual([]);
    });

    it('flags an old gateway build instead of claiming health', () => {
        const issues = collectIssues(base({ services: undefined }), true);
        expect(issues.map(i => i.key)).toEqual(['old-gw']);
    });

    it('flags Redis loss as the only issue', () => {
        const issues = collectIssues(base({ redis_ok: false }), true);
        expect(issues.map(i => [i.key, i.tone])).toEqual([['redis', 'bad']]);
    });

    it('flags down services and an open breaker', () => {
        const issues = collectIssues(base({
            services: [{ name: 'stratengine', status: 'down', last_beat: 0, age_ms: 0 }],
            orders: { cb_state: 1, cb_failures: 3, rl_count: 0, rl_max: 10, updated_at: '' },
        }), true);
        expect(issues.map(i => i.key)).toEqual(['svc-stratengine', 'cb']);
        expect(issues.every(i => i.tone === 'bad')).toBe(true);
    });

    it('flags candle lag only while ticks flow', () => {
        const lagging = base({ pipeline: { candle_lag_sec: 3, market_open: false } as PipelineSnapshot });
        expect(collectIssues(lagging, true).map(i => [i.key, i.tone])).toEqual([['lag', 'warn']]);
        expect(collectIssues(lagging, false)).toEqual([]);
    });

    it('flags a silent feed during market hours', () => {
        const open = base({ pipeline: { candle_lag_sec: 1, market_open: true } as PipelineSnapshot });
        expect(collectIssues(open, false).map(i => i.key)).toEqual(['feed']);
    });
});
