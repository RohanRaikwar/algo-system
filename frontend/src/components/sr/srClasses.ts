import type { Tone } from './srFormat';

/** Regime pill colours (shared by the panel, peek strip and sheet). */
export const REGIME_CLASS: Record<string, string> = {
    RANGE: 'text-sg-info bg-sg-info-soft',
    TREND_UP: 'text-sg-profit bg-sg-profit-soft',
    TREND_DOWN: 'text-sg-loss bg-sg-loss-soft',
    MIXED: 'text-sg-warn bg-sg-warn-soft',
    DEAD: 'text-sg-muted bg-sg-line-soft',
    WARMING: 'text-sg-muted bg-sg-line-soft',
};

export const TONE_CLASS: Record<Tone, string> = {
    up: 'text-sg-profit',
    down: 'text-sg-loss',
    muted: 'text-sg-muted',
    warn: 'text-sg-warn',
};

/** Phone peek strip / sheet pill. */
export const mobilePill = 'shrink-0 py-[2px] px-[8px] rounded-pill text-[11px] font-semibold tracking-[0.02em]';

/** Drag handle; hidden in phone landscape (peek chip and side sheet). */
export const sheetHandle = 'self-center shrink-0 w-[32px] h-[4px] rounded-[2px] bg-sg-line phone-land:hidden';
