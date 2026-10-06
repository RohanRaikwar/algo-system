import { describe, expect, it } from 'vitest';
import { srLines } from './useSRLines';
import { srBox } from '../srBoxPrimitive';
import type { SRView } from '../../../types/sr';

const v: SRView = {
    strategy: 'NIFTY50_SR', key: 'NSE:99926000', ts: '', regime: 'TREND_UP', adx: 27, rsi5: 60, close: 2271000,
    levels: [
        { price: 2262180, touches: 3, source: 'SWING' }, { price: 2266990, touches: 2, source: 'SWING' },
        { price: 2269315, touches: 2, source: 'OPENING' }, { price: 2255000, touches: 2, source: 'PREV_DAY' },
        { price: 2272815, touches: 2, source: 'SWING' }, { price: 2280000, touches: 2, source: 'SWING' },
    ],
    side: 'NONE', rejects: null, trades_today: 0, max_trades: 3, consec_losses: 0, day_pnl_pts: 0, cooldown_left: 0,
    entry_tf: 1, entry_from_min: 570, entry_to_min: 885, min_confirmations: 3, fade: true, retest: true, pullback: true,
    day_open: 2260325, day_high: 2272815, day_low: 2256160, prev_day_high: 2266910, prev_day_low: 2233625,
};

describe('SR chart lines', () => {
    it('draws 3 supports, the resistances above, and the day lines', () => {
        const titles = srLines(v, 2271000).map(l => `${l.title}@${l.price}`);
        expect(titles).toEqual([
            'S1 opening range×2@22693.15', 'S2 swing×2@22669.9', 'S3 swing×3@22621.8',
            'R1 swing×2@22728.15', 'R2 swing×2@22800',
            'DH@22728.15', 'DL@22561.6', 'DO@22603.25', 'PDH@22669.1', 'PDL@22336.25',
        ]);
    });

    it('adds SR position lines when in a trade', () => {
        const titles = srLines({ ...v, side: 'CALL', entry: 2271270, stop: 2265991, target: 2281828 }, 2271000).map(l => l.title);
        expect(titles.slice(-3)).toEqual(['SR CALL', 'SR stop', 'SR target']);
        expect(srLines(null, 1)).toEqual([]);
    });
});

describe('SR sideways box geometry', () => {
    it('converts times to IST-shifted seconds and paise to rupees', () => {
        const b = srBox({ ...v, box_from: '2026-10-06T07:10:00Z', box_to: '2026-10-06T07:40:00Z', box_high: 2272815, box_low: 2269315, boxed: true });
        expect(b).toEqual({ from: Date.parse('2026-10-06T07:10:00Z') / 1000 + 19800, to: Date.parse('2026-10-06T07:40:00Z') / 1000 + 19800, high: 22728.15, low: 22693.15, boxed: true });
        expect(srBox(v)).toBeNull();
    });
});
