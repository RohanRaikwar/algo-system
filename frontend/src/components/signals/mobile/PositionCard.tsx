import type { OpenOrder } from '../signalAnalytics';
import { ExitWatchCell, IndexLevels, useOrderLivePnL } from '../LiveOrdersTab';
import { PositionControls } from '../PositionControls';
import { formatTime, getSideBadge, pnlClass } from '../signalFormat';
import { slKind } from '../classes';
import { cn } from '@/lib/utils';
import { instrument, sub } from './classes';

const rupees = (v: number | null) => (v === null ? '—' : `₹${v.toFixed(2)}`);

const dt = 'text-sg-muted text-[11px]';
const dd = 'mt-[2px] mx-0 mb-0 text-[13px] font-semibold tabular-nums';

/** Open position: live P&L big, entry → current, stop and exit-watch verdict. */
export function PositionCard({ order }: { order: OpenOrder }) {
    const { currentPrice, pnl } = useOrderLivePnL(order);
    const tone = pnlClass(pnl);

    return (
        <article className="flex flex-col gap-[10px] p-[12px] border border-solid border-sg-line-soft rounded-[12px] bg-sg-surface">
            <div className="flex items-center gap-[8px] min-w-0">
                <span className={getSideBadge(order.side)}>{order.side}</span>
                <span className={cn(instrument, 'flex-1')}>{order.fnoSymbol || order.instrument}</span>
                {order.short && <span className={slKind('hard')}>SHORT</span>}
                <span className={cn('text-[18px] font-bold tabular-nums', tone)}>
                    {pnl === null ? '—' : `${pnl > 0 ? '+' : pnl < 0 ? '−' : ''}₹${Math.abs(pnl).toFixed(2)}`}
                </span>
            </div>
            <dl className="grid grid-cols-[repeat(3,1fr)] gap-[8px] m-0">
                <div><dt className={dt}>Entry</dt><dd className={dd}>{rupees(order.buyPrice)}</dd></div>
                <div><dt className={dt}>Current</dt><dd className={cn(dd, 'text-sg-accent')}>{rupees(currentPrice)}</dd></div>
                <div>
                    <dt className={dt}>Stop{order.stoplossKind ? ` · ${order.stoplossKind.toLowerCase()}` : ''}</dt>
                    <dd className={dd}>{order.stoplossPrice === null ? 'Pending' : rupees(order.stoplossPrice)}</dd>
                </div>
                <div><dt className={dt}>Target</dt><dd className={dd}>{rupees(order.targetPrice ?? null)}</dd></div>
                <div><dt className={dt}>Index</dt><dd className={dd}><IndexLevels order={order} /></dd></div>
            </dl>
            <div className="flex items-center justify-between gap-[8px] flex-wrap">
                <span className={sub}>{order.strategy} · since {formatTime(order.entryTime)}</span>
                <ExitWatchCell order={order} />
            </div>
            <PositionControls order={order} currentPrice={currentPrice} />
        </article>
    );
}
