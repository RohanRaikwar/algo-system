import { describe, expect, it } from 'vitest';
import { buildCompletedEvents, buildOpenOrders, legInfo } from '../signalAnalytics';
import { buildLiveOrderKey } from '../../../store/useLiveOrderStore';
import type { SignalRecord } from '../../../types/signal';

let id = 0;
function sig(p: Partial<SignalRecord>): SignalRecord {
    id += 1;
    return {
        id, strategy: 'NIFTY50_RANGE_IC', action: 'BUY', side: 'CALL', token: '302', exchange: 'NFO',
        reason: '', ema_values: '', candle_ts: '', created_at: `2026-09-28T10:0${id % 10}:00Z`, ...p,
    };
}

describe('multi-leg signals', () => {
    it('keys live orders by leg when present', () => {
        expect(buildLiveOrderKey('IC', 'call')).toBe('IC|CALL');
        expect(buildLiveOrderKey('IC', 'call', 'SHORT_CE')).toBe('IC|CALL|SHORT_CE');
    });

    it('parses the leg tag from the reason when fields are missing (REST history)', () => {
        expect(legInfo(sig({ reason: '[SHORT_CE 24200] IC ENTRY' }))).toEqual({ leg: 'SHORT_CE', strike: 24200, short: true });
        expect(legInfo(sig({ reason: '[LONG_PE 23700] IC ENTRY' }))).toEqual({ leg: 'LONG_PE', strike: 23700, short: false });
        expect(legInfo(sig({ leg: 'SHORT_PE', strike: 23800, short: true }))).toEqual({ leg: 'SHORT_PE', strike: 23800, short: true });
        expect(legInfo(sig({ reason: 'RANGE CALL entry' }))).toBeNull();
    });

    it('keeps four open legs apart and flags shorts', () => {
        const signals = [
            sig({ token: '301', reason: '[LONG_CE 24300] IC ENTRY', price: 4000 }),
            sig({ token: '302', reason: '[SHORT_CE 24200] IC ENTRY', price: 10000 }),
            sig({ token: '303', side: 'PUT', reason: '[LONG_PE 23700] IC ENTRY', price: 4000 }),
            sig({ token: '304', side: 'PUT', reason: '[SHORT_PE 23800] IC ENTRY', price: 10000 }),
        ].reverse(); // store is newest-first
        const open = buildOpenOrders(signals, {
            [buildLiveOrderKey('NIFTY50_RANGE_IC', 'CALL', 'SHORT_CE')]: {
                strategy_name: 'NIFTY50_RANGE_IC', side: 'CALL', leg: 'SHORT_CE', short: true, current_fno_price: 6000,
            },
        });
        expect(open).toHaveLength(4);
        const shortCE = open.find(o => o.leg === 'SHORT_CE')!;
        expect(shortCE.short).toBe(true);
        expect(shortCE.strike).toBe(24200);
        expect(shortCE.currentPrice).toBe(60);
    });

    it('computes a short leg move as entry minus exit', () => {
        const signals = [
            sig({ token: '302', reason: '[SHORT_CE 24200] IC ENTRY', price: 10000 }),
            sig({ token: '302', action: 'EXIT', reason: '[SHORT_CE 24200] IC PROFIT TARGET', price: 6000 }),
            sig({ token: '301', reason: '[LONG_CE 24300] IC ENTRY', price: 4000 }),
            sig({ token: '301', action: 'EXIT', reason: '[LONG_CE 24300] IC PROFIT TARGET', price: 3000 }),
        ].reverse();
        const events = buildCompletedEvents(signals);
        const byLeg = Object.fromEntries(events.map(e => [e.leg, e.move]));
        expect(byLeg.SHORT_CE).toBe(40);
        expect(byLeg.LONG_CE).toBe(-10);
    });

    it('leaves single-leg pairing unchanged', () => {
        const signals = [
            sig({ strategy: 'NIFTY50_RANGE', token: '99926000', exchange: 'NSE', reason: 'RANGE entry', price: 10000 }),
            sig({ strategy: 'NIFTY50_RANGE', token: '99926000', exchange: 'NSE', action: 'EXIT', reason: 'RANGE TARGET', price: 12000 }),
        ].reverse();
        const [e] = buildCompletedEvents(signals);
        expect(e.move).toBe(20);
        expect(e.leg).toBeUndefined();
    });
});
