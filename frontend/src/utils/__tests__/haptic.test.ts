import { afterEach, describe, expect, it, vi } from 'vitest';
import { haptic } from '../haptic';

describe('haptic', () => {
    afterEach(() => vi.unstubAllGlobals());

    it('is a no-op without the Vibration API', () => {
        vi.stubGlobal('navigator', {});
        expect(() => haptic()).not.toThrow();
    });

    it('vibrates when supported', () => {
        const vibrate = vi.fn();
        vi.stubGlobal('navigator', { vibrate });
        haptic(15);
        expect(vibrate).toHaveBeenCalledWith(15);
    });

    it('swallows a throwing vibrate', () => {
        vi.stubGlobal('navigator', { vibrate: () => { throw new Error('no gesture'); } });
        expect(() => haptic()).not.toThrow();
    });
});
