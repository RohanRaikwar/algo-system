import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { TableCell, TableRow } from '@/components/ui/table';
import type { SignalRecord } from '../../types/signal';
import { inferSide } from './signalAnalytics';
import {
    actionBadgeTone, formatDate, formatPrice, formatSignedPaise, formatTime,
    instrumentLabel, normalizeMarketState, orderMode, pnlClass, relativeTime, sideBadgeTone, signalPnL,
} from './signalFormat';
import { cellSub, marketTone, orderTypeTone, rowMono } from './classes';

interface SignalRowProps {
    signal: SignalRecord;
    isNew?: boolean;
    /** FNO entry price (paise) from the matching BUY signal — used to show P&L on EXITs */
    entryPrice?: number;
    /** Entry time from the matching BUY signal */
    entryTime?: string;
}

export function SignalRow({ signal, isNew, entryPrice, entryTime }: SignalRowProps) {
    const side = inferSide(signal);
    const marketState = normalizeMarketState(signal.market_state, signal.reason);

    const pnl = signalPnL(signal, entryPrice);
    const mode = orderMode(signal);

    return (
        <TableRow className={isNew ? 'animate-[signal-pulse_1.6s_ease-out] motion-reduce:animate-none' : undefined}>
            <TableCell kind="time" title={signal.created_at}>
                {formatTime(signal.created_at)}
                <span className={cellSub}>
                    {formatDate(signal.created_at)}, {relativeTime(signal.created_at)}
                </span>
            </TableCell>
            <TableCell>
                <Badge tone={actionBadgeTone(signal.action)}>
                    {signal.action}
                </Badge>
            </TableCell>
            <TableCell>
                <Badge tone={sideBadgeTone(side)}>
                    {side}
                </Badge>
            </TableCell>
            <TableCell>
                <Badge variant="market" tone={marketTone(marketState)}>
                    {marketState}
                </Badge>
            </TableCell>
            <TableCell kind="strategy" title={signal.strategy}>
                {signal.strategy}
            </TableCell>
            <TableCell>
                <Badge variant="order" tone={orderTypeTone(mode.mode)} title={mode.title}>
                    {mode.text}
                </Badge>
            </TableCell>
            <TableCell title={`${signal.exchange}:${signal.token}`}>
                <span className={signal.fno_symbol ? rowMono : undefined}>{instrumentLabel(signal)}</span>
            </TableCell>
            <TableCell kind="price">
                {formatPrice(signal.price)}
                {pnl !== null && (
                    <>
                        <span className={cellSub}>
                            entry {formatPrice(entryPrice)}
                        </span>
                        <span className={cn('block mt-[2px] text-[0.8rem] font-semibold', pnl === 0 ? undefined : pnlClass(pnl))}>
                            {formatSignedPaise(pnl)}
                        </span>
                    </>
                )}
            </TableCell>
            <TableCell kind="reason" title={signal.reason}>{signal.reason}</TableCell>
        </TableRow>
    );
}
