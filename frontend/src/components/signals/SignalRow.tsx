import type { SignalRecord } from '../../types/signal';
import { inferSide } from './signalAnalytics';

interface SignalRowProps {
    signal: SignalRecord;
    isNew?: boolean;
    /** FNO entry price (paise) from the matching BUY signal — used to show P&L on EXITs */
    entryPrice?: number;
    /** Entry time from the matching BUY signal */
    entryTime?: string;
}

function formatTime(ts: string): string {
    try {
        const d = new Date(ts);
        return d.toLocaleTimeString('en-IN', {
            hour: '2-digit', minute: '2-digit', second: '2-digit',
            hour12: false, timeZone: 'Asia/Kolkata',
        });
    } catch {
        return ts;
    }
}

function formatDate(ts: string): string {
    try {
        const d = new Date(ts);
        return d.toLocaleDateString('en-IN', {
            day: '2-digit', month: 'short',
            timeZone: 'Asia/Kolkata',
        });
    } catch {
        return '';
    }
}

function relativeTime(ts: string): string {
    try {
        const diff = Date.now() - new Date(ts).getTime();
        if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`;
        if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
        if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
        return `${Math.floor(diff / 86400000)}d ago`;
    } catch {
        return '';
    }
}

function getActionBadge(action: string): string {
    if (action === 'EXIT') return 'action-badge exit';
    return 'action-badge buy-call';
}

function getSideBadge(side: string): string {
    if (side === 'CALL') return 'action-badge buy-call';
    if (side === 'PUT') return 'action-badge buy-put';
    return 'action-badge';
}

function normalizeMarketState(marketState?: string, reason?: string): string {
    const raw = (marketState || '').trim().toLowerCase();
    if (raw === 'trending' || raw === 'sideways' || raw === 'choppy' || raw === 'range') {
        return raw.toUpperCase();
    }

    const hay = `${reason || ''}`.toLowerCase();
    if (/\brange\b/.test(hay)) return 'RANGE';
    if (/\bchoppy\b/.test(hay)) return 'CHOPPY';
    if (/\bsideways\b/.test(hay)) return 'SIDEWAYS';
    if (/\bmomentum\b/.test(hay)) return 'TRENDING';
    return '—';
}

function getMarketBadge(state: string): string {
    if (state === 'TRENDING') return 'market-badge trending';
    if (state === 'SIDEWAYS') return 'market-badge sideways';
    if (state === 'CHOPPY') return 'market-badge choppy';
    if (state === 'RANGE') return 'market-badge range';
    return 'market-badge';
}

function formatPrice(price?: number): string {
    if (price === undefined || price === null || price < 0) return '—';
    return `₹${(price / 100).toFixed(2)}`;
}

export function SignalRow({ signal, isNew, entryPrice, entryTime }: SignalRowProps) {
    const side = inferSide(signal);
    const marketState = normalizeMarketState(signal.market_state, signal.reason);

    // Compute P&L for EXIT signals when entry price is available
    const isExit = signal.action === 'EXIT';
    const exitPrice = signal.price;
    const hasPnL = isExit && entryPrice && entryPrice > 0 && exitPrice && exitPrice > 0;
    const pnlPaise = hasPnL ? exitPrice - entryPrice : 0;
    const pnlClass = pnlPaise > 0 ? 'price-up' : pnlPaise < 0 ? 'price-down' : '';

    // Order mode badge - two states:
    // 1. LIVE (live_mode=true) - Real orders placed via broker
    // 2. PAPER (live_mode=false) - Paper trading / simulation
    const isLiveMode = signal.live_mode === true;
    
    let badgeText = 'Paper';
    let badgeClass = 'paper';
    let badgeTitle = 'Paper trade (simulation only)';

    // Profit cap takes precedence for visually distinguishing from manual paper mode
    if (signal.profit_cap) {
        badgeText = 'Capped';
        badgeClass = 'capped';
        badgeTitle = 'Paper trade: daily profit cap reached';
    } else if (isLiveMode) {
        badgeText = 'Real';
        badgeClass = 'real';
        badgeTitle = 'Real order placed via broker';
    }

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
                <span className={`order-type-badge ${badgeClass}`} title={badgeTitle}>
                    {badgeText}
                </span>
            </td>
            <td>{signal.exchange}:{signal.token}</td>
            <td className="price-cell">
                {formatPrice(signal.price)}
                {hasPnL && (
                    <>
                        <span className="price-sub">
                            entry {formatPrice(entryPrice)}
                        </span>
                        <span className={`price-pnl ${pnlClass}`}>
                            {pnlPaise > 0 ? '+' : pnlPaise < 0 ? '−' : ''}₹{(Math.abs(pnlPaise) / 100).toFixed(2)}
                        </span>
                    </>
                )}
            </td>
            <td className="signal-reason" title={signal.reason}>{signal.reason}</td>
        </tr>
    );
}
