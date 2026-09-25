import { PlusCircle } from 'lucide-react';
import { useStrikeStore } from '../../store/useStrikeStore';
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

function LiveOrderRow({ order }: { order: OpenOrder }) {
    const fallbackPrice = useFNOPrice(order.side);
    const currentPrice = order.currentPrice ?? fallbackPrice;

    // P&L calculation using FNO option premium
    let pnl: number | null = null;
    let pnlClass = '';
    if (order.buyPrice !== null && currentPrice !== null) {
        // FNO options: both CALL and PUT are BUY(entry) → SELL(exit)
        // Live P&L = current_option_premium - entry_option_premium
        pnl = +(currentPrice - order.buyPrice).toFixed(2);
        pnlClass = pnl > 0 ? 'price-up' : pnl < 0 ? 'price-down' : 'price-flat';
    }

    return (
        <tr className="signal-row">
            <td className="strategy-cell">{order.strategy}</td>
            <td>
                <span className={getSideBadge(order.side)}>{order.side}</span>
            </td>
            <td>{order.instrument}</td>
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
