/** An entry signal stratengine blocked before any order (pub:refused). Strike is index points. */
export interface RefusedEntry {
    strategy: string;
    side: string;
    token: string;
    exchange: string;
    strike?: number;
    /** Why the entry was refused */
    reason: string;
    /** Why the strategy wanted to enter */
    strategy_reason: string;
    /** RFC3339 UTC */
    ts: string;
}

/** Today's refused entries (IST date), oldest first; each frame replaces the whole list. */
export interface RefusedView {
    date: string;
    entries: RefusedEntry[];
}
