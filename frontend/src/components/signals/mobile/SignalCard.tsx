import type { SignalRecord } from '../../../types/signal';
import { inferSide } from '../signalAnalytics';
import {
    formatPrice, formatSignedPaise, formatTime, getActionBadge, getSideBadge, instrumentLabel,
    orderMode, pnlClass, signalPnL, type OrderMode,
} from '../signalFormat';
import { cn } from '@/lib/utils';
import { cardLeft, cardMid, cardRight, cardShell, cardTap, instrument, pnlText, sub } from './classes';

interface SignalCardProps {
    signal: SignalRecord;
    entryPrice?: number;
    isNew?: boolean;
    onOpen: () => void;
}

/* Smaller action / side pill: replaces the shared badge's padding and size. */
const badgeFit = 'py-[2px] px-[7px] text-[10.5px] leading-[1.3]';

const MODE_DOT: Record<OrderMode, string> = {
    real: 'bg-sg-profit',
    capped: 'bg-sg-warn',
    shadow: 'bg-sg-info',
    paper: 'bg-sg-faint',
};

/** One signal as a tappable row: time + badges | instrument + strategy | price + P&L. */
export function SignalCard({ signal, entryPrice, isNew, onOpen }: SignalCardProps) {
    const side = inferSide(signal);
    const mode = orderMode(signal);
    const pnl = signalPnL(signal, entryPrice);

    return (
        <button type="button" className={cn(cardShell, cardTap, isNew ? 'border-sg-accent' : 'border-sg-line-soft')} onClick={onOpen}>
            <span className={cn(cardLeft, 'w-[96px]')}>
                <span className="text-sg-muted text-[11.5px] tabular-nums">{formatTime(signal.created_at)}</span>
                <span className="flex gap-[4px]">
                    <span className={cn(getActionBadge(signal.action), badgeFit)}>{signal.action.replace('WATCH_', 'W·')}</span>
                    {side !== '—' && <span className={cn(getSideBadge(side), badgeFit)}>{side}</span>}
                </span>
            </span>
            <span className={cardMid}>
                <span className={instrument}>{instrumentLabel(signal)}</span>
                <span className={sub}>
                    <span className={cn('w-[7px] h-[7px] shrink-0 rounded-[50%]', MODE_DOT[mode.mode] ?? 'bg-sg-faint')} title={mode.title} aria-hidden />
                    {mode.text} · {signal.strategy}
                </span>
            </span>
            <span className={cardRight}>
                <span className="text-[13px] font-semibold">{formatPrice(signal.price)}</span>
                {pnl !== null && <span className={cn(pnlText, pnlClass(pnl))}>{formatSignedPaise(pnl)}</span>}
            </span>
        </button>
    );
}
