// Web Push client: registers public/sw.js and keeps this browser's push
// subscription in the gateway (backend/internal/push).

const BASE = import.meta.env.VITE_API_URL || '';

export type PushState =
    | 'unsupported'   // no service worker / PushManager (or iOS outside the installed app)
    | 'ios-install'   // iOS Safari tab: push needs "Add to Home Screen" first
    | 'denied'        // user blocked notifications in browser settings
    | 'disabled'      // gateway has no VAPID keys
    | 'off'
    | 'on';

export function urlBase64ToUint8Array(b64: string): Uint8Array {
    const pad = '='.repeat((4 - (b64.length % 4)) % 4);
    const raw = atob((b64 + pad).replace(/-/g, '+').replace(/_/g, '/'));
    return Uint8Array.from(raw, c => c.charCodeAt(0));
}

export function isIOS(ua: string = navigator.userAgent): boolean {
    return /iPad|iPhone|iPod/.test(ua) || (ua.includes('Macintosh') && navigator.maxTouchPoints > 1);
}

export function isStandalone(): boolean {
    return window.matchMedia?.('(display-mode: standalone)').matches
        || (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

function supported(): boolean {
    return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
}

/** Register the service worker. Safe to call on every page load. */
export function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
    if (!('serviceWorker' in navigator)) return Promise.resolve(null);
    const url = `/sw.js${BASE ? `?api=${encodeURIComponent(BASE)}` : ''}`;
    return navigator.serviceWorker.register(url, { scope: '/' }).catch(err => {
        console.warn('[push] service worker registration failed', err);
        return null;
    });
}

async function fetchVapidKey(): Promise<string | null> {
    const res = await fetch(`${BASE}/api/push/vapid-public-key`);
    if (res.status === 503) return null;
    if (!res.ok) throw new Error(`VAPID key fetch failed: ${res.status}`);
    const { key } = await res.json() as { key: string };
    return key;
}

async function postJSON(path: string, body: unknown): Promise<Response> {
    const res = await fetch(`${BASE}${path}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(`${path} failed: ${res.status}`);
    return res;
}

/** Ask permission, subscribe, and store the subscription in the gateway.
 *  requestPermission() is the first call so a click handler keeps its user
 *  activation (Safari and Firefox need it). */
export function enablePush(): Promise<PushState> {
    // The first-tap listener and the header bell can both fire on one click:
    // share the in-flight request so the dialog shows once.
    pendingEnable ??= requestAndSubscribe().finally(() => { pendingEnable = null; });
    return pendingEnable;
}

let pendingEnable: Promise<PushState> | null = null;

async function requestAndSubscribe(): Promise<PushState> {
    if (!supported()) return isIOS() && !isStandalone() ? 'ios-install' : 'unsupported';
    const permission = await Notification.requestPermission();
    if (permission === 'denied') return 'denied';
    if (permission !== 'granted') return 'off';
    return ensureSubscribed();
}

/** Subscribe (if needed) and POST the subscription to the gateway. Assumes
 *  permission is granted; never shows a dialog. */
export async function ensureSubscribed(): Promise<PushState> {
    const key = await fetchVapidKey();
    if (!key) return 'disabled';

    const reg = (await registerServiceWorker()) ?? (await navigator.serviceWorker.ready);
    await navigator.serviceWorker.ready;
    let sub = await reg.pushManager.getSubscription();
    // A subscription made with an old VAPID key never receives pushes.
    const current = sub?.options.applicationServerKey;
    if (sub && current && !sameKey(new Uint8Array(current), urlBase64ToUint8Array(key))) {
        await sub.unsubscribe();
        sub = null;
    }
    if (!sub) {
        sub = await reg.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(key) as BufferSource,
        });
    }
    await postJSON('/api/push/subscribe', sub.toJSON());
    return 'on';
}

/** Chromium shows the permission dialog without a user tap. Safari, iOS and
 *  Firefox ignore such a request, so there it waits for the first tap. */
export function canPromptWithoutGesture(ua: string = navigator.userAgent): boolean {
    return !isIOS(ua) && /Chrome\/|Chromium\/|Edg\//.test(ua);
}

/**
 * Runs once per app open, like a native app's first-launch prompt:
 *  - granted: re-subscribe silently, so the gateway store stays current;
 *  - default: show the browser's permission dialog now (Chromium) or on the
 *    first tap anywhere (Safari, Firefox), at most once per open;
 *  - denied / unsupported: do nothing, the header bell explains why.
 * Returns a cleanup that removes a pending first-tap listener.
 */
export function initPushOnOpen(onState: (s: PushState) => void): () => void {
    let disposed = false;
    let removeTap = () => { };
    const report = (p: Promise<PushState>) => p
        .then(s => { if (!disposed) onState(s); })
        .catch(err => console.warn('[push]', err));

    const armFirstTap = () => {
        const onTap = () => {
            removeTap();
            if (Notification.permission === 'default') report(enablePush());
        };
        document.addEventListener('click', onTap, { capture: true });
        removeTap = () => document.removeEventListener('click', onTap, { capture: true });
    };

    if (!supported()) {
        onState(isIOS() && !isStandalone() ? 'ios-install' : 'unsupported');
    } else if (Notification.permission === 'granted') {
        report(ensureSubscribed());
    } else if (Notification.permission === 'denied') {
        onState('denied');
    } else if (canPromptWithoutGesture()) {
        // A dismissed dialog is not re-asked this session: Chrome blocks the
        // site after repeated dismissals. The bell stays available.
        report(enablePush());
    } else {
        onState('off');
        armFirstTap();
    }

    return () => {
        disposed = true;
        removeTap();
    };
}

export async function disablePush(): Promise<PushState> {
    const reg = await navigator.serviceWorker.getRegistration('/');
    const sub = await reg?.pushManager.getSubscription();
    if (sub) {
        await postJSON('/api/push/unsubscribe', { endpoint: sub.endpoint }).catch(() => { });
        await sub.unsubscribe();
    }
    return 'off';
}

/** Push a test notification through the gateway to this device only. If the
 *  gateway lost this subscription, re-register it and try once more. */
export async function sendTestPush(): Promise<number> {
    const send = async () => {
        const reg = await navigator.serviceWorker.getRegistration('/');
        const sub = await reg?.pushManager.getSubscription();
        if (!sub) throw new Error('Alerts are off on this device');
        return fetch(`${BASE}/api/push/test`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ endpoint: sub.endpoint }),
        });
    };
    let res = await send();
    if (res.status === 404) {
        await ensureSubscribed();
        res = await send();
    }
    if (res.status === 503) throw new Error('Push is not configured on the server');
    if (!res.ok) throw new Error(`Test failed: ${res.status}`);
    const { sent } = await res.json() as { sent: number };
    return sent;
}

export function sameKey(a: Uint8Array, b: Uint8Array): boolean {
    return a.length === b.length && a.every((v, i) => v === b[i]);
}
