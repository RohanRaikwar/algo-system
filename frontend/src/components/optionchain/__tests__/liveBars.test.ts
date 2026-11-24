import { describe, expect, it } from 'vitest';
import { applyTick, rupees } from '../liveBars';
import type { OptBar } from '../../../services/optionChain';

describe('option chart live bars', () => {
    it('updates the current bar and opens the next one', () => {
        const bars: OptBar[] = [{ t: 600, o: 100, h: 110, l: 90, c: 105, v: 5 }];
        applyTick(bars, 630, 120, 60);
        expect(bars[0]).toMatchObject({ h: 120, c: 120, l: 90 });
        applyTick(bars, 661, 80, 60);
        expect(bars).toHaveLength(2);
        expect(bars[1]).toEqual({ t: 660, o: 80, h: 80, l: 80, c: 80, v: 0 });
    });

    it('ignores old ticks and zero prices', () => {
        const bars: OptBar[] = [{ t: 600, o: 100, h: 100, l: 100, c: 100, v: 0 }];
        expect(applyTick(bars, 500, 90, 60)).toBeNull();
        expect(applyTick(bars, 620, 0, 60)).toBeNull();
        expect(bars).toHaveLength(1);
    });

    it('buckets by timeframe', () => {
        const bars: OptBar[] = [];
        applyTick(bars, 300 * 10 + 10, 100, 300);
        applyTick(bars, 300 * 10 + 290, 140, 300);
        expect(bars).toHaveLength(1);
        expect(bars[0]).toMatchObject({ t: 3000, o: 100, h: 140, c: 140 });
    });

    it('formats paise', () => {
        expect(rupees(12345)).toBe('123.45');
        expect(rupees(0)).toBe('—');
    });
});
