import { PlusCircle } from 'lucide-react';
import { useStrikeStore } from '../../store/useStrikeStore';
import { findReversal, useAnalystStore } from '../../store/useAnalystStore';
import type { OpenOrder } from './signalAnalytics';
import { PositionControls } from './PositionControls';
import { Badge } from '@/components/ui/badge';
import { Empty } from '@/components/ui/empty';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { sideBadgeTone } from './signalFormat';
import { cellSub, ewTone, pnlToneKey, priceNa, rowMono, slKindTone, tableWrap } from './classes';

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
            <Badge variant="tag" tone={ewTone(view.decision.toLowerCase())}>{view.decision}</Badge>
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
            {order.manual && <Badge variant="kind" tone={slKindTone('manual')}>manual</Badge>}
        </div>
    );
}

function LiveOrderRow({ order }: { order: OpenOrder }) {
    const { currentPrice, pnl } = useOrderLivePnL(order);
    const tone = pnl === null ? undefined : pnlToneKey(pnl);

    return (
        <TableRow>
            <TableCell density="compact" kind="strategy">{order.strategy}</TableCell>
            <TableCell density="compact">
                <Badge tone={sideBadgeTone(order.side)}>{order.side}</Badge>
            </TableCell>
            <TableCell density="compact" title={order.instrument}>
                <span className={rowMono}>{order.fnoSymbol || order.instrument}</span>
                {order.leg && (
                    <span className={cellSub}>
                        {order.leg.replace('_', ' ')}{order.strike ? ` ${order.strike}` : ''}
                        {order.short && <Badge variant="kind" tone={slKindTone('hard')} style={{ marginLeft: 6 }}>SHORT</Badge>}
                    </span>
                )}
            </TableCell>
            <TableCell density="compact" kind="price">
                {order.buyPrice !== null ? `₹${order.buyPrice.toFixed(2)}` : '—'}
            </TableCell>
            <TableCell density="compact" kind="price">
                {currentPrice !== null
                    ? <span className="text-sg-accent">₹{currentPrice.toFixed(2)}</span>
                    : <span className={priceNa}>—</span>}
            </TableCell>
            <TableCell density="compact" kind="price">
                {order.stoplossPrice !== null
                    ? (
                        <div className={slCell}>
                            <span className="text-sg-warn font-medium">₹{order.stoplossPrice.toFixed(2)}</span>
                            {order.stoplossKind && (
                                <Badge variant="kind" tone={slKindTone(order.stoplossKind.toLowerCase())}>
                                    {order.stoplossKind}
                                </Badge>
                            )}
                        </div>
                    )
                    : <span className={priceNa}>Pending</span>}
            </TableCell>
            <TableCell density="compact" kind="price">
                {order.targetPrice ? `₹${order.targetPrice.toFixed(2)}` : <span className={priceNa}>—</span>}
            </TableCell>
            <TableCell density="compact" kind="price">
                <IndexLevels order={order} />
            </TableCell>
            <TableCell density="compact" kind="price" tone={tone}>
                {pnl !== null
                    ? `${pnl > 0 ? '+' : pnl < 0 ? '−' : ''}₹${Math.abs(pnl).toFixed(2)}`
                    : '—'}
            </TableCell>
            <TableCell density="compact"><ExitWatchCell order={order} /></TableCell>
            <TableCell density="compact"><PositionControls order={order} currentPrice={currentPrice} /></TableCell>
            <TableCell density="compact" kind="time" title={order.entryTime}>
                {formatTime(order.entryTime)}
                <span className={cellSub}>
                    {formatDate(order.entryTime)}
                </span>
            </TableCell>
        </TableRow>
    );
}

export function LiveOrdersTab({ orders }: LiveOrdersTabProps) {
    if (orders.length === 0) {
        return (
            <Empty>
                <PlusCircle size={48} strokeWidth={1.5} />
                <p>No open positions. New entries show here with live P&amp;L until they exit.</p>
            </Empty>
        );
    }

    return (
        <div className={tableWrap()}>
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead style={{ width: '140px' }}>Strategy</TableHead>
                        <TableHead style={{ width: '70px' }}>Side</TableHead>
                        <TableHead style={{ width: '140px' }}>Instrument</TableHead>
                        <TableHead numeric style={{ width: '100px' }}>Entry</TableHead>
                        <TableHead numeric style={{ width: '100px' }}>Current</TableHead>
                        <TableHead numeric style={{ width: '100px' }}>Stop loss</TableHead>
                        <TableHead numeric style={{ width: '90px' }}>Target</TableHead>
                        <TableHead numeric style={{ width: '110px' }}>Index SL / TGT</TableHead>
                        <TableHead numeric style={{ width: '100px' }}>P&L</TableHead>
                        <TableHead style={{ width: '170px' }}>Exit watch</TableHead>
                        <TableHead style={{ width: '170px' }}>Actions</TableHead>
                        <TableHead style={{ width: '120px' }}>Entry time</TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {orders.map((o, i) => (
                        <LiveOrderRow key={`${o.strategy}-${o.instrument}-${i}`} order={o} />
                    ))}
                </TableBody>
            </Table>
        </div>
    );
}
