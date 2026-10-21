import { describe, expect, it } from 'vitest';
import { isWatchAction, matchesActionFilter, watchBadgeClass, watchExitPricedAtPremium } from '../watch';
import { actionBadge } from '../classes';
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
    it('shows P&L only on WATCH_EXIT rows priced at the premium', () => {
        expect(watchExitPricedAtPremium({ action: 'WATCH_EXIT', reason: 'EXITWATCH SHADOW SCORE: p=0.84 entry=153.65 idx=22191.95' })).toBe(true);
        expect(watchExitPricedAtPremium({ action: 'WATCH_EXIT', reason: 'EXITWATCH AUTO SCORE: p=0.84 [velocity]' })).toBe(false);
        expect(watchExitPricedAtPremium({ action: 'WATCH_TIGHTEN', reason: 'x idx=1' })).toBe(false);
        expect(watchExitPricedAtPremium({ action: 'EXIT', reason: 'x idx=1' })).toBe(false);
    });

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
        expect(watchBadgeClass('WATCH_EXIT')).toBe(actionBadge('watchExit'));
        expect(watchBadgeClass('WATCH_TIGHTEN')).toBe(actionBadge('watchTighten'));
        expect(watchBadgeClass('WATCH_HOLD')).toBe(actionBadge('watchHold'));
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

    it('draws a watch card for WATCH_EXIT only, without closing the entry', () => {
        const markers = buildChartMarkers(newestFirst, [], 'NSE:99926000', 60, false);
        expect(markers.map(m => m.kind)).toEqual(['entry', 'watch']);
        expect(markers[1].label).toBe('WATCH EXIT CALL');
        // Old-format row (index price, no idx= tag): no premium, no P&L.
        expect(markers[1].pnl).toBeUndefined();
    });

    it('shows would-be P&L on a premium-priced WATCH_EXIT and keeps the entry for the strategy EXIT', () => {
        const b = sig({ action: 'BUY', price: 15365, qty: 65 });
        const w = sig({ action: 'WATCH_EXIT', price: 16765, reason: 'EXITWATCH SHADOW SCORE: p=0.84 entry=153.65 idx=22191.95' });
        const x = sig({ action: 'EXIT', price: 16000 });
        const markers = buildChartMarkers([x, w, b], [], 'NSE:99926000', 60, false);
        expect(markers.map(m => m.kind)).toEqual(['entry', 'watch', 'exit']);
        expect(markers[1].pnl).toBe((16765 - 15365) * 65);
        expect(markers[1].lines).toContain('@ ₹167.65');
        expect(markers[2].pnl).toBe((16000 - 15365) * 65);
    });
});

describe('entry advice (WATCH_BUY)', () => {
    const advice = sig({
        action: 'WATCH_BUY', price: 11050, qty: 65, fno_symbol: 'NIFTY13OCT2622500CE',
        reason: 'SR CONTINUE CALL regime=MIXED level=2238345 stop=2233145 target=2254545',
    });

    it('is a watch action with its own badge', () => {
        expect(isWatchAction('WATCH_BUY')).toBe(true);
        expect(matchesActionFilter('WATCH_BUY', 'WATCH')).toBe(true);
        expect(matchesActionFilter('WATCH_BUY', 'BUY')).toBe(false);
        expect(watchBadgeClass('WATCH_BUY')).toBe(actionBadge('watchBuy'));
    });

    it('opens no position and pairs with no exit', () => {
        const x = sig({ action: 'EXIT', price: 12000 });
        expect(buildOpenOrders([advice])).toHaveLength(0);
        expect(buildCompletedEvents([x, advice])).toHaveLength(0);
    });

    it('draws an advice card without qty or P&L', () => {
        const markers = buildChartMarkers([advice], [], 'NSE:99926000', 60, false);
        expect(markers).toHaveLength(1);
        expect(markers[0].kind).toBe('watch');
        expect(markers[0].label).toBe('ADVICE BUY CALL');
        expect(markers[0].pnl).toBeUndefined();
        expect(markers[0].lines[0]).toContain('advice');
        expect(markers[0].lines.some(l => l.startsWith('qty '))).toBe(false);
        expect(markers[0].lines.some(l => l.includes('NIFTY13OCT2622500CE'))).toBe(true);
    });
});
