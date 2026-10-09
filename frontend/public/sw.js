// TradingPulse service worker: Web Push only. It does not cache anything,
// because every screen shows live market data. The gateway sends the payload
// built by backend/internal/push (Notification: title, body, tag, url, sticky).

self.addEventListener('install', () => {
    self.skipWaiting();
});

self.addEventListener('activate', (event) => {
    event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
    let data = {};
    try {
        data = event.data ? event.data.json() : {};
    } catch {
        data = { title: 'TradingPulse', body: event.data ? event.data.text() : '' };
    }
    const title = data.title || 'TradingPulse';
    event.waitUntil(
        self.registration.showNotification(title, {
            body: data.body || '',
            tag: data.tag || undefined,
            renotify: Boolean(data.tag),
            requireInteraction: Boolean(data.sticky),
            icon: '/icon-192.png',
            badge: '/badge-96.png',
            data: { url: data.url || '/' },
        }),
    );
});

self.addEventListener('notificationclick', (event) => {
    event.notification.close();
    const target = new URL(event.notification.data?.url || '/', self.location.origin).href;
    event.waitUntil(
        self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((wins) => {
            for (const w of wins) {
                if (new URL(w.url).origin === self.location.origin) {
                    return w.focus().then((c) => (c && c.url !== target ? c.navigate(target) : c));
                }
            }
            return self.clients.openWindow(target);
        }),
    );
});

// The browser rotated the subscription (expiry, key change): re-register it
// with the gateway so alerts keep arriving without opening the app.
self.addEventListener('pushsubscriptionchange', (event) => {
    event.waitUntil((async () => {
        const apiBase = apiBaseFromURL();
        const keyRes = await fetch(`${apiBase}/api/push/vapid-public-key`);
        if (!keyRes.ok) return;
        const { key } = await keyRes.json();
        const sub = await self.registration.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(key),
        });
        await fetch(`${apiBase}/api/push/subscribe`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(sub),
        });
    })());
});

// The API base is baked into the page bundle (VITE_API_URL), not into this
// file, so the page passes it in the registration URL: /sw.js?api=<base>.
function apiBaseFromURL() {
    return new URL(self.location.href).searchParams.get('api') || '';
}

function urlBase64ToUint8Array(b64) {
    const pad = '='.repeat((4 - (b64.length % 4)) % 4);
    const raw = atob((b64 + pad).replace(/-/g, '+').replace(/_/g, '/'));
    return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}
