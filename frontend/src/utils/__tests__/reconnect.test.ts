import { describe, expect, it } from 'vitest';
import { ReconnectBackoff, RECONNECT_STABLE_MS } from '../requestControl';
import { ConnectionEpochGate, snapshotSignalUpdates } from '../seqTracker';

describe('ReconnectBackoff', () => {
    it('keeps growing attempts when connections drop before becoming stable', () => {
        const b = new ReconnectBackoff();
        expect(b.closed(0)).toBe(1);
        b.opened(1000);
        // Server dropped us 800ms after open (e.g. slow-client disconnect).
        expect(b.closed(1800)).toBe(2);
        b.opened(3000);
        expect(b.closed(3500)).toBe(3);
    });

    it('resets attempts once a connection stayed open for the stable window', () => {
        const b = new ReconnectBackoff();
        b.closed(0);
        b.closed(10);
        b.opened(100);
        expect(b.closed(100 + RECONNECT_STABLE_MS)).toBe(1);
    });

    it('defaults to a 10s stable window', () => {
        expect(RECONNECT_STABLE_MS).toBe(10000);
    });
});

describe('ConnectionEpochGate', () => {
    const newer = '1a0cdf65564-aaaa';
    const olderClock = '1a0cdf00000-bbbb'; // restarted gateway with a clock behind

    it('adopts the first epoch of a new socket even if its clock is behind', () => {
        const g = new ConnectionEpochGate();
        g.pin(); // previous connection had adopted `newer`
        g.reset(); // new socket opened
        expect(g.isStale(newer, olderClock)).toBe(false);
    });

    it('applies stale-epoch checks within one connection', () => {
        const g = new ConnectionEpochGate();
        g.reset();
        g.pin();
        expect(g.isStale(newer, olderClock)).toBe(true);
        expect(g.isStale(olderClock, newer)).toBe(false);
    });
});

describe('snapshotSignalUpdates', () => {
    const env = (seq: number, ts: string) => ({
        channel: 'pub:signal', channel_seq: seq,
        data: { strategy_name: 'S', action: 'BUY', token: '1', ts },
    });

    it('returns signals newer than the latest applied seq, in seq order', () => {
        const out = snapshotSignalUpdates([env(5, 'a'), env(3, 'b'), env(4, 'c')], 3, []);
        expect(out.map(e => e.channel_seq)).toEqual([4, 5]);
    });

    it('skips signals already in the store (e.g. after an epoch reset)', () => {
        const existing = [{ strategy: 'S', action: 'BUY', token: '1', candle_ts: 'a' }];
        const out = snapshotSignalUpdates([env(1, 'a'), env(2, 'b')], 0, existing);
        expect(out.map(e => e.channel_seq)).toEqual([2]);
    });

    it('ignores missing or malformed entries', () => {
        expect(snapshotSignalUpdates(undefined, 0, [])).toEqual([]);
        expect(snapshotSignalUpdates([null, { channel_seq: 'x' }, { channel_seq: 2 }], 0, [])).toEqual([]);
    });
});
