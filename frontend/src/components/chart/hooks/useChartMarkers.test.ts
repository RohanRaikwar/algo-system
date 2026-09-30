import { describe, expect, it } from 'vitest';
import { buildChartMarkers, levelsLine, reasonLines } from './useChartMarkers';
import { layoutCards, type CardRequest } from '../tradeMarkersPrimitive';
import { oldestCandleMs } from './useChartLazyLoad';
import { mergeSignalLists, useSignalStore } from '../../../store/useSignalStore';
import type { SignalPayload } from '../../../types/signal';
import type { CandleRaw } from '../../../store/useCandleStore';
import { parseRefusedView } from '../../../store/useRefusedStore';
import { channelKind, snapshotLiveUpdates } from '../../../utils/seqTracker';
import { IST_OFFSET } from '../../../utils/helpers';
import type { SignalRecord } from '../../../types/signal';
import type { RefusedEntry } from '../../../types/refused';

const refused: RefusedEntry = {
    strategy: 'NIFTY50_SR', side: 'PUT', token: '99926000', exchange: 'NSE', strike: 22600,
    reason: 'SR strike: no greeks', strategy_reason: 'SR PULLBACK PUT', ts: '2026-09-29T05:16:01Z',
};
const entry = {
    id: 1, strategy: 'NIFTY50_SR', action: 'BUY', side: 'CALL', token: '99926000', exchange: 'NSE',
    reason: '', ema_values: '', candle_ts: '2026-09-29T05:10:00Z', created_at: '2026-09-29T05:10:00Z',
} as SignalRecord;

describe('buildChartMarkers', () => {
    it('marks refused entries on their TF bucket', () => {
        const m = buildChartMarkers([entry], [refused], 'NSE:99926000', 60, false);
        expect(m).toHaveLength(2);
        const r = m[1];
        expect(r.kind).toBe('refused');
        expect(r.label).toBe('REFUSED PUT');
        const sec = Date.parse(refused.ts) / 1000 + IST_OFFSET;
        expect(r.time).toBe(Math.floor(sec / 60) * 60);
        expect(m[0]).toMatchObject({ kind: 'entry', label: 'BUY CALL', side: 'C' }); // sorted by time
    });

    it('skips other instruments and uses compact text on phones', () => {
        expect(buildChartMarkers([], [refused], 'NFO:40712', 60, false)).toHaveLength(0);
        expect(buildChartMarkers([], [refused], '99926000', 60, true)[0]).toMatchObject({ label: '', side: 'P' });
    });
});

describe('card lines', () => {
    const buy = { ...entry, side: 'PUT', price: 12050, qty: 75, strike: 22700 } as SignalRecord;
    const exit = {
        ...buy, id: 2, action: 'EXIT', price: 13575,
        candle_ts: '2026-09-29T06:10:00Z', created_at: '2026-09-29T06:10:00Z',
    } as SignalRecord;

    it('shows premium, strike, qty on entries and P&L on the paired exit', () => {
        // Store order is newest-first; pairing must still run oldest-first.
        const [b, e] = buildChartMarkers([exit, buy], [], 'NSE:99926000', 60, false);
        expect(b.lines).toEqual(['NIFTY50_SR · 10:40:00', '@ ₹120.50  22700P', 'qty 75']);
        expect(e.lines).toEqual(['NIFTY50_SR · 11:40:00', '@ ₹135.75', 'P&L +₹1,143.75']);
        expect(e.pnl).toBe((13575 - 12050) * 75);
    });

    it('puts both reasons on refused cards and drops lines in compact mode', () => {
        expect(buildChartMarkers([], [refused], 'NSE:99926000', 60, false)[0].lines)
            .toEqual(['NIFTY50_SR · 10:46:01 · 22600P', 'SR PULLBACK PUT', 'Blocked: SR strike: no greeks']);
        const compact = buildChartMarkers([exit, buy], [refused], 'NSE:99926000', 60, true);
        expect(compact.every(m => m.lines.length === 0)).toBe(true);
    });
});

describe('levelsLine', () => {
    it('reads paise levels from the reason as rupees', () => {
        const reason = 'SR FADE PUT regime=RANGE level=2273620 stop=2277540 target=2266332 vwap=2272499';
        expect(levelsLine(reason)).toBe('LVL 22736.20  SL 22775.40  TGT 22663.32');
        expect(levelsLine('EMA cross up')).toBe('');
    });
});

describe('reasonLines', () => {
    it('wraps the full reason to at most four rows', () => {
        const reason = '[LONG_PE 22700] SR FADE PUT regime=RANGE level=2273620 stop=2277540 target=2266332 vwap=2272499 ema=2274505/2275132 rsi5=45.1 adx=18.5 vwap=Y ema=Y rsi=n confirm=wick session=midday trend=down';
        const rows = reasonLines(reason);
        expect(rows).toHaveLength(4);
        expect(rows[0]).toBe('SR FADE PUT regime=RANGE level=2273620');
        expect(rows.every(r => r.length <= 40)).toBe(true);
        expect(rows[3].endsWith('…')).toBe(true);
        expect(rows.join(' ')).not.toMatch(/LONG_PE/);
        expect(reasonLines('EMA cross up')).toEqual(['EMA cross up']);
        expect(reasonLines('')).toEqual([]);
    });
});

describe('mergeSignalLists', () => {
    it('keeps live signals REST lacks, dedupes shared ones, newest first', () => {
        const older = { ...entry, id: 7 } as SignalRecord;
        const liveCopy = { ...entry, id: 999, candle_ts: '2026-09-29T05:10:00.000Z' } as SignalRecord;
        const liveOnly = {
            ...entry, id: 1000, action: 'EXIT',
            candle_ts: '2026-09-29T06:00:00Z', created_at: '2026-09-29T06:00:00Z',
        } as SignalRecord;
        const merged = mergeSignalLists([liveOnly, liveCopy], [older]);
        expect(merged.map(s => s.id)).toEqual([1000, 7]);
    });
});

describe('layoutCards', () => {
    const hit = (a: { left: number; top: number; w: number; h: number }, b: typeof a) =>
        a.left < b.left + b.w && b.left < a.left + a.w && a.top < b.top + b.h && b.top < a.top + a.h;

    it('clears candles under the card span, not only its own bar', () => {
        const bars = [80, 100, 120, 140].map(x => ({ x, top: x === 100 ? 300 : 200, bottom: 580 }));
        const [{ rect, above }] = layoutCards([{ x: 100, y: 300, above: true, w: 60, h: 40 }], bars, 800, 600);
        expect(above).toBe(true);
        for (const b of bars) {
            if (b.x >= rect.left && b.x <= rect.left + rect.w) expect(rect.top + rect.h).toBeLessThan(b.top);
        }
        expect(rect.top + rect.h).toBeLessThan(200);
    });

    it('keeps cards on one bar apart', () => {
        const req: CardRequest = { x: 400, y: 300, above: true, w: 80, h: 46 };
        const out = layoutCards([req, req, req], [{ x: 400, top: 300, bottom: 330 }], 800, 600);
        for (let i = 0; i < out.length; i++) {
            for (let j = i + 1; j < out.length; j++) expect(hit(out[i].rect, out[j].rect)).toBe(false);
        }
    });

    it('flips side when the preferred side has no room', () => {
        const [{ rect, above }] = layoutCards([{ x: 400, y: 30, above: true, w: 80, h: 46 }],
            [{ x: 400, top: 30, bottom: 60 }], 800, 600);
        expect(above).toBe(false);
        expect(rect.top).toBeGreaterThan(60);
    });
});

describe('pub:refused', () => {
    it('parses object and string payloads', () => {
        const view = { date: '2026-09-29', entries: [refused] };
        expect(parseRefusedView(view)?.entries).toHaveLength(1);
        expect(parseRefusedView(JSON.stringify(view))?.entries[0].reason).toBe('SR strike: no greeks');
        expect(parseRefusedView({ date: 'x' })).toBeNull();
        expect(parseRefusedView('not json')).toBeNull();
    });

    it('is a full-state channel carried by SNAPSHOT', () => {
        expect(channelKind('pub:refused')).toBe('full_state');
        const ups = snapshotLiveUpdates({ refused: { date: 'd', entries: [] }, liveSeqs: { 'pub:refused': 3 } }, {});
        expect(ups).toEqual([{ channel: 'pub:refused', data: { date: 'd', entries: [] }, seq: 3 }]);
    });
});

describe('addLiveSignal', () => {
    it('drops a redelivered signal so its marker is not doubled', () => {
        useSignalStore.setState({ signals: [], unreadCount: 0 });
        const sig = {
            strategy_name: 'NIFTY50_SR', action: 'BUY', side: 'CALL', token: '99926000', exchange: 'NSE',
            qty: 1, price: 100, reason: '', ts: '2026-09-29T05:10:00Z',
        } as SignalPayload;
        const add = useSignalStore.getState().addLiveSignal;
        add(sig);
        add({ ...sig, ts: '2026-09-29T05:10:00.000Z' }); // replay, other spelling
        add({ ...sig, action: 'EXIT' });
        expect(useSignalStore.getState().signals.map(s => s.action)).toEqual(['EXIT', 'BUY']);
        expect(useSignalStore.getState().unreadCount).toBe(2);
    });
});

describe('oldestCandleMs', () => {
    it('finds the selected instrument\'s earliest candle in a mixed, unordered store', () => {
        const c = (ts: string, token: string) => ({ ts, token, exchange: 'NSE' }) as CandleRaw;
        const arr = [c('2026-09-30T04:01:00Z', '99926000'), c('2026-09-28T04:00:00Z', '40712'), c('2026-09-29T04:00:00Z', '99926000')];
        expect(oldestCandleMs(arr, 'NSE:99926000')).toBe(Date.parse('2026-09-29T04:00:00Z'));
        expect(oldestCandleMs(arr, 'NSE:1')).toBeNull();
    });
});
