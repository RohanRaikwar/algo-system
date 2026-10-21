import { cn } from '@/lib/utils';
import { TONE_CLASS } from '../sr/srClasses';

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

/* ── Empty / loading states ───────────────────────────────── */

export const emptyState = 'flex flex-col items-center justify-center gap-[10px] py-[56px] px-[20px] text-center text-sg-muted [&_svg]:text-sg-faint [&_p]:max-w-[52ch] [&_p]:text-[0.9rem]';
export const errorText = 'text-sg-loss';
export const spin = 'animate-[sg-spin_1s_linear_infinite] motion-reduce:animate-none';

/* ── Buttons ──────────────────────────────────────────────── */

/** Pagination and refresh buttons. */
export const ctrlBtn = 'inline-flex items-center gap-[6px] h-[32px] py-0 px-[12px] bg-sg-raised border border-solid border-sg-line rounded-ctrl text-sg-text-2 text-[0.82rem] cursor-pointer [transition:background_0.15s,color_0.15s] hover:enabled:bg-sg-hover hover:enabled:text-sg-text disabled:opacity-[0.45] disabled:cursor-default';

/* ── Tables ───────────────────────────────────────────────── */

/** Scroll box around a table; inside a day card its border is softer. */
export function tableWrap({ dayBody = false, fullHeight = false } = {}): string {
    return cn(
        'overflow-auto border border-solid rounded-[12px] bg-sg-bg',
        fullHeight ? 'max-h-none' : 'max-h-[min(70vh,760px)]',
        dayBody ? 'border-sg-line-soft' : 'border-sg-line',
    );
}

export const table = 'w-full min-w-[880px] border-collapse table-fixed [&_tbody_tr]:border-b [&_tbody_tr]:border-b-sg-line-soft [&_tbody_tr]:[transition:background_0.12s] [&_tbody_tr:last-child]:[border-bottom:none] [&_tbody_tr:hover]:bg-sg-surface';

const thBase = 'sticky top-0 z-[1] py-[10px] px-[12px] bg-sg-raised border-b border-b-sg-line text-sg-muted text-[0.78rem] font-medium whitespace-nowrap';
export const th = `${thBase} text-left`;
export const thNum = `${thBase} text-right`;

type CellKind = 'plain' | 'strategy' | 'time' | 'price' | 'reason';

const CELL_KIND: Record<CellKind, string> = {
    plain: '',
    strategy: 'font-semibold whitespace-nowrap',
    time: 'tabular-nums whitespace-nowrap',
    price: 'text-right tabular-nums font-medium whitespace-nowrap',
    reason: 'whitespace-nowrap',
};

/**
 * Body cell of a signals table row. compact tables pad 9px, the log 10px.
 * Text is text-2 except the strategy cell; tone (a P&L colour) replaces it.
 */
export function cell(compact: boolean, kind: CellKind = 'plain', tone?: string): string {
    const color = kind === 'strategy' ? 'text-sg-text' : tone || 'text-sg-text-2';
    return cn(
        compact ? 'py-[9px]' : 'py-[10px]',
        'px-[12px] text-[0.86rem] align-middle overflow-hidden text-ellipsis',
        CELL_KIND[kind],
        color,
    );
}

/** Traded option symbol inside a table row. */
export const rowMono = 'font-mono text-[0.92em] tracking-[-0.01em] whitespace-nowrap';
/** Small grey line under a time or price. */
export const cellSub = 'block mt-[2px] text-[0.74rem] text-sg-faint font-normal';
export const priceNa = 'text-sg-faint text-[0.8rem]';

/* ── Badges ───────────────────────────────────────────────── */

const pill = 'inline-flex items-center justify-center gap-[5px] py-[3px] px-[9px] rounded-pill text-[0.74rem] font-semibold leading-[1.3] whitespace-nowrap';
const TONE_MUTED = 'text-sg-muted bg-sg-raised';
const TONE_PROFIT = 'text-sg-profit bg-sg-profit-soft';
const TONE_LOSS = 'text-sg-loss bg-sg-loss-soft';
const TONE_WARN = 'text-sg-warn bg-sg-warn-soft';
const TONE_INFO = 'text-sg-info bg-sg-info-soft';

export type ActionTone = 'none' | 'buy' | 'put' | 'exit' | 'watchHold' | 'watchTighten' | 'watchExit' | 'watchBuy';

const ACTION_TONE: Record<ActionTone, string> = {
    none: TONE_MUTED,
    buy: TONE_PROFIT,
    put: TONE_LOSS,
    exit: 'text-sg-text-2 bg-sg-raised',
    watchHold: TONE_INFO,
    watchTighten: TONE_WARN,
    watchExit: TONE_LOSS,
    watchBuy: TONE_PROFIT,
};

/** Action / side pill (BUY, EXIT, CALL, PUT, WATCH_*). */
export function actionBadge(tone: ActionTone): string {
    return `${pill} ${ACTION_TONE[tone]}`;
}

const MARKET_TONE: Record<string, string> = {
    TRENDING: TONE_PROFIT,
    SIDEWAYS: TONE_WARN,
    CHOPPY: TONE_LOSS,
    RANGE: TONE_INFO,
};

export function marketBadge(state: string): string {
    return `${pill} min-w-[74px] ${MARKET_TONE[state] ?? TONE_MUTED}`;
}

const ORDER_TYPE_TONE: Record<string, string> = {
    paper: TONE_MUTED,
    capped: TONE_WARN,
    real: 'text-white bg-sg-loss',
    shadow: TONE_INFO,
};

/** Paper / Capped / Real / Shadow pill with a leading dot. */
export function orderTypeBadge(mode: string): string {
    return `${pill} min-w-[62px] before:content-[''] before:w-[6px] before:h-[6px] before:rounded-[50%] before:bg-current ${ORDER_TYPE_TONE[mode] ?? TONE_MUTED}`;
}

export const statusCompleteBadge = `${pill} ${TONE_PROFIT}`;

const smallPill = 'py-[1px] px-[7px] rounded-pill text-[0.68rem] font-semibold';

const SL_KIND_TONE: Record<string, string> = {
    trail: 'text-sg-info bg-sg-info-soft',
    hard: TONE_WARN,
    manual: 'text-sg-accent bg-sg-accent-soft',
};

/** Stop kind tag (trail / hard / manual), also used for SHORT and real. */
export function slKind(kind: string): string {
    return cn(smallPill, 'lowercase', SL_KIND_TONE[kind]);
}

const EW_TONE: Record<string, string> = {
    hold: TONE_PROFIT,
    tighten: TONE_WARN,
    exit: TONE_LOSS,
};

/** Exit-watch verdict tag (hold / tighten / exit). */
export function ewBadge(decision: string): string {
    return cn(smallPill, EW_TONE[decision]);
}

/* ── Day groups and summary cards ─────────────────────────── */

export const tabStack = 'flex flex-col gap-[14px]';
export const sectionHeader = 'flex items-center justify-between gap-[12px]';
export const sectionTitleText = 'flex items-center gap-[8px] text-sg-text font-semibold [&_svg]:text-sg-muted';
export const dailyTitle = `${sectionTitleText} text-[1rem]`;
export const dayList = 'flex flex-col gap-[8px]';
export const dayCard = 'border border-solid border-sg-line rounded-[12px] bg-sg-bg overflow-hidden';
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
export const summaryCardShell = 'py-[12px] px-[14px] border border-solid border-sg-line rounded-[12px] min-w-0';
export const summaryCard = `${summaryCardShell} bg-sg-bg`;
export const summaryLabel = 'flex items-center gap-[5px] mb-[4px] text-sg-muted text-[0.8rem]';

/** Summary card value; tone replaces the text colour, sm is the smaller size. */
export function summaryValue(tone = 'text-sg-text', sm = false): string {
    return cn('font-semibold tabular-nums overflow-hidden text-ellipsis whitespace-nowrap', sm ? 'text-[0.95rem]' : 'text-[1.15rem]', tone);
}

/** "Last updated" line under a tab. */
export const footerNote = 'flex items-center justify-end gap-[6px] text-sg-faint text-[0.8rem] tabular-nums';

/* ── F&O cards (FnoInstrumentsTab, SRStatusCard) ──────────── */

export const fnoCard = 'bg-sg-bg border border-solid border-sg-line rounded-[12px] overflow-hidden';
const fnoHeaderBase = 'flex items-center gap-[8px] py-[12px] px-[14px] border-b border-b-sg-line-soft font-semibold';
export const fnoAtmHeader = `${fnoHeaderBase} [&_svg]:text-sg-accent`;
export const fnoInstHeader = fnoHeaderBase;
export const fnoBadge = `ml-auto py-[2px] px-[9px] rounded-pill text-[0.74rem] font-semibold ${TONE_PROFIT}`;
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
