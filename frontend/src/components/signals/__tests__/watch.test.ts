import { describe, expect, it } from 'vitest';
import { isWatchAction, matchesActionFilter, watchBadgeClass } from '../watch';
import { buildCompletedEvents, buildOpenOrders } from '../signalAnalytics';
import { buildChartMarkers } from '../../chart/hooks/useChartMarkers';
import type { SignalRecord } from '../../../types/signal';

let id = 0;
function sig(p: Partial<SignalRecord>): SignalRecord {
    id += 1;
    return {
        id, strategy: 'NIFTY50_SR', action: 'BUY', side: 'CALL', token: '99926000', exchange: 'NSE',
        reason: '', ema_values: '', candle_ts: `2026-10-05T04:3${id % 10}:00Z`, created_at: `2026-10-05T04:3${id % 10}:00Z`,
        price: 2400000, ...p,
    };
}

describe('exit watch signals', () => {
    it('recognises WATCH_* actions and filters them', () => {
        expect(isWatchAction('WATCH_EXIT')).toBe(true);
        expect(isWatchAction('watch_hold')).toBe(true);
        expect(isWatchAction('EXIT')).toBe(false);
        expect(isWatchAction(undefined)).toBe(false);
        expect(matchesActionFilter('WATCH_HOLD', 'WATCH')).toBe(true);
        expect(matchesActionFilter('EXIT', 'WATCH')).toBe(false);
        expect(matchesActionFilter('EXIT', 'EXIT')).toBe(true);
        expect(matchesActionFilter('WATCH_EXIT', 'EXIT')).toBe(false);
        expect(matchesActionFilter('BUY', 'ALL')).toBe(true);
        expect(watchBadgeClass('WATCH_EXIT')).toContain('watch-exit');
        expect(watchBadgeClass('WATCH_TIGHTEN')).toContain('watch-tighten');
        expect(watchBadgeClass('WATCH_HOLD')).toContain('watch-hold');
    });

    // Store order is newest-first.
    const buy = sig({ action: 'BUY' });
    const hold = sig({ action: 'WATCH_HOLD', reason: 'EXITWATCH SHADOW RUNNER TARGET_RUN: target hit' });
    const wexit = sig({ action: 'WATCH_EXIT', reason: 'EXITWATCH SHADOW RUNNER SR_REJECT: rejected', price: 2406500 });
    const newestFirst = [wexit, hold, buy];

    it('never closes or completes a trade', () => {
        expect(buildOpenOrders(newestFirst)).toHaveLength(1);
        expect(buildCompletedEvents(newestFirst)).toHaveLength(0);
    });

    it('draws no chart marker', () => {
        const markers = buildChartMarkers(newestFirst, [], 'NSE:99926000', 60, false);
        expect(markers).toHaveLength(1);
        expect(markers[0].kind).toBe('entry');
    });
});
