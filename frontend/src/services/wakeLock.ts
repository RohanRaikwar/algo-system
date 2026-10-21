// Screen Wake Lock: keeps the screen (and so the phone) awake while the app
// is open and shown. A web app cannot stop Android sleeping once it is in the
// background or the screen is locked; there, alerts come by Web Push.
// The lock is dropped by the browser whenever the page is hidden, so it is
// taken again each time the page becomes visible.

type Sentinel = { released: boolean; release(): Promise<void>; addEventListener(t: 'release', f: () => void): void };
type WakeLockNav = Navigator & { wakeLock?: { request(type: 'screen'): Promise<Sentinel> } };

const KEY = 'levels.keepAwake';

export function wakeLockSupported(nav: Navigator = navigator): boolean {
    return typeof (nav as WakeLockNav).wakeLock?.request === 'function';
}

/** Saved choice; on by default in the installed app (standalone), off in a tab. */
export function keepAwakeWanted(standalone: boolean): boolean {
    try {
        const v = localStorage.getItem(KEY);
        if (v !== null) return v === '1';
    } catch { /* storage off */ }
    return standalone;
}

export function saveKeepAwake(on: boolean): void {
    try { localStorage.setItem(KEY, on ? '1' : '0'); } catch { /* storage off */ }
}

/**
 * Holds a screen wake lock while `on` and the page is visible. Returns a
 * stop function. onHeld reports whether a lock is held right now.
 */
export function holdWakeLock(onHeld: (held: boolean) => void, nav: Navigator = navigator, doc: Document = document): () => void {
    const wl = (nav as WakeLockNav).wakeLock;
    if (!wl) {
        onHeld(false);
        return () => { };
    }
    let sentinel: Sentinel | null = null;
    let stopped = false;
    const acquire = () => {
        if (stopped || doc.visibilityState !== 'visible' || (sentinel && !sentinel.released)) return;
        wl.request('screen').then(s => {
            if (stopped) { void s.release(); return; }
            sentinel = s;
            onHeld(true);
            s.addEventListener('release', () => { if (!stopped) onHeld(false); });
        }).catch(() => onHeld(false)); // refused (battery saver, not visible)
    };
    acquire();
    doc.addEventListener('visibilitychange', acquire);
    return () => {
        stopped = true;
        doc.removeEventListener('visibilitychange', acquire);
        if (sentinel && !sentinel.released) void sentinel.release();
        onHeld(false);
    };
}
