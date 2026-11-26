import { describe, expect, it } from 'vitest';
import { buildChartMarkers } from '../hooks/useChartMarkers';
import { compactMarker, markersAt, type Rect, type TradeMarker } from '../tradeMarkersPrimitive';
import { markerRows, markerTitle } from '../MarkerDetails';
import type { SignalRecord } from '../../../types/signal';
import type { RefusedEntry } from '../../../types/refused';

const longReason = 'SR PULLBACK PUT regime=TREND_DOWN close=2271270 stop=2275540 target=2266332 '
    + 'rejected the resistance twice on the 5m chart and closed below the fast EMA with volume rising';

const buy = {
    id: 1, strategy: 'NIFTY50_SR', action: 'BUY', side: 'PUT', token: '99926000', exchange: 'NSE',
    price: 12050, qty: 75, strike: 22700, reason: longReason, ema_values: '',
    candle_ts: '2026-09-29T05:10:00Z', created_at: '2026-09-29T05:10:00Z',
} as SignalRecord;
const exit = {
    ...buy, id: 2, action: 'EXIT', price: 13575, reason: 'target hit',
    candle_ts: '2026-09-29T06:10:00Z', created_at: '2026-09-29T06:10:00Z',
} as SignalRecord;
const refused: RefusedEntry = {
    strategy: 'NIFTY50_SR', side: 'CALL', token: '99926000', exchange: 'NSE', strike: 22600,
    reason: 'SR strike: no greeks', strategy_reason: 'SR PULLBACK CALL', ts: '2026-09-29T05:16:01Z',
};

describe('compact markers keep their details for the tap popup', () => {
    it('keeps title, detail rows and the full reason on phones', () => {
        const [b, , e] = buildChartMarkers([exit, buy], [refused], 'NSE:99926000', 60, true);
        expect(b.label).toBe('');
        expect(b.lines).toEqual([]);
        expect(b.title).toBe('BUY PUT');
        expect(b.detail).toContain('@ ₹120.50  22700P');
        expect(b.detail).toContain('qty 75');
        expect(b.detail).toContain('SL 22755.40  TGT 22663.32');
        expect(b.reason).toBe(longReason);
        expect(e.title).toBe('EXIT PUT');
        expect(e.detail).toContain('P&L +₹1,143.75');
    });

    it('gives refused entries both reasons in full', () => {
        const r = buildChartMarkers([], [refused], 'NSE:99926000', 60, true)[0];
        expect(r.title).toBe('REFUSED CALL');
        expect(r.reason).toBe('SR PULLBACK CALL\nBlocked: SR strike: no greeks');
    });

    it('desktop cards are unchanged and carry the reason too', () => {
        const [b] = buildChartMarkers([buy], [], 'NSE:99926000', 60, false);
        expect(b.label).toBe('BUY PUT');
        expect(b.lines.length).toBeGreaterThan(0);
        expect(b.reason).toBe(longReason);
    });

    it('compactMarker keeps title, detail and reason', () => {
        const m: TradeMarker = { time: 1, kind: 'exit', label: 'EXIT CALL', side: 'C', lines: ['a', 'b'], reason: 'why' };
        expect(compactMarker(m)).toMatchObject({ label: '', lines: [], title: 'EXIT CALL', detail: ['a', 'b'], reason: 'why' });
    });
});

describe('markersAt', () => {
    const mk = (time: number): TradeMarker => ({ time, kind: 'entry', label: '', side: 'C', lines: [] });
    const rect = (left: number, top: number): Rect => ({ left, top, w: 20, h: 20 });
    const placed = [
        { rect: rect(100, 100), m: mk(10) },
        { rect: rect(110, 110), m: mk(20) },
        { rect: rect(300, 300), m: mk(30) },
    ];

    it('hits a card, its touch margin, and nothing outside', () => {
        expect(markersAt(placed, 305, 305).map(m => m.time)).toEqual([30]);
        expect(markersAt(placed, 326, 305).map(m => m.time)).toEqual([30]); // 6px right of the card
        expect(markersAt(placed, 340, 305)).toEqual([]);
        expect(markersAt(placed, 10, 10)).toEqual([]);
    });

    it('returns every overlapping card, newest first', () => {
        expect(markersAt(placed, 115, 115).map(m => m.time)).toEqual([20, 10]);
    });
});

describe('popup rows', () => {
    it('drops the clipped reason rows when the full reason is shown', () => {
        const [b] = buildChartMarkers([buy], [], 'NSE:99926000', 60, true);
        const rows = markerRows(b);
        expect(rows[0]).toBe('NIFTY50_SR · 10:40:00');
        expect(rows).toContain('qty 75');
        expect(rows.some(r => r.includes('rejected the resistance'))).toBe(false);
    });

    it('titles a marker without a label from its kind and side', () => {
        expect(markerTitle({ time: 1, kind: 'sweep', label: '', side: '', lines: [] })).toBe('Sweep');
        expect(markerTitle({ time: 1, kind: 'exit', label: '', side: 'P', lines: [] })).toBe('Exit PUT');
    });
});
