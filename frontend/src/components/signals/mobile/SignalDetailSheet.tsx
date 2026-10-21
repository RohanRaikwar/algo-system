import type { ReactNode } from 'react';
import { BottomSheet } from '../../layout/BottomSheet';
import type { SignalRecord } from '../../../types/signal';
import { inferSide } from '../signalAnalytics';
import {
    formatDate, formatPrice, formatSignedPaise, formatTime, getActionBadge, getMarketBadge,
    getSideBadge, instrumentLabel, normalizeMarketState, orderMode, pnlClass, relativeTime, signalPnL,
} from '../signalFormat';
import { orderTypeBadge } from '../classes';

interface Props {
    signal: SignalRecord | null;
    entry?: { price: number; time: string };
    onClose: () => void;
}

function Row({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className="flex justify-between gap-[16px] py-[9px] px-0 border-b border-b-sg-line-soft text-[13px]">
            <dt className="text-sg-muted">{label}</dt>
            <dd className="m-0 text-right font-medium tabular-nums select-text">{children}</dd>
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
                    <div className="flex flex-wrap gap-[6px] mb-[14px]">
                        <span className={getActionBadge(s.action)}>{s.action}</span>
                        {side !== '—' && <span className={getSideBadge(side)}>{side}</span>}
                        <span className={orderTypeBadge(mode.mode)} title={mode.title}>{mode.text}</span>
                        {market !== '—' && <span className={getMarketBadge(market)}>{market}</span>}
                    </div>
                    <dl className="flex flex-col m-0">
                        <Row label="Instrument"><span>{instrumentLabel(s)}</span></Row>
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
                        <section className="mt-[14px]">
                            <h3 className="mt-0 mx-0 mb-[6px] text-sg-muted text-[12px] font-semibold">Reason</h3>
                            <p className="m-0 text-sg-text-2 font-mono text-[12px] leading-[1.55] [word-break:break-word] select-text">{s.reason}</p>
                        </section>
                    )}
                </>
            )}
        </BottomSheet>
    );
}
