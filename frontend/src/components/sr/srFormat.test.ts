import { describe, expect, it } from 'vitest';
import { asOf, hhmm, peekSummary, pnlPaise, points, rupees, watching } from './rangeFormat';
import type { RangeView } from '../../types/range';

const base: RangeView = {
    strategy: 'NIFTY50_RANGE', key: 'NSE:99926000', ts: '', regime: 'TRENDING', adx: 40, max_adx: 30, rsi5: 40,
    flag_tight: false, flag_trend_day: false, day_trend: 0, side: 'NONE', trades_today: 0, max_trades: 4,
    entry_tf: 5, mean_reversion: false, breakout: true, flag: true, time_exit_min: 900,
};

describe('range panel formatting', () => {
    it('formats prices, points and times', () => {
        expect(rupees(2278490)).toBe('22,784.90');
        expect(rupees(0)).toBe('—');
        expect(points(-3450, true)).toBe('−34.5 pts');
        expect(hhmm(900)).toBe('15:00');
    });

    it('describes what the strategy waits for', () => {
        expect(watching({ ...base, pending_side: 'PUT', pending_kind: 'FLAG', pending_edge: 2281820 }).text)
            .toContain('Flag PUT armed below 22,818.20');
        const flag = watching({ ...base, flag_box_lo: 2281820, flag_box_hi: 2284870, flag_tight: true, flag_trend_day: true, day_trend: -30000 });
        expect(flag.kind === 'flag' && flag.ready).toBe(true);
        expect(flag.text).toContain('below 22,818.20');
        expect(watching({ ...base, flag_box_lo: 1, flag_box_hi: 2, day_trend: 5000 }).text).toContain('needs 100');
        expect(watching({ ...base, regime: 'RANGE', support: 2280000, resistance: 2290000 }).text).toContain('22,800.00');
        expect(watching({ ...base, side: 'CALL' }).text).toContain('position');
        expect(watching({ ...base, regime: 'WARMING', flag: false }).text).toContain('Warming');
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
        const flat = peekSummary({ ...base, day_open: 2270000, day_trend: -640 }, null);
        expect(flat.position).toEqual({ text: 'Flat', tone: 'muted' });
        expect(flat.alert).toBeNull();
        expect(flat.stats.map(s => s.value)).toEqual(['40.0', '40', '−6.4 pts']);

        const long = peekSummary({ ...base, side: 'CALL', entry: 2280000 }, 2281240);
        expect(long.position).toEqual({ text: 'CALL +12.4', tone: 'up' });

        const armed = peekSummary({ ...base, pending_side: 'PUT', pending_kind: 'FLAG', pending_edge: 2281820 }, null);
        expect(armed.alert).toContain('armed below');
    });
});
