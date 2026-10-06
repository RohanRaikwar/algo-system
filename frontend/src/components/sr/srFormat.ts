import type { RangeView } from '../../types/range';

export const REGIME_LABEL: Record<RangeView['regime'], string> = { RANGE: 'Range', TRENDING: 'Trending', WARMING: 'Warming up' };

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

/** Open-position P&L in paise against the last price; null when flat or unknown. */
export function pnlPaise(v: RangeView, last: number | null): number | null {
    if (v.side === 'NONE' || !last || !v.entry) return null;
    return v.side === 'CALL' ? last - v.entry : v.entry - last;
}

const IST_TIME = new Intl.DateTimeFormat('en-IN', { timeZone: 'Asia/Kolkata', hour: '2-digit', minute: '2-digit', hour12: false });

/** "as of" time for a view: its 1m bar's close, in IST. Empty when ts is missing. */
export function asOf(ts: string): string {
    if (!ts) return '';
    const ms = new Date(ts).getTime();
    return Number.isNaN(ms) ? '' : IST_TIME.format(new Date(ms + 60_000));
}

export type Tone = 'up' | 'down' | 'muted' | 'warn';

export interface PeekSummary {
    position: { text: string; tone: Tone };
    /** Live watch line; replaces the stats when the strategy is about to act. */
    alert: string | null;
    stats: { label: string; value: string }[];
}

/** One-line strategy state for the phone peek strip. */
export function peekSummary(v: RangeView, last: number | null): PeekSummary {
    const pnl = pnlPaise(v, last);
    const position = v.side === 'NONE'
        ? { text: 'Flat', tone: 'muted' as Tone }
        : {
            text: pnl === null ? v.side : `${v.side} ${points(pnl, true).replace(' pts', '')}`,
            tone: (pnl === null ? 'muted' : pnl >= 0 ? 'up' : 'down') as Tone,
        };
    const w = watching(v);
    const alert = w.kind === 'pending' || (w.kind === 'flag' && w.ready) ? w.text : null;
    const stats = [
        { label: 'ADX', value: v.adx.toFixed(1) },
        { label: 'RSI', value: v.rsi5 ? v.rsi5.toFixed(0) : '—' },
        { label: 'Day', value: v.day_open ? points(v.day_trend, true) : '—' },
    ];
    return { position, alert, stats };
}
