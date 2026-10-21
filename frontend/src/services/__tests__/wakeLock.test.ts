import { describe, expect, it, vi } from 'vitest';
import { holdWakeLock, keepAwakeWanted, saveKeepAwake, wakeLockSupported } from '../wakeLock';

function fakes(visible = true) {
    const listeners: Array<() => void> = [];
    const doc = {
        visibilityState: visible ? 'visible' : 'hidden',
        addEventListener: (_: string, f: () => void) => listeners.push(f),
        removeEventListener: vi.fn(),
    } as unknown as Document & { visibilityState: string };
    let onRelease: () => void = () => { };
    const sentinel = {
        released: false,
        release: vi.fn(async () => { sentinel.released = true; onRelease(); }),
        addEventListener: (_: string, f: () => void) => { onRelease = f; },
    };
    const request = vi.fn(async () => { sentinel.released = false; return sentinel; });
    const nav = { wakeLock: { request } } as unknown as Navigator;
    return { doc, nav, request, sentinel, show: () => { (doc as { visibilityState: string }).visibilityState = 'visible'; listeners.forEach(f => f()); } };
}

const flush = () => new Promise(r => setTimeout(r, 0));

describe('screen wake lock', () => {
    it('holds the lock while shown and takes it again after the page comes back', async () => {
        const f = fakes();
        const held: boolean[] = [];
        const stop = holdWakeLock(h => held.push(h), f.nav, f.doc);
        await flush();
        expect(f.request).toHaveBeenCalledWith('screen');
        expect(held[held.length - 1]).toBe(true);

        // The browser drops the lock when the page is hidden.
        f.sentinel.released = true;
        f.show();
        await flush();
        expect(f.request).toHaveBeenCalledTimes(2);

        stop();
        expect(f.sentinel.release).toHaveBeenCalled();
        expect(held[held.length - 1]).toBe(false);
    });

    it('waits for a hidden page and reports no support', async () => {
        const f = fakes(false);
        holdWakeLock(() => { }, f.nav, f.doc);
        await flush();
        expect(f.request).not.toHaveBeenCalled();
        expect(wakeLockSupported({} as Navigator)).toBe(false);
        const held: boolean[] = [];
        holdWakeLock(h => held.push(h), {} as Navigator, f.doc);
        expect(held).toEqual([false]);
    });

    it('defaults on in the installed app and remembers the choice', () => {
        const m = new Map<string, string>();
        vi.stubGlobal('localStorage', {
            getItem: (k: string) => m.get(k) ?? null,
            setItem: (k: string, v: string) => { m.set(k, v); },
            removeItem: (k: string) => { m.delete(k); },
        });
        expect(keepAwakeWanted(true)).toBe(true);
        expect(keepAwakeWanted(false)).toBe(false);
        saveKeepAwake(false);
        expect(keepAwakeWanted(true)).toBe(false);
        vi.unstubAllGlobals();
    });
});
