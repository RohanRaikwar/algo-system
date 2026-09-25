import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createConcurrencyLimiter, resyncJitterMs, SnapshotRequestTracker, RESYNC_JITTER_MAX_MS } from '../requestControl';

describe('resyncJitterMs', () => {
    it('is within [0, 3000)', () => {
        expect(RESYNC_JITTER_MAX_MS).toBe(3000);
        expect(resyncJitterMs(() => 0)).toBe(0);
        expect(resyncJitterMs(() => 0.999999)).toBeLessThan(3000);
        expect(resyncJitterMs(() => 0.5)).toBe(1500);
    });
});

describe('createConcurrencyLimiter', () => {
    it('caps concurrent tasks and runs queued ones as slots free', async () => {
        const run = createConcurrencyLimiter(2);
        let active = 0;
        let peak = 0;
        const resolvers: Array<() => void> = [];
        const task = () => new Promise<number>(resolve => {
            active++;
            peak = Math.max(peak, active);
            resolvers.push(() => { active--; resolve(active); });
        });
        const all = Promise.all([run(task), run(task), run(task), run(task)]);
        await Promise.resolve();
        expect(resolvers.length).toBe(2);
        while (resolvers.length) {
            resolvers.shift()!();
            await new Promise(r => setTimeout(r, 0));
        }
        await all;
        expect(peak).toBe(2);
    });
    it('frees the slot when a task rejects', async () => {
        const run = createConcurrencyLimiter(1);
        await expect(run(() => Promise.reject(new Error('x')))).rejects.toThrow('x');
        await expect(run(() => Promise.resolve(7))).resolves.toBe(7);
    });
});

describe('SnapshotRequestTracker', () => {
    beforeEach(() => { vi.useFakeTimers(); });
    afterEach(() => { vi.useRealTimers(); });

    it('retries on timeout up to maxRetries', () => {
        const t = new SnapshotRequestTracker({ timeoutMs: 5000, maxRetries: 3 });
        let n = 0;
        const retry = () => { n++; t.track(`r${n + 1}`, retry, true); };
        t.track('r1', retry);
        vi.advanceTimersByTime(4999);
        expect(n).toBe(0);
        vi.advanceTimersByTime(1);
        expect(n).toBe(1);
        vi.advanceTimersByTime(20000);
        expect(n).toBe(3);
    });

    it('accepts only the latest reqId and stops retrying', () => {
        const t = new SnapshotRequestTracker({ timeoutMs: 5000, maxRetries: 3 });
        const retry = vi.fn();
        t.track('r1', retry);
        t.track('r2', retry);
        expect(t.accept('r1')).toBe(false);
        expect(t.accept('r2')).toBe(true);
        vi.advanceTimersByTime(60000);
        expect(retry).not.toHaveBeenCalled();
    });

    it('accepts snapshots without reqId (legacy)', () => {
        const t = new SnapshotRequestTracker({ timeoutMs: 5000, maxRetries: 3 });
        expect(t.accept(undefined)).toBe(true);
    });

    it('a new (non-retry) request resets the retry budget', () => {
        const t = new SnapshotRequestTracker({ timeoutMs: 1000, maxRetries: 1 });
        let n = 0;
        const retry = () => { n++; t.track(`x${n}`, retry, true); };
        t.track('a', retry);
        vi.advanceTimersByTime(5000);
        expect(n).toBe(1);
        t.track('b', retry);
        vi.advanceTimersByTime(5000);
        expect(n).toBe(2);
    });

    it('clear cancels pending retries', () => {
        const t = new SnapshotRequestTracker({ timeoutMs: 1000, maxRetries: 3 });
        const retry = vi.fn();
        t.track('r1', retry);
        t.clear();
        vi.advanceTimersByTime(10000);
        expect(retry).not.toHaveBeenCalled();
    });
});
