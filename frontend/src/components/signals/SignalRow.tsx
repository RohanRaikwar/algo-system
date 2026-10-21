import { cn } from '@/lib/utils';
import type { SignalRecord } from '../../types/signal';
import { inferSide } from './signalAnalytics';
import {
    formatDate, formatPrice, formatSignedPaise, formatTime, getActionBadge, getMarketBadge,
    getSideBadge, instrumentLabel, normalizeMarketState, orderMode, pnlClass, relativeTime, signalPnL,
} from './signalFormat';
import { cell, cellSub, orderTypeBadge, rowMono } from './classes';

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
        <tr className={isNew ? 'animate-[signal-pulse_1.6s_ease-out] motion-reduce:animate-none' : undefined}>
            <td className={cell(false, 'time')} title={signal.created_at}>
                {formatTime(signal.created_at)}
                <span className={cellSub}>
                    {formatDate(signal.created_at)}, {relativeTime(signal.created_at)}
                </span>
            </td>
            <td className={cell(false)}>
                <span className={getActionBadge(signal.action)}>
                    {signal.action}
                </span>
            </td>
            <td className={cell(false)}>
                <span className={getSideBadge(side)}>
                    {side}
                </span>
            </td>
            <td className={cell(false)}>
                <span className={getMarketBadge(marketState)}>
                    {marketState}
                </span>
            </td>
            <td className={cell(false, 'strategy')} title={signal.strategy}>
                {signal.strategy}
            </td>
            <td className={cell(false)}>
                <span className={orderTypeBadge(mode.mode)} title={mode.title}>
                    {mode.text}
                </span>
            </td>
            <td className={cell(false)} title={`${signal.exchange}:${signal.token}`}>
                <span className={signal.fno_symbol ? rowMono : undefined}>{instrumentLabel(signal)}</span>
            </td>
            <td className={cell(false, 'price')}>
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
            </td>
            <td className={cell(false, 'reason')} title={signal.reason}>{signal.reason}</td>
        </tr>
    );
}
