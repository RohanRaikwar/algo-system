import type { OpenOrder } from '../signalAnalytics';
import { ExitWatchCell, useOrderLivePnL } from '../LiveOrdersTab';
import { formatTime, getSideBadge } from '../signalFormat';
import styles from './signalsMobile.module.css';

const rupees = (v: number | null) => (v === null ? '—' : `₹${v.toFixed(2)}`);

/** Open position: live P&L big, entry → current, stop and exit-watch verdict. */
export function PositionCard({ order }: { order: OpenOrder }) {
    const { currentPrice, pnl } = useOrderLivePnL(order);
    const tone = pnl === null ? '' : pnl > 0 ? 'price-up' : pnl < 0 ? 'price-down' : 'price-flat';

    return (
        <article className={styles.position}>
            <div className={styles.posTop}>
                <span className={getSideBadge(order.side)}>{order.side}</span>
                <span className={styles.instrument}>{order.fnoSymbol || order.instrument}</span>
                {order.short && <span className="sl-kind hard">SHORT</span>}
                <span className={`${styles.posPnl} ${tone}`}>
                    {pnl === null ? '—' : `${pnl > 0 ? '+' : pnl < 0 ? '−' : ''}₹${Math.abs(pnl).toFixed(2)}`}
                </span>
            </div>
            <dl className={styles.posGrid}>
                <div><dt>Entry</dt><dd>{rupees(order.buyPrice)}</dd></div>
                <div><dt>Current</dt><dd className="live-price-pulse">{rupees(currentPrice)}</dd></div>
                <div>
                    <dt>Stop{order.stoplossKind ? ` · ${order.stoplossKind.toLowerCase()}` : ''}</dt>
                    <dd>{order.stoplossPrice === null ? 'Pending' : rupees(order.stoplossPrice)}</dd>
                </div>
            </dl>
            <div className={styles.posFoot}>
                <span className={styles.sub}>{order.strategy} · since {formatTime(order.entryTime)}</span>
                <ExitWatchCell order={order} />
            </div>
        </article>
    );
}
