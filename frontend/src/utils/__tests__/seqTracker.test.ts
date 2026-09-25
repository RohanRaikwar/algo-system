import { describe, expect, it } from 'vitest';
import {
    classifySeq, isMissedComplete, isStaleEpoch, epochStartMs, channelKind,
    shouldApplyBackfill, snapshotLiveUpdates,
} from '../seqTracker';
import { reconnectDelay } from '../helpers';

describe('classifySeq', () => {
    it('first message on a channel is ok', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 0, epoch: 'e1', seq: 7 })).toBe('ok');
    });
    it('next seq is ok', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 4, epoch: 'e1', seq: 5 })).toBe('ok');
    });
    it('skipped seq is a gap', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 4, epoch: 'e1', seq: 9 })).toBe('gap');
    });
    it('repeated seq is a duplicate', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 4, epoch: 'e1', seq: 4 })).toBe('duplicate');
    });
    it('lower seq within the same epoch is a regression', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 400, epoch: 'e1', seq: 3 })).toBe('regression');
    });
    it('different epoch is an epoch change even if seq is higher', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 4, epoch: 'e2', seq: 5 })).toBe('epoch_change');
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 400, epoch: 'e2', seq: 1 })).toBe('epoch_change');
    });
    it('first epoch seen is not a change', () => {
        expect(classifySeq({ storedEpoch: null, storedSeq: 0, epoch: 'e1', seq: 1 })).toBe('ok');
    });
    it('seq 1 after a higher stored seq is a channel restart (idle eviction), not a regression', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 400, epoch: 'e1', seq: 1 })).toBe('ok');
    });
    it('legacy envelopes without epoch fall back to seq comparison', () => {
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 4, epoch: undefined, seq: 5 })).toBe('ok');
        expect(classifySeq({ storedEpoch: 'e1', storedSeq: 4, epoch: undefined, seq: 2 })).toBe('regression');
    });
});

describe('isMissedComplete', () => {
    it('uses explicit complete flag when present', () => {
        expect(isMissedComplete({ complete: false, count: 3, messages: [1, 2, 3] }, 1, 3)).toBe(false);
        expect(isMissedComplete({ complete: true, count: 3, messages: [1, 2, 3] }, 1, 3)).toBe(true);
    });
    it('falls back to count for legacy responses', () => {
        expect(isMissedComplete({ count: 3, messages: [1, 2, 3] }, 1, 3)).toBe(true);
        expect(isMissedComplete({ count: 1, messages: [1] }, 1, 3)).toBe(false);
    });
    it('treats malformed responses as incomplete', () => {
        expect(isMissedComplete(null, 1, 3)).toBe(false);
        expect(isMissedComplete({ error: 'x' }, 1, 3)).toBe(false);
    });
});

describe('reconnectDelay', () => {
    it('uses equal jitter within [cap/2, cap]', () => {
        // attempt 1 → cap = base
        expect(reconnectDelay(1, 1000, 10000, () => 0)).toBe(500);
        expect(reconnectDelay(1, 1000, 10000, () => 0.999999)).toBeCloseTo(1000, 0);
        // attempt 3 → cap = 4000
        expect(reconnectDelay(3, 1000, 10000, () => 0.5)).toBe(3000);
    });
    it('caps exponential growth at max', () => {
        expect(reconnectDelay(20, 1000, 10000, () => 0)).toBe(5000);
        expect(reconnectDelay(20, 1000, 10000, () => 0.999999)).toBeLessThanOrEqual(10000);
    });
    it('varies across calls with real randomness', () => {
        const samples = new Set(Array.from({ length: 20 }, () => reconnectDelay(4, 1000, 10000)));
        expect(samples.size).toBeGreaterThan(1);
        for (const s of samples) {
            expect(s).toBeGreaterThanOrEqual(4000);
            expect(s).toBeLessThanOrEqual(8000);
        }
    });
    it('treats attempt < 1 as the first attempt', () => {
        expect(reconnectDelay(0, 1000, 10000, () => 0)).toBe(500);
    });
});

describe('epoch ordering', () => {
    const older = '18f00000000-aaaaaaaaaaaa';
    const newer = '18f00000001-bbbbbbbbbbbb';
    it('parses the hex start-ms prefix', () => {
        expect(epochStartMs('ff-abc')).toBe(255);
        expect(epochStartMs('garbage')).toBeNull();
        expect(epochStartMs(undefined)).toBeNull();
    });
    it('frames from an older epoch are stale', () => {
        expect(isStaleEpoch(newer, older)).toBe(true);
    });
    it('same or newer epochs are not stale', () => {
        expect(isStaleEpoch(older, older)).toBe(false);
        expect(isStaleEpoch(older, newer)).toBe(false);
    });
    it('unknown stored epoch, missing or unparsable epochs are never stale', () => {
        expect(isStaleEpoch(null, older)).toBe(false);
        expect(isStaleEpoch(newer, undefined)).toBe(false);
        expect(isStaleEpoch('zz-1', older)).toBe(false);
        expect(isStaleEpoch(newer, 'zz-1')).toBe(false);
    });
    it('compares numerically, not lexically', () => {
        // 'ff' < '100' numerically though 'ff' > '100' as strings
        expect(isStaleEpoch('100-x', 'ff-y')).toBe(true);
        expect(isStaleEpoch('ff-y', '100-x')).toBe(false);
    });
});

describe('channelKind', () => {
    it('classifies full-state channels', () => {
        for (const ch of ['pub:orders', 'pub:pnl', 'pub:strike', 'pub:analyst:state', 'pub:analyst:levels']) {
            expect(channelKind(ch)).toBe('full_state');
        }
    });
    it('ticks are latest-value only', () => {
        expect(channelKind('pub:tick:NSE:99926000')).toBe('latest_only');
    });
    it('candles, indicators and signals are streams', () => {
        expect(channelKind('pub:candle:60s:NSE:1')).toBe('stream');
        expect(channelKind('pub:ind:SMA_9:60s:NSE:1')).toBe('stream');
        expect(channelKind('pub:signal')).toBe('stream');
    });
});

describe('shouldApplyBackfill', () => {
    it('skips full-state envelopes not newer than the latest applied', () => {
        expect(shouldApplyBackfill('pub:orders', 5, 9)).toBe(false);
        expect(shouldApplyBackfill('pub:strike', 9, 9)).toBe(false);
        expect(shouldApplyBackfill('pub:analyst:state', 10, 9)).toBe(true);
    });
    it('always applies stream envelopes', () => {
        expect(shouldApplyBackfill('pub:candle:60s:NSE:1', 5, 9)).toBe(true);
    });
});

describe('snapshotLiveUpdates', () => {
    const orders = { orders: [{ id: 'a' }] };
    const pnl = { realized_pnl: 1 };
    it('applies state when nothing newer was seen', () => {
        const out = snapshotLiveUpdates({ orders, pnl, liveSeqs: { 'pub:orders': 4, 'pub:pnl': 0 } }, {});
        expect(out).toEqual([
            { channel: 'pub:orders', data: orders, seq: 4 },
            { channel: 'pub:pnl', data: pnl, seq: 0 },
        ]);
    });
    it('skips state older than the latest applied live seq', () => {
        const out = snapshotLiveUpdates({ orders, pnl, liveSeqs: { 'pub:orders': 4, 'pub:pnl': 0 } }, { 'pub:orders': 5, 'pub:pnl': 2 });
        expect(out).toEqual([]);
    });
    it('applies state at or after the latest seq', () => {
        const out = snapshotLiveUpdates({ orders, liveSeqs: { 'pub:orders': 5 } }, { 'pub:orders': 5 });
        expect(out).toEqual([{ channel: 'pub:orders', data: orders, seq: 5 }]);
    });
    it('ignores missing fields', () => {
        expect(snapshotLiveUpdates({}, {})).toEqual([]);
    });
});
