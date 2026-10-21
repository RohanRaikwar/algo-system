import type { BadgeTone } from '@/components/ui/badge';
import { actionBadge, actionTone, type ActionTone } from './classes';

/**
 * Exit-watch signals (WATCH_HOLD / WATCH_TIGHTEN / WATCH_EXIT) come from the
 * exitwatch service on pub:signal and its own journal; WATCH_BUY is SR entry
 * advice from stratengine. They are advice only: no order is placed, so P&L,
 * trade pairing and chart markers skip them.
 */
export function isWatchAction(action?: string): boolean {
    return (action ?? '').toUpperCase().startsWith('WATCH_');
}

/** Action filter used by the Signals LOG tab: 'WATCH' matches any WATCH_*. */
export function matchesActionFilter(action: string, filter: string): boolean {
    if (filter === 'ALL') return true;
    if (filter === 'WATCH') return isWatchAction(action);
    return action === filter;
}

function watchActionTone(action: string): ActionTone {
    const a = action.toUpperCase();
    if (a === 'WATCH_EXIT') return 'watchExit';
    if (a === 'WATCH_BUY') return 'watchBuy';
    if (a === 'WATCH_TIGHTEN') return 'watchTighten';
    return 'watchHold';
}

/** Badge tone for a WATCH_* action. */
export function watchBadgeTone(action: string): BadgeTone {
    return actionTone(watchActionTone(action));
}

/** Badge class for a WATCH_* action. */
export function watchBadgeClass(action: string): string {
    return actionBadge(watchActionTone(action));
}

/**
 * True for a WATCH_EXIT priced at the option premium. Exitwatch tags those
 * with "idx=" in the reason; older rows carry the index LTP as price, which
 * cannot be compared with the entry premium.
 */
export function watchExitPricedAtPremium(sig: { action: string; reason?: string }): boolean {
    return sig.action === 'WATCH_EXIT' && / idx=/.test(sig.reason ?? '');
}
