import type { DailyCompletedOrder } from '../../../services/api';
import { formatPrice, formatSignedPaise, getSideBadge, pnlClass } from '../signalFormat';
import styles from './signalsMobile.module.css';

const hhmm = (ts: string) => {
    try {
        return new Date(ts).toLocaleTimeString('en-IN', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: 'Asia/Kolkata' });
    } catch {
        return '';
    }
};

/** Completed trade: entry → exit with times, realized P&L and points. */
export function TradeCard({ order }: { order: DailyCompletedOrder }) {
    const pts = (order.exit_price - order.entry_price) / 100;
    return (
        <article className={styles.trade}>
            <span className={styles.cardLeft}>
                <span className={getSideBadge(order.side)}>{order.side}</span>
            </span>
            <span className={styles.cardMid}>
                <span className={styles.instrument}>{order.fno_symbol || order.instrument}</span>
                <span className={styles.sub}>
                    {formatPrice(order.entry_price)} {hhmm(order.entry_time)} → {formatPrice(order.exit_price)} {hhmm(order.exit_time)}
                </span>
            </span>
            <span className={styles.cardRight}>
                <span className={`${styles.pnl} ${pnlClass(order.realized_pnl)}`}>{formatSignedPaise(order.realized_pnl)}</span>
                <span className={`${styles.sub} ${pnlClass(pts)}`}>{pts > 0 ? '+' : ''}{pts.toFixed(1)} pts</span>
            </span>
        </article>
    );
}
