import type { SignalRecord } from '../../types/signal';
import { inferSide } from './signalAnalytics';
import {
    formatDate, formatPrice, formatSignedPaise, formatTime, getActionBadge, getMarketBadge,
    getSideBadge, instrumentLabel, normalizeMarketState, orderMode, pnlClass, relativeTime, signalPnL,
} from './signalFormat';

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
        <tr className={`signal-row${isNew ? ' new-signal' : ''}`}>
            <td className="signal-time" title={signal.created_at}>
                {formatTime(signal.created_at)}
                <span className="signal-time-sub">
                    {formatDate(signal.created_at)}, {relativeTime(signal.created_at)}
                </span>
            </td>
            <td>
                <span className={getActionBadge(signal.action)}>
                    {signal.action}
                </span>
            </td>
            <td>
                <span className={getSideBadge(side)}>
                    {side}
                </span>
            </td>
            <td>
                <span className={getMarketBadge(marketState)}>
                    {marketState}
                </span>
            </td>
            <td className="strategy-cell" title={signal.strategy}>
                {signal.strategy}
            </td>
            <td>
                <span className={`order-type-badge ${mode.mode}`} title={mode.title}>
                    {mode.text}
                </span>
            </td>
            <td title={`${signal.exchange}:${signal.token}`}>
                <span className={signal.fno_symbol ? 'mono' : undefined}>{instrumentLabel(signal)}</span>
            </td>
            <td className="price-cell">
                {formatPrice(signal.price)}
                {pnl !== null && (
                    <>
                        <span className="price-sub">
                            entry {formatPrice(entryPrice)}
                        </span>
                        <span className={`price-pnl ${pnl === 0 ? '' : pnlClass(pnl)}`}>
                            {formatSignedPaise(pnl)}
                        </span>
                    </>
                )}
            </td>
            <td className="signal-reason" title={signal.reason}>{signal.reason}</td>
        </tr>
    );
}
