import { PlusCircle } from 'lucide-react';
import { useStrikeStore } from '../../store/useStrikeStore';
import { findReversal, useAnalystStore } from '../../store/useAnalystStore';
import type { OpenOrder } from './signalAnalytics';

interface LiveOrdersTabProps {
    orders: OpenOrder[];
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

function getSideBadge(side: string) {
    if (side === 'CALL') return 'action-badge buy-call';
    if (side === 'PUT') return 'action-badge buy-put';
    return 'action-badge';
}

/**
 * Get the live FNO option premium for a CALL or PUT order.
 * Uses the strike store's callLTP/putLTP (paise) → converts to rupees.
 */
function useFNOPrice(side: string): number | null {
    const callLTP = useStrikeStore(s => s.callLTP);
    const putLTP = useStrikeStore(s => s.putLTP);

    if (side === 'CALL' && callLTP > 0) return callLTP / 100;
    if (side === 'PUT' && putLTP > 0) return putLTP / 100;
    return null;
}

/**
 * Exit-watch verdict for the position: reversal probability, HOLD/TIGHTEN/EXIT
 * and the top reasons. SHADOW means stratengine does not act on it yet.
 */
function ExitWatchCell({ order }: { order: OpenOrder }) {
    const payload = useAnalystStore(s => s.reversal);
    const view = order.leg ? null : findReversal(payload, order.strategy, order.side);
    if (!view) return <span className="price-na">—</span>;
    const pct = Math.round(view.p * 100);
    const reasons = (view.reasons ?? []).join(', ');
    const title = [
        `reversal p=${pct}%`,
        `progress ${Math.round(view.progress * 100)}% (peak ${Math.round(view.peak_progress * 100)}%)`,
        view.exit_reason ? `exit: ${view.exit_reason}` : '',
        view.suggested_stop ? `suggested stop ${(view.suggested_stop / 100).toFixed(2)}` : '',
        reasons ? `reasons: ${reasons}` : '',
    ].filter(Boolean).join('\n');
    return (
        <div className="ew-cell" title={title}>
            <span className={`ew-badge ew-${view.decision.toLowerCase()}`}>{view.decision}</span>
            <span className="ew-bar"><span className="ew-fill" style={{ width: `${pct}%` }} /></span>
            <span className="ew-pct">{pct}%</span>
            {payload?.shadow && <span className="ew-shadow">shadow</span>}
        </div>
    );
}

function LiveOrderRow({ order }: { order: OpenOrder }) {
    const fallbackPrice = useFNOPrice(order.side);
    // The ATM CE/PE premium is not a condor leg's premium: no fallback for legs.
    const currentPrice = order.currentPrice ?? (order.leg ? null : fallbackPrice);

    // P&L calculation using FNO option premium
    let pnl: number | null = null;
    let pnlClass = '';
    if (order.buyPrice !== null && currentPrice !== null) {
        // Long option: live P&L = current premium − entry premium.
        // Short leg (sold to open): entry premium − current premium.
        const diff = order.short ? order.buyPrice - currentPrice : currentPrice - order.buyPrice;
        pnl = +diff.toFixed(2);
        pnlClass = pnl > 0 ? 'price-up' : pnl < 0 ? 'price-down' : 'price-flat';
    }

    return (
        <tr className="signal-row">
            <td className="strategy-cell">{order.strategy}</td>
            <td>
                <span className={getSideBadge(order.side)}>{order.side}</span>
            </td>
            <td title={order.instrument}>
                <span className="mono">{order.fnoSymbol || order.instrument}</span>
                {order.leg && (
                    <span className="signal-time-sub">
                        {order.leg.replace('_', ' ')}{order.strike ? ` ${order.strike}` : ''}
                        {order.short && <span className="sl-kind hard" style={{ marginLeft: 6 }}>SHORT</span>}
                    </span>
                )}
            </td>
            <td className="price-cell">
                {order.buyPrice !== null ? `₹${order.buyPrice.toFixed(2)}` : '—'}
            </td>
            <td className="price-cell">
                {currentPrice !== null
                    ? <span className="live-price-pulse">₹{currentPrice.toFixed(2)}</span>
                    : <span className="price-na">—</span>}
            </td>
            <td className="price-cell">
                {order.stoplossPrice !== null
                    ? (
                        <div className="sl-cell">
                            <span className="sl-price">₹{order.stoplossPrice.toFixed(2)}</span>
                            {order.stoplossKind && (
                                <span className={`sl-kind ${order.stoplossKind.toLowerCase()}`}>
                                    {order.stoplossKind}
                                </span>
                            )}
                        </div>
                    )
                    : <span className="price-na">Pending</span>}
            </td>
            <td className={`price-cell ${pnlClass}`}>
                {pnl !== null
                    ? `${pnl > 0 ? '+' : pnl < 0 ? '−' : ''}₹${Math.abs(pnl).toFixed(2)}`
                    : '—'}
            </td>
            <td><ExitWatchCell order={order} /></td>
            <td className="signal-time" title={order.entryTime}>
                {formatTime(order.entryTime)}
                <span className="signal-time-sub">
                    {formatDate(order.entryTime)}
                </span>
            </td>
        </tr>
    );
}

export function LiveOrdersTab({ orders }: LiveOrdersTabProps) {
    if (orders.length === 0) {
        return (
            <div className="signals-empty">
                <PlusCircle size={48} strokeWidth={1.5} />
                <p>No open positions. New entries show here with live P&amp;L until they exit.</p>
            </div>
        );
    }

    return (
        <div className="signals-table-wrap">
            <table className="signals-table compact-table">
                <thead>
                    <tr>
                        <th style={{ width: '140px' }}>Strategy</th>
                        <th style={{ width: '70px' }}>Side</th>
                        <th style={{ width: '140px' }}>Instrument</th>
                        <th className="num" style={{ width: '100px' }}>Entry</th>
                        <th className="num" style={{ width: '100px' }}>Current</th>
                        <th className="num" style={{ width: '100px' }}>Stop loss</th>
                        <th className="num" style={{ width: '100px' }}>P&L</th>
                        <th style={{ width: '170px' }}>Exit watch</th>
                        <th style={{ width: '120px' }}>Entry time</th>
                    </tr>
                </thead>
                <tbody>
                    {orders.map((o, i) => (
                        <LiveOrderRow key={`${o.strategy}-${o.instrument}-${i}`} order={o} />
                    ))}
                </tbody>
            </table>
        </div>
    );
}
