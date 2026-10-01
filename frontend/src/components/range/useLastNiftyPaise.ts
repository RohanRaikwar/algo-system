import { useMemo } from 'react';
import { useCandleStore } from '../../store/useCandleStore';

/** Last 1m close in paise for an instrument key, for distance-to-level readouts. */
export function useLastNiftyPaise(key: string | undefined): number | null {
    const candles = useCandleStore(s => s.candles[60]);
    return useMemo(() => {
        if (!key || !candles?.length) return null;
        let best: number | null = null;
        let bestMs = -Infinity;
        for (const c of candles) {
            if (`${c.exchange}:${c.token}` !== key) continue;
            const ms = new Date(c.ts).getTime();
            if (ms > bestMs) { bestMs = ms; best = Math.round(c.close * 100); }
        }
        return best;
    }, [candles, key]);
}
