import { describe, expect, it } from 'vitest';
import { buildOpenOrders } from '../signalAnalytics';
import type { LiveOrderStatePayload, SignalRecord } from '../../../types/signal';

function sig(p: Partial<SignalRecord>): SignalRecord {
    return {
        id: 1, strategy: 'TG_TCI', action: 'BUY', side: 'PUT', token: '99919000', exchange: 'BSE',
        reason: 'TG ENTRY', ema_values: '', candle_ts: '', created_at: '2026-11-26T04:13:58Z', ...p,
    };
}

describe('Telegram open orders', () => {
    it('hides a Telegram BUY stratengine does not hold (entry never filled)', () => {
        expect(buildOpenOrders([sig({})])).toHaveLength(0);
    });

    it('shows a held Telegram position with its levels', () => {
        const live = { 'TG_TCI|PUT': { strategy_name: 'TG_TCI', side: 'PUT', stoploss_price: 42000, target_price: 47000 } as LiveOrderStatePayload };
        const [o] = buildOpenOrders([sig({})], live);
        expect(o.stoplossPrice).toBe(420);
        expect(o.targetPrice).toBe(470);
    });

    it('keeps a partial exit open and closes on the book-only EXIT', () => {
        const live = { 'TG_TCI|PUT': { strategy_name: 'TG_TCI', side: 'PUT' } as LiveOrderStatePayload };
        const buy = sig({ id: 1 });
        const part = sig({ id: 2, action: 'PARTIAL_EXIT', created_at: '2026-11-26T04:20:00Z' });
        expect(buildOpenOrders([part, buy], live)).toHaveLength(1);
        const refused = sig({ id: 3, action: 'EXIT', reason: 'ENTRY_REFUSED x', created_at: '2026-11-26T04:30:00Z' });
        expect(buildOpenOrders([refused, part, buy], live)).toHaveLength(0);
    });

    it('does not hide other strategies without a live order', () => {
        expect(buildOpenOrders([sig({ strategy: 'NIFTY50_SR', exchange: 'NSE', token: '99926000' })])).toHaveLength(1);
    });
});
