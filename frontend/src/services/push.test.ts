import { afterEach, describe, expect, it, vi } from 'vitest';
import { canPromptWithoutGesture, initPushOnOpen, sameKey, urlBase64ToUint8Array } from './push';

describe('urlBase64ToUint8Array', () => {
    it('decodes unpadded base64url', () => {
        // "hi?" -> base64 "aGk/" -> base64url "aGk_"
        expect(Array.from(urlBase64ToUint8Array('aGk_'))).toEqual([104, 105, 63]);
        // "hi" -> "aGk=" -> unpadded "aGk"
        expect(Array.from(urlBase64ToUint8Array('aGk'))).toEqual([104, 105]);
    });
});

describe('sameKey', () => {
    it('compares bytes', () => {
        expect(sameKey(new Uint8Array([1, 2]), new Uint8Array([1, 2]))).toBe(true);
        expect(sameKey(new Uint8Array([1, 2]), new Uint8Array([1, 3]))).toBe(false);
        expect(sameKey(new Uint8Array([1]), new Uint8Array([1, 2]))).toBe(false);
    });
});

describe('canPromptWithoutGesture', () => {
    it('is true only for desktop/Android Chromium', () => {
        expect(canPromptWithoutGesture('Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/128.0 Mobile Safari/537.36')).toBe(true);
        expect(canPromptWithoutGesture('Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/128.0 Safari/537.36 Edg/128.0')).toBe(true);
        expect(canPromptWithoutGesture('Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0')).toBe(false);
        expect(canPromptWithoutGesture('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 CriOS/128.0 Mobile Safari/604.1')).toBe(false);
        expect(canPromptWithoutGesture('Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15')).toBe(false);
    });
});

describe('initPushOnOpen', () => {
    const CHROME = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/128.0 Safari/537.36';
    const FIREFOX = 'Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0';
    let clickHandlers: Array<() => void>;

    function setup(permission: NotificationPermission, ua: string, answer: NotificationPermission = 'default') {
        clickHandlers = [];
        const requestPermission = vi.fn(async () => answer);
        const N = { permission, requestPermission };
        vi.stubGlobal('Notification', N);
        vi.stubGlobal('window', { Notification: N, PushManager: {}, matchMedia: () => ({ matches: false }) });
        vi.stubGlobal('navigator', { userAgent: ua, maxTouchPoints: 0, serviceWorker: {} });
        vi.stubGlobal('document', {
            addEventListener: (_: string, h: () => void) => clickHandlers.push(h),
            removeEventListener: (_: string, h: () => void) => { clickHandlers = clickHandlers.filter(x => x !== h); },
        });
        // 503 from the gateway: push not configured, stops before subscribe.
        vi.stubGlobal('fetch', vi.fn(async () => ({ status: 503, ok: false })));
        return requestPermission;
    }

    afterEach(() => vi.unstubAllGlobals());

    it('asks immediately on Chromium', async () => {
        const req = setup('default', CHROME);
        const states: string[] = [];
        initPushOnOpen(s => states.push(s));
        expect(req).toHaveBeenCalledTimes(1);
        await vi.waitFor(() => expect(states).toEqual(['off']));
        expect(clickHandlers).toHaveLength(0);
    });

    it('waits for the first tap on Firefox, once', async () => {
        const req = setup('default', FIREFOX, 'granted');
        const states: string[] = [];
        initPushOnOpen(s => states.push(s));
        expect(req).not.toHaveBeenCalled();
        expect(clickHandlers).toHaveLength(1);
        clickHandlers[0]();
        expect(req).toHaveBeenCalledTimes(1);
        expect(clickHandlers).toHaveLength(0);
        await vi.waitFor(() => expect(states).toEqual(['off', 'disabled']));
    });

    it('does not ask when already granted or denied', async () => {
        let req = setup('granted', CHROME);
        const states: string[] = [];
        initPushOnOpen(s => states.push(s));
        expect(req).not.toHaveBeenCalled();
        await vi.waitFor(() => expect(states).toEqual(['disabled']));

        req = setup('denied', FIREFOX);
        initPushOnOpen(s => states.push(s));
        expect(req).not.toHaveBeenCalled();
        expect(clickHandlers).toHaveLength(0);
        expect(states[states.length - 1]).toBe('denied');
    });

    it('cleanup removes a pending tap listener', () => {
        setup('default', FIREFOX);
        const stop = initPushOnOpen(() => { });
        stop();
        expect(clickHandlers).toHaveLength(0);
    });
});
