import type { BadgeTone } from '@/components/ui/badge';
import type { Tone } from './srFormat';

/** Regime pill tone (Badge variant="mobile"), shared by the panel, peek strip and sheet. */
export const REGIME_TONE: Record<string, BadgeTone> = {
    RANGE: 'info',
    TREND_UP: 'profit',
    TREND_DOWN: 'loss',
    MIXED: 'warn',
    DEAD: 'neutral',
    WARMING: 'neutral',
};

export const TONE_CLASS: Record<Tone, string> = {
    up: 'text-sg-profit',
    down: 'text-sg-loss',
    muted: 'text-sg-muted',
    warn: 'text-sg-warn',
};

/** Drag handle; hidden in phone landscape (peek chip and side sheet). */
export const sheetHandle = 'self-center shrink-0 w-[32px] h-[4px] rounded-[2px] bg-sg-line phone-land:hidden';
