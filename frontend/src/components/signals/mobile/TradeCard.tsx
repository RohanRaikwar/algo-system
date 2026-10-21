import type { DailyCompletedOrder } from '../../../services/api';
import { formatPrice, formatSignedPaise, getSideBadge, pnlClass } from '../signalFormat';
import { cn } from '@/lib/utils';
import { Card } from '@/components/ui/card';
import { cardLeft, cardMid, cardRight, instrument, pnlText, sub, subBase } from './classes';

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
        <Card variant="row" render={<article />}>
            <span className={cn(cardLeft, 'w-auto')}>
                <span className={getSideBadge(order.side)}>{order.side}</span>
            </span>
            <span className={cardMid}>
                <span className={instrument}>{order.fno_symbol || order.instrument}</span>
                <span className={sub}>
                    {formatPrice(order.entry_price)} {hhmm(order.entry_time)} → {formatPrice(order.exit_price)} {hhmm(order.exit_time)}
                </span>
            </span>
            <span className={cardRight}>
                <span className={cn(pnlText, pnlClass(order.realized_pnl))}>{formatSignedPaise(order.realized_pnl)}</span>
                <span className={cn(subBase, pnlClass(pts))}>{pts > 0 ? '+' : ''}{pts.toFixed(1)} pts</span>
            </span>
        </Card>
    );
}
