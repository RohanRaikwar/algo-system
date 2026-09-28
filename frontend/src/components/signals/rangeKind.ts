export type RangeEntryKind = 'BREAKOUT' | 'FLAG' | 'MEAN_REVERSION';

const KIND_RE = /\bRANGE (BREAKOUT|FLAG|MEAN_REVERSION)\b/;

/** Entry type of a NIFTY50_RANGE signal, read from its reason; null otherwise. */
export function rangeEntryKind(reason: string | undefined): RangeEntryKind | null {
    const m = reason?.match(KIND_RE);
    return m ? (m[1] as RangeEntryKind) : null;
}

export const RANGE_KIND_LABEL: Record<RangeEntryKind, string> = {
    BREAKOUT: 'Breakout',
    FLAG: 'Flag',
    MEAN_REVERSION: 'Mean rev.',
};
