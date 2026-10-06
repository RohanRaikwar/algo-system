import { describe, expect, it } from 'vitest';
import { asOf, boxLine, clock, dayPicture, nearestN, reasonWarnings, situationLine, warnSummary, hhmm, nearestLevels, peekSummary, pnlPaise, points, rupees, watching } from './srFormat';
import type { SRView } from '../../types/sr';

const base: SRView = {
    strategy: 'NIFTY50_SR', key: 'NSE:99926000', ts: '', regime: 'RANGE', adx: 18, rsi5: 45, close: 2280000,
    levels: [
        { price: 2270000, touches: 3, source: 'SWING' },
        { price: 2275000, touches: 2, source: 'PREV_DAY' },
        { price: 2290000, touches: 2, source: 'OPENING' },
    ],
    side: 'NONE', rejects: null, trades_today: 1, max_trades: 3, consec_losses: 0, day_pnl_pts: -640,
    cooldown_left: 0, entry_tf: 5, entry_from_min: 570, entry_to_min: 870, min_confirmations: 2,
    fade: true, retest: true, pullback: true,
};

describe('SR panel formatting', () => {
    it('formats prices, points and times', () => {
        expect(rupees(2278490)).toBe('22,784.90');
        expect(rupees(0)).toBe('—');
        expect(points(-3450, true)).toBe('−34.5 pts');
        expect(hhmm(900)).toBe('15:00');
        expect(clock('0001-01-01T00:00:00Z')).toBe('');
        expect(clock('2026-10-01T06:28:00Z')).toBe('11:58');
    });

    it('picks the nearest support and resistance', () => {
        const { support, resistance } = nearestLevels(base.levels, 2280000);
        expect(support?.price).toBe(2275000);
        expect(resistance?.price).toBe(2290000);
        expect(nearestLevels(base.levels, 2260000).support).toBeUndefined();
        expect(nearestLevels(null, 2280000)).toEqual({});
    });

    it('describes what the strategy waits for', () => {
        expect(watching({ ...base, side: 'CALL' }, 2280000).text).toContain('position');
        expect(watching({ ...base, regime: 'WARMING' }, 2280000).text).toContain('Warming');
        expect(watching({ ...base, block: 'trade_cap' }, 2280000)).toEqual({ kind: 'blocked', text: 'Blocked — daily trade cap reached' });
        const armed = watching({ ...base, pending_side: 'PUT', pending_level: 2275000, pending_left: 1 }, 2280000);
        expect(armed.kind).toBe('pending');
        expect(armed.text).toContain('22,750.00');
        expect(armed.text).toContain('1 bar left');
        expect(watching({ ...base, regime: 'DEAD' }, 2280000).text).toContain('dead');
        expect(watching(base, 2280000).text).toBe('Waiting for a setup at support 22,750.00 / resistance 22,900.00');
        expect(watching({ ...base, levels: [] }, 2280000).text).toBe('No levels yet');
    });

    it('computes open-position P&L from the last price', () => {
        expect(pnlPaise({ ...base, side: 'CALL', entry: 2280000 }, 2281240)).toBe(1240);
        expect(pnlPaise({ ...base, side: 'PUT', entry: 2280000 }, 2281240)).toBe(-1240);
        expect(pnlPaise(base, 2281240)).toBeNull();
        expect(pnlPaise({ ...base, side: 'CALL', entry: 2280000 }, null)).toBeNull();
    });

    it('formats as-of time as the bar close in IST', () => {
        expect(asOf('2026-10-01T06:28:00Z')).toBe('11:59');
        expect(asOf('')).toBe('');
    });

    it('summarises state for the phone peek strip', () => {
        const flat = peekSummary(base, null);
        expect(flat.position).toEqual({ text: 'Flat', tone: 'muted' });
        expect(flat.alert).toBeNull();
        expect(flat.stats.map(s => s.value)).toEqual(['18.0', '45', '−6.4 pts']);

        const long = peekSummary({ ...base, side: 'CALL', entry: 2280000 }, 2281240);
        expect(long.position).toEqual({ text: 'CALL +12.4', tone: 'up' });

        const armed = peekSummary({ ...base, pending_side: 'PUT', pending_level: 2275000, pending_left: 2 }, null);
        expect(armed.alert).toContain('waiting for a retest');
    });
});

describe('day picture', () => {
    const v: SRView = {
        ...base, day_open: 2270000, day_high: 2300000, day_low: 2272000,
        prev_day_high: 2282000, prev_day_low: 2260000, or_high: 2276000, or_low: 2272000,
        day_extreme_pct: 80, day_run_pts: 6000, day_extreme_puts: false,
    };

    it('matches the strategy order reason numbers', () => {
        const d = dayPicture(v, 2283500)!; // same as the Go test: 41%, −165 / +115
        expect(d.pct).toBe(41);
        expect(d.parts).toEqual([
            '41% of day range', '165 pts below high', '115 pts above low',
            '+15 pts vs PDH', '+235 pts vs PDL', '+135 pts from open', 'above OR',
        ]);
        expect(d.blocked).toBeNull();
    });

    it('flags a blocked CALL at the top after a run, PUT only with the mirror on', () => {
        expect(dayPicture(v, 2299000)!.blocked).toBe('CALL');
        expect(dayPicture(v, 2272500)!.blocked).toBeNull();
        expect(dayPicture({ ...v, day_extreme_puts: true }, 2272500)!.blocked).toBe('PUT');
        expect(dayPicture({ ...v, day_extreme_pct: 0 }, 2299000)!.blocked).toBeNull();
        expect(dayPicture({ ...v, day_high: undefined }, 2283500)).toBeNull();
    });
});

describe('box line', () => {
    it('says sideways when the box is within the limit', () => {
        expect(boxLine({ ...base, box_pts: 2800, box_limit: 3000, box_bars: 6 }))
            .toEqual({ boxed: true, text: 'Sideways: 28 pts in 30 min (sideways ≤ 30 pts) — ⚠ pullback/retest flagged' });
        expect(boxLine({ ...base, box_pts: 4800, box_limit: 3000, box_bars: 6 })!.boxed).toBe(false);
        expect(boxLine(base)).toBeNull();
    });
});

describe('chart situation helpers', () => {
    const levels = [21, 22, 23, 24, 25, 26, 27, 28].map(p => ({ price: p * 100000, touches: 2, source: 'SWING' }));

    it('picks the nearest 3 supports and resistances, closest first', () => {
        const { supports, resistances } = nearestN(levels, 2450000, 3);
        expect(supports.map(l => l.price / 100000)).toEqual([24, 23, 22]);
        expect(resistances.map(l => l.price / 100000)).toEqual([25, 26, 27]);
        expect(nearestN(null, 1, 3)).toEqual({ supports: [], resistances: [] });
    });

    it('reads and summarises warnings', () => {
        expect(reasonWarnings('SR PULLBACK CALL ... close=2283500 warn=sideways,day_extreme')).toEqual(['sideways', 'day_extreme']);
        expect(reasonWarnings('SR FADE CALL close=1')).toEqual([]);
        expect(warnSummary(['call:day_extreme', 'call:sideways', 'put:sideways'])).toBe('CALL: day extreme, sideways · PUT: sideways');
        expect(warnSummary(null)).toBe('');
    });

    it('builds the situation line', () => {
        const v: SRView = { ...base, regime: 'TREND_UP', day_high: 2300000, day_low: 2272000, box_pts: 3500, box_bars: 6, boxed: true };
        expect(situationLine(v, 2297200)).toBe('SR · Trend up · 90% of day · Sideways 35 pts/30m');
    });
});
