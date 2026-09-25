import { describe, it, expect } from 'vitest';
import { appendMetricsSample, METRICS_HISTORY_LEN, type MetricsSample } from '../useWSStore';
import type { SystemMetrics } from '../../types/ws';

function metrics(ticks: number | undefined, p95 = 0): SystemMetrics {
    return {
        e2e_latency_p95_ms: p95,
        pipeline: ticks === undefined ? null : { ticks_total: ticks } as SystemMetrics['pipeline'],
    } as SystemMetrics;
}

describe('appendMetricsSample', () => {
    it('derives tick rate from consecutive totals', () => {
        const h = appendMetricsSample([], metrics(100), 1_000, metrics(160, 1.4), 3_000);
        expect(h).toHaveLength(1);
        expect(h[0].tickRate).toBe(30);
        expect(h[0].p95).toBe(1.4);
    });

    it('has no rate for the first sample or a counter reset', () => {
        expect(appendMetricsSample([], null, null, metrics(100), 1_000)[0].tickRate).toBeNull();
        expect(appendMetricsSample([], metrics(500), 1_000, metrics(10), 3_000)[0].tickRate).toBeNull();
    });

    it('has no rate when mdengine is not reporting', () => {
        expect(appendMetricsSample([], metrics(100), 1_000, metrics(undefined), 3_000)[0].tickRate).toBeNull();
    });

    it('caps history length', () => {
        let h: MetricsSample[] = [];
        for (let i = 0; i < METRICS_HISTORY_LEN + 20; i++) {
            h = appendMetricsSample(h, metrics(i), i * 2000, metrics(i + 1), (i + 1) * 2000);
        }
        expect(h).toHaveLength(METRICS_HISTORY_LEN);
        expect(h[h.length - 1].at).toBe((METRICS_HISTORY_LEN + 20) * 2000);
    });
});
