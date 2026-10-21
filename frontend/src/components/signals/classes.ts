import { cn } from '@/lib/utils';
import { TONE_CLASS } from '../sr/srClasses';
import { badgeVariants, type BadgeTone } from '@/components/ui/badge';

/** Utility class strings shared by the Signals tabs (desktop and phone). */

/* ── Page shell ───────────────────────────────────────────── */

/** Signals / Health page root without its gap (the phone page sets gap-0). */
export const signalsPageBase = 'flex flex-col text-sg-text font-sans text-[0.9rem] min-w-0 [&_*:focus-visible]:[outline:none] [&_*:focus-visible]:shadow-ring [&_button]:[font-family:inherit] motion-reduce:[&_*]:[transition:none]!';
export const signalsPage = `${signalsPageBase} gap-[12px]`;

/* ── P&L colour ───────────────────────────────────────────── */

/** Profit / loss / flat text colour for a signed value. */
export function pnlTone(v: number): string {
    return TONE_CLASS[v > 0 ? 'up' : v < 0 ? 'down' : 'muted'];
}

/* ── Tables (Table / TableHead / TableCell carry the cell looks) ── */

/** Scroll box around a table; inside a day card its border is softer. */
export function tableWrap({ dayBody = false, fullHeight = false } = {}): string {
    return cn(
        'overflow-auto border border-solid rounded-[12px] bg-sg-bg',
        fullHeight ? 'max-h-none' : 'max-h-[min(70vh,760px)]',
        dayBody ? 'border-sg-line-soft' : 'border-sg-line',
    );
}

/** Traded option symbol inside a table row. */
export const rowMono = 'font-mono text-[0.92em] tracking-[-0.01em] whitespace-nowrap';
/** Small grey line under a time or price. */
export const cellSub = 'block mt-[2px] text-[0.74rem] text-sg-faint font-normal';
export const priceNa = 'text-sg-faint text-[0.8rem]';
export const errorText = 'text-sg-loss';

/* ── P&L tone keys (Badge / TableCell tone props) ─────────── */

export type PnlToneKey = 'profit' | 'loss' | 'muted';

/** Profit / loss / flat tone for a signed value, as a component tone prop. */
export function pnlToneKey(v: number): PnlToneKey {
    return v > 0 ? 'profit' : v < 0 ? 'loss' : 'muted';
}

/* ── Badges ───────────────────────────────────────────────── */
/*
 * Desktop renders <Badge variant tone>; the *Tone helpers pick the tone.
 * The class-string helpers (actionBadge, marketBadge, orderTypeBadge,
 * slKind) are the same Badge classes for markup that still sets className
 * (the phone views).
 */

export type ActionTone = 'none' | 'buy' | 'put' | 'exit' | 'watchHold' | 'watchTighten' | 'watchExit' | 'watchBuy';

const ACTION_TONE: Record<ActionTone, BadgeTone> = {
    none: 'muted',
    buy: 'profit',
    put: 'loss',
    exit: 'exit',
    watchHold: 'info',
    watchTighten: 'warn',
    watchExit: 'loss',
    watchBuy: 'profit',
};

/** Badge tone of an action / side pill (BUY, EXIT, CALL, PUT, WATCH_*). */
export function actionTone(tone: ActionTone): BadgeTone {
    return ACTION_TONE[tone];
}

/** Action / side pill classes (BUY, EXIT, CALL, PUT, WATCH_*). */
export function actionBadge(tone: ActionTone): string {
    return cn(badgeVariants({ tone: ACTION_TONE[tone] }));
}

const MARKET_TONE: Record<string, BadgeTone> = {
    TRENDING: 'profit',
    SIDEWAYS: 'warn',
    CHOPPY: 'loss',
    RANGE: 'info',
};

export function marketTone(state: string): BadgeTone {
    return MARKET_TONE[state] ?? 'muted';
}

export function marketBadge(state: string): string {
    return cn(badgeVariants({ variant: 'market', tone: marketTone(state) }));
}

const ORDER_TYPE_TONE: Record<string, BadgeTone> = {
    paper: 'muted',
    capped: 'warn',
    real: 'solid-loss',
    shadow: 'info',
};

export function orderTypeTone(mode: string): BadgeTone {
    return ORDER_TYPE_TONE[mode] ?? 'muted';
}

/** Paper / Capped / Real / Shadow pill with a leading dot. */
export function orderTypeBadge(mode: string): string {
    return cn(badgeVariants({ variant: 'order', tone: orderTypeTone(mode) }));
}

const SL_KIND_TONE: Record<string, BadgeTone> = {
    trail: 'info',
    hard: 'warn',
    manual: 'accent',
};

/** Stop kind tag tone (trail / hard / manual, also SHORT and real); none for other kinds. */
export function slKindTone(kind: string): BadgeTone | undefined {
    return SL_KIND_TONE[kind];
}

/** Stop kind tag classes (trail / hard / manual), also used for SHORT and real. */
export function slKind(kind: string): string {
    return cn(badgeVariants({ variant: 'kind', tone: slKindTone(kind) }));
}

const EW_TONE: Record<string, BadgeTone> = {
    hold: 'profit',
    tighten: 'warn',
    exit: 'loss',
};

/** Exit-watch verdict tag tone (hold / tighten / exit). */
export function ewTone(decision: string): BadgeTone | undefined {
    return EW_TONE[decision];
}

/* ── Day groups and summary cards ─────────────────────────── */

export const tabStack = 'flex flex-col gap-[14px]';
export const sectionHeader = 'flex items-center justify-between gap-[12px]';
export const sectionTitleText = 'flex items-center gap-[8px] text-sg-text font-semibold [&_svg]:text-sg-muted';
export const dailyTitle = `${sectionTitleText} text-[1rem]`;
export const dayList = 'flex flex-col gap-[8px]';
export const dayHead = 'flex items-center justify-between gap-[12px] w-full py-[12px] px-[14px] bg-transparent [border:none] text-sg-text text-left cursor-pointer [transition:background_0.12s] hover:bg-sg-surface lte-640:flex-col lte-640:items-start';
export const dayLeft = 'flex items-center gap-[8px] min-w-0 [&_svg]:text-sg-muted [&_svg]:shrink-0';
export const dayDate = 'font-semibold text-[0.9rem] whitespace-nowrap';
export const dayRight = 'flex items-center gap-[14px] flex-wrap justify-end lte-640:justify-start lte-640:gap-[10px]';
export const dayMeta = 'text-sg-muted text-[0.82rem] tabular-nums whitespace-nowrap';
export const dayBody = 'pt-0 px-[10px] pb-[10px]';

/** Day P&L in a day header; pts is the smaller points figure. */
export function dayPnl(tone: string, pts = false): string {
    return cn('font-semibold tabular-nums whitespace-nowrap', pts ? 'text-[0.82rem]' : 'text-[0.9rem]', tone);
}

export const summaryGrid = 'grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-[8px]';
export const summaryLabel = 'flex items-center gap-[5px] mb-[4px] text-sg-muted text-[0.8rem]';

/** Summary card value; tone replaces the text colour, sm is the smaller size. */
export function summaryValue(tone = 'text-sg-text', sm = false): string {
    return cn('font-semibold tabular-nums overflow-hidden text-ellipsis whitespace-nowrap', sm ? 'text-[0.95rem]' : 'text-[1.15rem]', tone);
}

/** "Last updated" line under a tab. */
export const footerNote = 'flex items-center justify-end gap-[6px] text-sg-faint text-[0.8rem] tabular-nums';

/* ── F&O cards (FnoInstrumentsTab, SRStatusCard) ──────────── */

const fnoHeaderBase = 'flex items-center gap-[8px] py-[12px] px-[14px] border-b border-b-sg-line-soft font-semibold';
export const fnoAtmHeader = `${fnoHeaderBase} [&_svg]:text-sg-accent`;
export const fnoInstHeader = fnoHeaderBase;
export const fnoAtmBody = 'grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))]';
export const fnoAtmStat = 'flex flex-col gap-[3px] py-[12px] px-[14px] border-r border-r-sg-line-soft last:[border-right:none]';
export const fnoLabel = 'text-sg-muted text-[0.8rem]';
export const fnoParam = 'inline-flex items-center gap-[5px] text-[0.9rem] font-medium tabular-nums';
export const fnoInstBody = 'py-[4px] px-[14px]';
export const fnoInstRow = 'flex items-center justify-between gap-[12px] py-[10px] px-0 border-b border-b-sg-line-soft last:[border-bottom:none]';
const fnoValueBase = 'inline-flex items-center gap-[5px] min-w-0 overflow-hidden text-ellipsis tabular-nums';
export const fnoValue = `${fnoValueBase} text-sg-text font-medium`;
export const fnoValueMono = `${fnoValue} font-mono text-[0.82rem]`;
export const fnoValueLtp = `${fnoValueBase} text-sg-text text-[1.05rem] font-semibold`;
export const fnoValueLtpLive = `${fnoValueBase} text-sg-accent text-[1.05rem] font-semibold`;
export const fnoNa = 'text-sg-faint text-[0.84rem] font-normal';
