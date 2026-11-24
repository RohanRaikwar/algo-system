import { tfBucketSec } from '../../utils/helpers';
import type { OptBar } from '../../services/optionChain';

/**
 * Applies one live price (paise) at unix second `sec` to the bar list of
 * timeframe tf: updates the last bar when it is the same bucket, else
 * appends a new bar. Returns the bar that changed. Older ticks are ignored.
 */
export function applyTick(bars: OptBar[], sec: number, price: number, tf: number): OptBar | null {
    if (price <= 0) return null;
    const start = tfBucketSec(sec, tf);
    const last = bars[bars.length - 1];
    if (last && start < last.t) return null;
    if (last && last.t === start) {
        last.h = Math.max(last.h, price);
        last.l = Math.min(last.l, price);
        last.c = price;
        return last;
    }
    const bar: OptBar = { t: start, o: price, h: price, l: price, c: price, v: 0 };
    bars.push(bar);
    return bar;
}

/** Rupees with two decimals from paise; '—' when unknown. */
export function rupees(paise?: number): string {
    return paise && paise > 0 ? (paise / 100).toFixed(2) : '—';
}
