import type { RangeView } from '../../types/range';

/** paise → "22,784.90" */
export function rupees(paise?: number): string {
    if (paise === undefined || paise === null || paise === 0) return '—';
    return (paise / 100).toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** paise distance → "+12.5 pts" */
export function points(paise: number, signed = false): string {
    const v = paise / 100;
    const s = Math.abs(v).toLocaleString('en-IN', { maximumFractionDigits: 1 });
    if (!signed) return `${s} pts`;
    return `${v > 0 ? '+' : v < 0 ? '−' : ''}${s} pts`;
}

/** 900 → "15:00" */
export function hhmm(min: number): string {
    return `${String(Math.floor(min / 60)).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`;
}

export type Watch =
    | { kind: 'pending'; text: string }
    | { kind: 'flag'; text: string; ready: boolean }
    | { kind: 'range'; text: string }
    | { kind: 'none'; text: string };

/** What the strategy is waiting for, in one line. */
export function watching(v: RangeView): Watch {
    if (v.side !== 'NONE') return { kind: 'none', text: 'In a position — managing exits' };
    if (v.pending_side) {
        const dir = v.pending_side === 'CALL' ? 'above' : 'below';
        const label = v.pending_kind === 'FLAG' ? 'Flag' : 'Breakout';
        return { kind: 'pending', text: `${label} ${v.pending_side} armed ${dir} ${rupees(v.pending_edge)} — needs one more 5m close beyond it` };
    }
    if (v.regime === 'RANGE' && v.support && v.resistance) {
        return { kind: 'range', text: `Waiting for a 5m close beyond ${rupees(v.support)} – ${rupees(v.resistance)}` };
    }
    if (v.flag && v.flag_box_lo && v.flag_box_hi) {
        const dir = v.day_trend < 0 ? 'down' : 'up';
        const ready = v.flag_tight && v.flag_trend_day;
        if (ready) {
            const edge = v.day_trend < 0 ? `below ${rupees(v.flag_box_lo)}` : `above ${rupees(v.flag_box_hi)}`;
            return { kind: 'flag', ready, text: `Flag box ${rupees(v.flag_box_lo)} – ${rupees(v.flag_box_hi)} on a ${dir} day — watching for a break ${edge}` };
        }
        const why = !v.flag_trend_day ? `day move ${points(v.day_trend, true)} (needs 100)` : 'last hour too wide for a flag';
        return { kind: 'flag', ready, text: `No setup — ${why}` };
    }
    return { kind: 'none', text: v.regime === 'WARMING' ? 'Warming up indicators' : 'No setup yet' };
}
