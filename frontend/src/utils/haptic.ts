/**
 * Short tap vibration for native feel. Android Chrome only; iOS Safari has
 * no Vibration API, so this is a silent no-op there.
 */
export function haptic(ms = 10): void {
    if (typeof navigator === 'undefined' || typeof navigator.vibrate !== 'function') return;
    try {
        navigator.vibrate(ms);
    } catch {
        // Some browsers throw before the first user gesture; feedback is optional.
    }
}
