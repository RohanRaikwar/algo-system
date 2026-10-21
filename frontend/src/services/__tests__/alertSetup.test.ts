import { describe, expect, it, vi } from 'vitest';
import { isAndroid, markAlertSetupDone, showAlertSetupAtStart } from '../alertSetup';

const ANDROID = 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/141.0 Mobile Safari/537.36';
const DESKTOP = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/141.0 Safari/537.36';

describe('alert setup at start', () => {
    it('shows on Android until finished, never on desktop', () => {
        const m = new Map<string, string>();
        vi.stubGlobal('localStorage', { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => { m.set(k, v); } });
        expect(isAndroid(ANDROID)).toBe(true);
        expect(showAlertSetupAtStart(DESKTOP)).toBe(false);
        expect(showAlertSetupAtStart(ANDROID)).toBe(true);
        markAlertSetupDone();
        expect(showAlertSetupAtStart(ANDROID)).toBe(false);
        vi.unstubAllGlobals();
    });
});
