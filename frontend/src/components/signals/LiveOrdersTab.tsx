import { PlusCircle } from 'lucide-react';
import { useStrikeStore } from '../../store/useStrikeStore';
import { findReversal, useAnalystStore } from '../../store/useAnalystStore';
import type { OpenOrder } from './signalAnalytics';
import { PositionControls } from './PositionControls';
import { actionBadge, cell, cellSub, emptyState, ewBadge, pnlTone, priceNa, rowMono, slKind, table, tableWrap, th, thNum } from './classes';

interface LiveOrdersTabProps {
    orders: OpenOrder[];
}

const slCell = 'inline-flex flex-col items-end gap-[3px]';
const lvlSub = 'block text-[0.68rem] text-sg-muted';

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
    if (side === 'CALL') return actionBadge('buy');
    if (side === 'PUT') return actionBadge('put');
    return actionBadge('none');
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
export function ExitWatchCell({ order }: { order: OpenOrder }) {
    const payload = useAnalystStore(s => s.reversal);
    const view = order.leg ? null : findReversal(payload, order.strategy, order.side);
    if (!view) return <span className={priceNa}>—</span>;
    const pct = Math.round(view.p * 100);
    const reasons = (view.reasons ?? []).join(', ');
    const title = [
        `reversal p=${pct}%`,
        `progress ${Math.round(view.progress * 100)}% (peak ${Math.round(view.peak_progress * 100)}%)`,
        view.exit_reason ? `exit: ${view.exit_reason}${view.exit_text ? ` (${view.exit_text})` : ''}` : '',
        view.phase === 'RUNNER' && view.next_level ? `next ${view.next_level.type} ${(view.next_level.price / 100).toFixed(2)}` : '',
        view.phase === 'RUNNER' && view.lock_level ? `lock ${(view.lock_level / 100).toFixed(2)}` : '',
        view.suggested_stop ? `suggested stop ${(view.suggested_stop / 100).toFixed(2)}` : '',
        reasons ? `reasons: ${reasons}` : '',
    ].filter(Boolean).join('\n');
    return (
        <div className="flex items-center gap-[6px]" title={title}>
            <span className={ewBadge(view.decision.toLowerCase())}>{view.decision}</span>
            <span className="w-[44px] h-[4px] rounded-pill bg-sg-line overflow-hidden"><span className="block h-full bg-sg-muted" style={{ width: `${pct}%` }} /></span>
            <span className="text-[0.72rem] text-sg-text-2 tabular-nums">{pct}%</span>
            {view.phase === 'RUNNER' && (
                <span className="text-[0.68rem] text-sg-info whitespace-nowrap">
                    runner{view.next_level ? ` → ${(view.next_level.price / 100).toFixed(0)}` : ''}
                    {view.lock_level ? ` · lock ${(view.lock_level / 100).toFixed(0)}` : ''}
                </span>
            )}
            {payload?.shadow && <span className="text-[0.62rem] text-sg-faint uppercase">shadow</span>}
        </div>
    );
}

/** Live premium (rupees) and P&L for an open position; shared with the phone card. */
export function useOrderLivePnL(order: OpenOrder): { currentPrice: number | null; pnl: number | null } {
    const fallbackPrice = useFNOPrice(order.side);
    // The ATM CE/PE premium is not a basket leg's premium: no fallback for legs.
    const currentPrice = order.currentPrice ?? (order.leg ? null : fallbackPrice);
    if (order.buyPrice === null || currentPrice === null) return { currentPrice, pnl: null };
    // Long option: live P&L = current premium − entry premium.
    // Short leg (sold to open): entry premium − current premium.
    const diff = order.short ? order.buyPrice - currentPrice : currentPrice - order.buyPrice;
    return { currentPrice, pnl: +diff.toFixed(2) };
}

/** Index stop and target (chart levels); "manual" once moved by hand. */
export function IndexLevels({ order }: { order: OpenOrder }) {
    if (!order.indexStop && !order.indexTarget) return <span className={priceNa}>—</span>;
    return (
        <div className={slCell}>
            <span className={lvlSub}>SL {order.indexStop?.toFixed(2) ?? '—'}</span>
            <span className={lvlSub}>TGT {order.indexTarget?.toFixed(2) ?? '—'}</span>
            {order.manual && <span className={slKind('manual')}>manual</span>}
        </div>
    );
}

function LiveOrderRow({ order }: { order: OpenOrder }) {
    const { currentPrice, pnl } = useOrderLivePnL(order);
    const tone = pnl === null ? undefined : pnlTone(pnl);

    return (
        <tr>
            <td className={cell(true, 'strategy')}>{order.strategy}</td>
            <td className={cell(true)}>
                <span className={getSideBadge(order.side)}>{order.side}</span>
            </td>
            <td className={cell(true)} title={order.instrument}>
                <span className={rowMono}>{order.fnoSymbol || order.instrument}</span>
                {order.leg && (
                    <span className={cellSub}>
                        {order.leg.replace('_', ' ')}{order.strike ? ` ${order.strike}` : ''}
                        {order.short && <span className={slKind('hard')} style={{ marginLeft: 6 }}>SHORT</span>}
                    </span>
                )}
            </td>
            <td className={cell(true, 'price')}>
                {order.buyPrice !== null ? `₹${order.buyPrice.toFixed(2)}` : '—'}
            </td>
            <td className={cell(true, 'price')}>
                {currentPrice !== null
                    ? <span className="text-sg-accent">₹{currentPrice.toFixed(2)}</span>
                    : <span className={priceNa}>—</span>}
            </td>
            <td className={cell(true, 'price')}>
                {order.stoplossPrice !== null
                    ? (
                        <div className={slCell}>
                            <span className="text-sg-warn font-medium">₹{order.stoplossPrice.toFixed(2)}</span>
                            {order.stoplossKind && (
                                <span className={slKind(order.stoplossKind.toLowerCase())}>
                                    {order.stoplossKind}
                                </span>
                            )}
                        </div>
                    )
                    : <span className={priceNa}>Pending</span>}
            </td>
            <td className={cell(true, 'price')}>
                {order.targetPrice ? `₹${order.targetPrice.toFixed(2)}` : <span className={priceNa}>—</span>}
            </td>
            <td className={cell(true, 'price')}>
                <IndexLevels order={order} />
            </td>
            <td className={cell(true, 'price', tone)}>
                {pnl !== null
                    ? `${pnl > 0 ? '+' : pnl < 0 ? '−' : ''}₹${Math.abs(pnl).toFixed(2)}`
                    : '—'}
            </td>
            <td className={cell(true)}><ExitWatchCell order={order} /></td>
            <td className={cell(true)}><PositionControls order={order} currentPrice={currentPrice} /></td>
            <td className={cell(true, 'time')} title={order.entryTime}>
                {formatTime(order.entryTime)}
                <span className={cellSub}>
                    {formatDate(order.entryTime)}
                </span>
            </td>
        </tr>
    );
}

export function LiveOrdersTab({ orders }: LiveOrdersTabProps) {
    if (orders.length === 0) {
        return (
            <div className={emptyState}>
                <PlusCircle size={48} strokeWidth={1.5} />
                <p>No open positions. New entries show here with live P&amp;L until they exit.</p>
            </div>
        );
    }

    return (
        <div className={tableWrap()}>
            <table className={table}>
                <thead>
                    <tr>
                        <th className={th} style={{ width: '140px' }}>Strategy</th>
                        <th className={th} style={{ width: '70px' }}>Side</th>
                        <th className={th} style={{ width: '140px' }}>Instrument</th>
                        <th className={thNum} style={{ width: '100px' }}>Entry</th>
                        <th className={thNum} style={{ width: '100px' }}>Current</th>
                        <th className={thNum} style={{ width: '100px' }}>Stop loss</th>
                        <th className={thNum} style={{ width: '90px' }}>Target</th>
                        <th className={thNum} style={{ width: '110px' }}>Index SL / TGT</th>
                        <th className={thNum} style={{ width: '100px' }}>P&L</th>
                        <th className={th} style={{ width: '170px' }}>Exit watch</th>
                        <th className={th} style={{ width: '170px' }}>Actions</th>
                        <th className={th} style={{ width: '120px' }}>Entry time</th>
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
