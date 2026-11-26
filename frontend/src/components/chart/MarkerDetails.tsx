import { Badge } from '@/components/ui/badge';
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { BottomSheet } from '../layout/BottomSheet';
import { useMediaQuery, PHONE_QUERY } from '../../hooks/useMediaQuery';
import type { TradeMarker, TradeMarkerKind } from './tradeMarkersPrimitive';

const KIND_TONE: Record<TradeMarkerKind, 'profit' | 'loss' | 'muted' | 'info' | 'warn'> = {
    entry: 'profit',
    exit: 'loss',
    refused: 'muted',
    watch: 'info',
    sweep: 'warn',
};

const KIND_WORD: Record<TradeMarkerKind, string> = {
    entry: 'Entry',
    exit: 'Exit',
    refused: 'Refused',
    watch: 'Advice',
    sweep: 'Sweep',
};

/** Title shown for a marker: its full label, else kind + side. */
export function markerTitle(m: TradeMarker): string {
    const t = m.title ?? m.label;
    if (t) return t;
    return [KIND_WORD[m.kind], m.side === 'C' ? 'CALL' : m.side === 'P' ? 'PUT' : ''].filter(Boolean).join(' ');
}

/**
 * Rows for the popup: the card's detail rows without the clipped reason
 * rows when the full reason is shown below them.
 */
export function markerRows(m: TradeMarker): string[] {
    const rows = [...(m.detail ?? m.lines)];
    if (!m.reason) return rows;
    // reasonLines() wraps the reason into the card's last rows; the popup
    // shows the whole text instead, so drop the trailing rows that are
    // pieces of it (never the first, meta row).
    const text = m.reason.replace(/\s+/g, ' ');
    while (rows.length > 1) {
        const last = rows[rows.length - 1].replace(/…$/, '').replace(/^Blocked: /, '').trim();
        if (last && !text.includes(last)) break;
        rows.pop();
    }
    return rows;
}

function MarkerItem({ m }: { m: TradeMarker }) {
    const rows = markerRows(m);
    return (
        <article className="flex flex-col gap-[6px] py-[12px] border-b border-b-sg-line-soft last:border-b-0">
            <div className="flex items-center gap-[8px] flex-wrap">
                <Badge tone={KIND_TONE[m.kind]}>{KIND_WORD[m.kind]}</Badge>
                <span className="font-semibold text-sg-text">{markerTitle(m)}</span>
            </div>
            {rows.map((r, i) => {
                const pnlRow = m.pnl !== undefined && /P&L/.test(r);
                return (
                    <p
                        key={i}
                        className={cn(
                            'm-0 text-[13px] tabular-nums',
                            i === 0 ? 'text-sg-muted' : 'text-sg-text-2',
                            pnlRow && (m.pnl! >= 0 ? 'text-sg-profit' : 'text-sg-loss'),
                        )}
                    >
                        {r}
                    </p>
                );
            })}
            {m.reason && (
                <p className="m-0 text-[13px] text-sg-text-2 whitespace-pre-wrap break-words">{m.reason}</p>
            )}
        </article>
    );
}

interface MarkerDetailsProps {
    markers: TradeMarker[] | null;
    onClose: () => void;
}

/** Full details of the chart markers under a click/tap: Dialog on desktop, bottom sheet on phones. */
export function MarkerDetails({ markers, onClose }: MarkerDetailsProps) {
    const isPhone = useMediaQuery(PHONE_QUERY);
    const open = !!markers && markers.length > 0;
    const list = markers ?? [];
    const title = list.length > 1 ? `${list.length} events` : list[0] ? markerTitle(list[0]) : '';
    const body = (
        <div className="flex flex-col min-w-0">
            {list.map((m, i) => <MarkerItem key={`${m.time}-${m.kind}-${i}`} m={m} />)}
        </div>
    );

    if (isPhone) {
        return (
            <BottomSheet open={open} onClose={onClose} title={title}>
                {body}
            </BottomSheet>
        );
    }
    return (
        <Dialog open={open} onOpenChange={o => { if (!o) onClose(); }}>
            <DialogContent showCloseButton className="sm:max-w-[520px] w-[min(96vw,520px)]">
                <DialogHeader>
                    <div>
                        <DialogTitle>{title}</DialogTitle>
                        <DialogDescription>Chart event details</DialogDescription>
                    </div>
                </DialogHeader>
                <DialogBody className="max-h-[70vh] overflow-y-auto">{body}</DialogBody>
            </DialogContent>
        </Dialog>
    );
}
