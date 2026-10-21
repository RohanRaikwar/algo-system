// First-start alert setup (Android): notification permission, Chrome's
// battery setting, and a locked-phone test. A web app cannot change Chrome's
// battery setting or read it, so that step is a guided manual one.

const DONE_KEY = 'levels.alertSetupDone';

export function isAndroid(ua: string = navigator.userAgent): boolean {
    return /Android/i.test(ua);
}

export function alertSetupDone(): boolean {
    try { return localStorage.getItem(DONE_KEY) === '1'; } catch { return false; }
}

export function markAlertSetupDone(): void {
    try { localStorage.setItem(DONE_KEY, '1'); } catch { /* storage off */ }
}

/** Shown at start on Android until finished or skipped. */
export function showAlertSetupAtStart(ua?: string): boolean {
    return isAndroid(ua) && !alertSetupDone();
}
