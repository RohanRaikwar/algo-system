import type { SignalRecord } from '../../../types/signal';
import { inferSide } from '../signalAnalytics';
import {
    formatPrice, formatSignedPaise, formatTime, getActionBadge, getSideBadge, instrumentLabel,
    orderMode, pnlClass, signalPnL,
} from '../signalFormat';
import styles from './signalsMobile.module.css';

interface SignalCardProps {
    signal: SignalRecord;
    entryPrice?: number;
    isNew?: boolean;
    onOpen: () => void;
}

/** One signal as a tappable row: time + badges | instrument + strategy | price + P&L. */
export function SignalCard({ signal, entryPrice, isNew, onOpen }: SignalCardProps) {
    const side = inferSide(signal);
    const mode = orderMode(signal);
    const pnl = signalPnL(signal, entryPrice);

    return (
        <button type="button" className={`${styles.card}${isNew ? ` ${styles.cardNew}` : ''}`} onClick={onOpen}>
            <span className={styles.cardLeft}>
                <span className={styles.time}>{formatTime(signal.created_at)}</span>
                <span className={styles.badges}>
                    <span className={getActionBadge(signal.action)}>{signal.action.replace('WATCH_', 'W·')}</span>
                    {side !== '—' && <span className={getSideBadge(side)}>{side}</span>}
                </span>
            </span>
            <span className={styles.cardMid}>
                <span className={styles.instrument}>{instrumentLabel(signal)}</span>
                <span className={styles.sub}>
                    <span className={`${styles.modeDot} ${styles[`mode_${mode.mode}`]}`} title={mode.title} aria-hidden />
                    {mode.text} · {signal.strategy}
                </span>
            </span>
            <span className={styles.cardRight}>
                <span className={styles.price}>{formatPrice(signal.price)}</span>
                {pnl !== null && <span className={`${styles.pnl} ${pnlClass(pnl)}`}>{formatSignedPaise(pnl)}</span>}
            </span>
        </button>
    );
}
