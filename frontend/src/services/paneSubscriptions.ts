/**
 * Reference-counted chart subscriptions for extra dashboard panes.
 *
 * The main chart owns the global (selectedToken, selectedTF) subscription.
 * Extra panes each need their own symbol:tf subscription; two panes (or a
 * pane and the main chart) may share one, so a subscription is only dropped
 * when nobody shows it any more. Pure bookkeeping: callers send the
 * SUBSCRIBE/UNSUBSCRIBE messages.
 */
const subs = new Map<string, { symbol: string; tf: number; refs: number }>();

const key = (symbol: string, tf: number) => `${symbol}:${tf}`;

/** Adds a reference; true when this is the first one (subscribe now). */
export function acquirePaneSub(symbol: string, tf: number): boolean {
    const k = key(symbol, tf);
    const cur = subs.get(k);
    if (cur) {
        cur.refs++;
        return false;
    }
    subs.set(k, { symbol, tf, refs: 1 });
    return true;
}

/** Drops a reference; true when it was the last one (unsubscribe now). */
export function releasePaneSub(symbol: string, tf: number): boolean {
    const k = key(symbol, tf);
    const cur = subs.get(k);
    if (!cur) return false;
    if (--cur.refs > 0) return false;
    subs.delete(k);
    return true;
}

/** Whether any pane shows symbol at tf. */
export function isPaneSub(symbol: string, tf: number): boolean {
    return subs.has(key(symbol, tf));
}

/** Every pane subscription, for resubscribing after a reconnect or resync. */
export function listPaneSubs(): Array<{ symbol: string; tf: number }> {
    return [...subs.values()].map(({ symbol, tf }) => ({ symbol, tf }));
}

/** Test helper. */
export function resetPaneSubs() {
    subs.clear();
}
