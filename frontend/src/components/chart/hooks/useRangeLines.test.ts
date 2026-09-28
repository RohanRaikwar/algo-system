import { describe, expect, it } from 'vitest';
import { rangeLines } from './useRangeLines';
import type { RangeView } from '../../../types/range';

const base: RangeView = {
    strategy: 'NIFTY50_RANGE', key: 'NSE:99926000', ts: '', regime: 'TRENDING', adx: 40, max_adx: 30, rsi5: 40,
    flag_tight: true, flag_trend_day: true, day_trend: -30000, flag_box_lo: 2281820, flag_box_hi: 2284870,
    side: 'NONE', trades_today: 0, max_trades: 4, entry_tf: 5, mean_reversion: false, breakout: true, flag: true, time_exit_min: 900,
};

describe('rangeLines', () => {
    it('draws the flag box on a trend day', () => {
        expect(rangeLines(base).map(l => [l.title, l.price])).toEqual([['Flag high', 22848.7], ['Flag low', 22818.2]]);
    });
    it('draws support/resistance in a range', () => {
        const t = rangeLines({ ...base, regime: 'RANGE', support: 2280000, resistance: 2290000 }).map(l => l.title);
        expect(t).toEqual(['Support', 'Resistance']);
    });
    it('shows only entry, stop and target while in a position', () => {
        const t = rangeLines({ ...base, side: 'PUT', entry: 2279765, stop: 2284320, target: 2266000 }).map(l => l.title);
        expect(t).toEqual(['Entry PUT', 'Stop', 'Target']);
    });
    it('marks an armed break', () => {
        expect(rangeLines({ ...base, pending_side: 'PUT', pending_edge: 2281820 })[0].title).toBe('Armed PUT');
        expect(rangeLines(null)).toEqual([]);
    });
});
