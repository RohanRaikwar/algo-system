import type { SRLevel, SRView } from '../../types/sr';
import { rejectLabel } from '../signals/SRStatusCard';

export const REGIME_LABEL: Record<string, string> = {
    RANGE: 'Range',
    TREND_UP: 'Trend up',
    TREND_DOWN: 'Trend down',
    MIXED: 'Mixed',
    DEAD: 'Dead',
    WARMING: 'Warming up',
};

export const KIND_LABEL: Record<string, string> = {
    FADE: 'Fade',
    BREAKOUT_RETEST: 'Breakout retest',
    PULLBACK: 'Pullback',
};

const SOURCE_LABEL: Record<string, string> = {
    SWING: 'swing',
    PREV_DAY: 'prev day',
    OPENING: 'opening range',
};

export function sourceLabel(s: string): string {
    return SOURCE_LABEL[s] ?? s.toLowerCase();
}

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

/** Nearest level at or below price (support) and above it (resistance). */
export function nearestLevels(levels: SRLevel[] | null, price: number | null | undefined): { support?: SRLevel; resistance?: SRLevel } {
    if (!price || !levels) return {};
    let support: SRLevel | undefined;
    let resistance: SRLevel | undefined;
    for (const l of levels) {
        if (l.price <= price) {
            if (!support || l.price > support.price) support = l;
        } else if (!resistance || l.price < resistance.price) {
            resistance = l;
        }
    }
    return { support, resistance };
}

/** Nearest n levels below price (supports, closest first) and above (resistances, closest first). */
export function nearestN(levels: SRLevel[] | null, price: number | null | undefined, n: number): { supports: SRLevel[]; resistances: SRLevel[] } {
    if (!price || !levels) return { supports: [], resistances: [] };
    const supports = levels.filter(l => l.price <= price).sort((a, b) => b.price - a.price).slice(0, n);
    const resistances = levels.filter(l => l.price > price).sort((a, b) => a.price - b.price).slice(0, n);
    return { supports, resistances };
}

const WARN_LABEL: Record<string, string> = { day_extreme: 'day extreme', sideways: 'sideways' };

/** "day_extreme" → "day extreme". */
export function warnLabel(w: string): string {
    return WARN_LABEL[w] ?? w.replace(/_/g, ' ');
}

/** Warnings in an entry reason's "warn=a,b" tag. */
export function reasonWarnings(reason: string | undefined): string[] {
    const m = /(?:^|\s)warn=([\w,]+)/.exec(reason ?? '');
    return m ? m[1].split(',').filter(Boolean) : [];
}

/** "CALL: day extreme, sideways · PUT: sideways" from the view's warn list; "" when clear. */
export function warnSummary(warn: string[] | null | undefined): string {
    const by: Record<string, string[]> = {};
    for (const w of warn ?? []) {
        const [side, what] = w.split(':');
        (by[side.toUpperCase()] ??= []).push(warnLabel(what));
    }
    return Object.entries(by).map(([side, ws]) => `${side}: ${ws.join(', ')}`).join(' · ');
}

/** One-line chart situation: "SR · Trend up · 90% of day · Sideways 35 pts/30m". */
export function situationLine(v: SRView, price: number | null): string {
    const parts = ['SR', REGIME_LABEL[v.regime] ?? v.regime];
    const d = dayPicture(v, price);
    if (d) parts.push(`${d.pct}% of day`);
    if (v.box_pts && v.box_bars) parts.push(`${v.boxed ? 'Sideways' : 'Moving'} ${points(v.box_pts)}/${v.box_bars * 5}m`);
    return parts.join(' · ');
}

export type Watch =
    | { kind: 'pending'; text: string }
    | { kind: 'blocked'; text: string }
    | { kind: 'idle'; text: string };

/** What the strategy is waiting for, in one line. */
export function watching(v: SRView, price: number | null): Watch {
    if (v.side !== 'NONE') return { kind: 'idle', text: 'In a position — managing exits' };
    if (v.regime === 'WARMING') return { kind: 'idle', text: 'Warming up indicators' };
    if (v.block) return { kind: 'blocked', text: `Blocked — ${rejectLabel(v.block)}` };
    if (v.pending_side) {
        const left = v.pending_left ?? 0;
        return {
            kind: 'pending',
            text: `${v.pending_side} break of ${rupees(v.pending_level)} — waiting for a retest (${left} bar${left === 1 ? '' : 's'} left)`,
        };
    }
    if (v.regime === 'DEAD') return { kind: 'idle', text: 'Market dead — 15m ATR below floor, no entries' };
    const { support, resistance } = nearestLevels(v.levels, price);
    if (!support && !resistance) return { kind: 'idle', text: 'No levels yet' };
    const parts = [
        support && `support ${rupees(support.price)}`,
        resistance && `resistance ${rupees(resistance.price)}`,
    ].filter(Boolean);
    const what = v.regime === 'MIXED' ? 'a breakout and retest' : 'a setup';
    return { kind: 'idle', text: `Waiting for ${what} at ${parts.join(' / ')}` };
}

/** Open-position P&L in paise against the last price; null when flat or unknown. */
export function pnlPaise(v: SRView, last: number | null): number | null {
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

/** Clock time of an event in IST; empty for missing or zero Go times. */
export function clock(ts?: string): string {
    if (!ts || ts.startsWith('0001')) return '';
    const ms = new Date(ts).getTime();
    return Number.isNaN(ms) ? '' : IST_TIME.format(new Date(ms));
}

export interface DayPicture {
    /** 0 = day low, 100 = day high. */
    pct: number;
    fromHigh: number;
    fromLow: number;
    /** Readable parts: "92% of day range", "8 pts below high", … */
    parts: string[];
    /** Side the day-extreme gate refuses right now, if any. */
    blocked: 'CALL' | 'PUT' | null;
}

/** Where price stands in today's data; same numbers as the strategy's day check. */
export function dayPicture(v: SRView, price: number | null): DayPicture | null {
    const hi = v.day_high, lo = v.day_low;
    if (!price || !hi || !lo || hi <= lo) return null;
    const fromHigh = hi - price, fromLow = price - lo;
    const pct = Math.floor((fromLow * 100) / (hi - lo));
    const parts = [
        `${pct}% of day range`,
        `${points(fromHigh)} below high`,
        `${points(fromLow)} above low`,
    ];
    if (v.prev_day_high && v.prev_day_low) {
        parts.push(`${points(price - v.prev_day_high, true)} vs PDH`, `${points(price - v.prev_day_low, true)} vs PDL`);
    }
    if (v.day_open) parts.push(`${points(price - v.day_open, true)} from open`);
    if (v.or_high && v.or_low) {
        parts.push(price > v.or_high ? 'above OR' : price < v.or_low ? 'below OR' : 'inside OR');
    }
    let blocked: DayPicture['blocked'] = null;
    const ext = v.day_extreme_pct ?? 0, run = v.day_run_pts ?? 0;
    if (ext > 0) {
        if (pct >= ext && fromLow >= run) blocked = 'CALL';
        else if (v.day_extreme_puts && pct <= 100 - ext && fromHigh >= run) blocked = 'PUT';
    }
    return { pct, fromHigh, fromLow, parts, blocked };
}

/** "Sideways: 28 pts in 30 min (≤ 30)" while boxed, "Moving: …" otherwise; null without data. */
export function boxLine(v: SRView): { text: string; boxed: boolean } | null {
    if (!v.box_pts || !v.box_limit || !v.box_bars) return null;
    const boxed = v.box_pts <= v.box_limit;
    const span = `${points(v.box_pts)} in ${v.box_bars * 5} min (sideways ≤ ${points(v.box_limit)})`;
    const effect = v.gate_mode === 'block' ? 'pullback/retest held' : '⚠ pullback/retest flagged';
    return { boxed, text: boxed ? `Sideways: ${span} — ${effect}` : `Moving: ${span}` };
}

export type Tone = 'up' | 'down' | 'muted' | 'warn';

export interface PeekSummary {
    position: { text: string; tone: Tone };
    /** Live watch line; replaces the stats when the strategy is armed or blocked. */
    alert: string | null;
    stats: { label: string; value: string }[];
}

/** One-line strategy state for the phone peek strip. */
export function peekSummary(v: SRView, last: number | null): PeekSummary {
    const pnl = pnlPaise(v, last);
    const position = v.side === 'NONE'
        ? { text: 'Flat', tone: 'muted' as Tone }
        : {
            text: pnl === null ? v.side : `${v.side} ${points(pnl, true).replace(' pts', '')}`,
            tone: (pnl === null ? 'muted' : pnl >= 0 ? 'up' : 'down') as Tone,
        };
    const w = watching(v, last ?? v.close ?? null);
    const alert = w.kind === 'idle' ? null : w.text;
    const stats = [
        { label: 'ADX', value: v.adx.toFixed(1) },
        { label: 'RSI', value: v.rsi5 ? v.rsi5.toFixed(0) : '—' },
        { label: 'Day', value: points(v.day_pnl_pts, true) },
    ];
    return { position, alert, stats };
}
