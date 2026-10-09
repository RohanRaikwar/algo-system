import type { ReactNode } from 'react';
import { BottomSheet } from '../../layout/BottomSheet';
import type { SignalRecord } from '../../../types/signal';
import { inferSide } from '../signalAnalytics';
import {
    formatDate, formatPrice, formatSignedPaise, formatTime, getActionBadge, getMarketBadge,
    getSideBadge, instrumentLabel, normalizeMarketState, orderMode, pnlClass, relativeTime, signalPnL,
} from '../signalFormat';
import styles from './signalsMobile.module.css';

interface Props {
    signal: SignalRecord | null;
    entry?: { price: number; time: string };
    onClose: () => void;
}

function Row({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className={styles.detailRow}>
            <dt>{label}</dt>
            <dd>{children}</dd>
        </div>
    );
}

/** Every field of one signal; replaces the desktop table's hover tooltips. */
export function SignalDetailSheet({ signal, entry, onClose }: Props) {
    const s = signal;
    const side = s ? inferSide(s) : '—';
    const mode = s ? orderMode(s) : null;
    const pnl = s ? signalPnL(s, entry?.price) : null;
    const market = s ? normalizeMarketState(s.market_state, s.reason) : '—';

    return (
        <BottomSheet open={s !== null} onClose={onClose} title={s ? `${s.action} · ${formatTime(s.created_at)}` : 'Signal'}>
            {s && mode && (
                <>
                    <div className={styles.detailBadges}>
                        <span className={getActionBadge(s.action)}>{s.action}</span>
                        {side !== '—' && <span className={getSideBadge(side)}>{side}</span>}
                        <span className={`order-type-badge ${mode.mode}`} title={mode.title}>{mode.text}</span>
                        {market !== '—' && <span className={getMarketBadge(market)}>{market}</span>}
                    </div>
                    <dl className={styles.detailList}>
                        <Row label="Instrument"><span className="mono">{instrumentLabel(s)}</span></Row>
                        <Row label="Strategy">{s.strategy}</Row>
                        <Row label="Price">{formatPrice(s.price)}</Row>
                        {entry && <Row label="Entry">{formatPrice(entry.price)} at {formatTime(entry.time)}</Row>}
                        {pnl !== null && <Row label="P&L"><span className={pnlClass(pnl)}>{formatSignedPaise(pnl)}</span></Row>}
                        {s.stoploss_price ? <Row label="Stop loss">{formatPrice(s.stoploss_price)}</Row> : null}
                        {s.qty ? <Row label="Qty">{s.qty}</Row> : null}
                        {s.strike ? <Row label="Strike">{s.strike}{s.leg ? ` · ${s.leg.replace('_', ' ')}` : ''}{s.short ? ' · short' : ''}</Row> : null}
                        <Row label="Time">{formatDate(s.created_at)} {formatTime(s.created_at)} · {relativeTime(s.created_at)}</Row>
                        {s.candle_ts && s.candle_ts !== s.created_at && <Row label="Candle">{formatTime(s.candle_ts)}</Row>}
                    </dl>
                    {s.reason && (
                        <section className={styles.reason}>
                            <h3>Reason</h3>
                            <p>{s.reason}</p>
                        </section>
                    )}
                </>
            )}
        </BottomSheet>
    );
}
