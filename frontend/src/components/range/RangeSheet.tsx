import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type RefObject } from 'react';
import { X } from 'lucide-react';
import { useRangeStore } from '../../store/useRangeStore';
import { RANGE_KIND_LABEL, type RangeEntryKind } from '../signals/rangeKind';
import { REGIME_LABEL, asOf, hhmm, pnlPaise, points, rupees, watching } from './rangeFormat';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import styles from './RangeMobile.module.css';

export type SheetSnap = 'closed' | 'half' | 'full';

interface RangeSheetProps {
    id: string;
    snap: SheetSnap;
    onSnap: (s: SheetSnap) => void;
    /** Gets focus back when the sheet closes (the peek strip). */
    returnFocusRef: RefObject<HTMLElement | null>;
}

const HALF = 0.5;
const FULL = 0.9;

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
    return (
        <div className={styles.stat}>
            <span className={styles.statLabel}>{label}</span>
            <span className={styles.statValue}>{value}</span>
            {sub && <span className={styles.statSub}>{sub}</span>}
        </div>
    );
}

function SheetBody() {
    const v = useRangeStore(s => s.view);
    const last = useLastNiftyPaise(v?.key);

    if (!v) return <p className={styles.muted}>Waiting for the strategy engine…</p>;

    const watch = watching(v);
    const watchLive = watch.kind === 'pending' || (watch.kind === 'flag' && watch.ready);
    const inPosition = v.side !== 'NONE';
    const kindLabel = v.kind ? RANGE_KIND_LABEL[v.kind as RangeEntryKind] ?? v.kind : '';
    const pnl = pnlPaise(v, last);
    const hasLevels = Boolean(v.support && v.resistance);

    return (
        <>
            {inPosition && (
                <section className={styles.card} aria-label="Position">
                    <div className={styles.posHead}>
                        <span className={`${styles.side} ${v.side === 'CALL' ? styles.call : styles.put}`}>{v.side}</span>
                        <span className={styles.kind}>{kindLabel}</span>
                        {v.strike ? <span className={styles.muted}>{v.strike} {v.side === 'CALL' ? 'CE' : 'PE'}</span> : null}
                        {pnl !== null && <span className={`${styles.pnl} ${pnl >= 0 ? styles.up : styles.down}`}>{points(pnl, true)}</span>}
                    </div>
                    <div className={styles.stats}>
                        <Stat label="Entry" value={rupees(v.entry)} />
                        <Stat label="Stop" value={rupees(v.stop)} sub={last && v.stop ? points(Math.abs(last - v.stop)) : undefined} />
                        <Stat label="Target" value={rupees(v.target)} sub={last && v.target ? points(Math.abs(v.target - last)) : undefined} />
                    </div>
                </section>
            )}

            <section className={styles.card} aria-label="Watching">
                <h3 className={styles.cardTitle}>Watching</h3>
                <p className={`${styles.watch}${watchLive ? ` ${styles.warn}` : ''}`}>{watch.text}</p>
            </section>

            <section className={styles.card} aria-label="Situation">
                <h3 className={styles.cardTitle}>Situation</h3>
                <div className={styles.stats}>
                    <Stat label="15m ADX" value={v.adx.toFixed(1)} sub={`range < ${v.max_adx}`} />
                    <Stat label="5m RSI" value={v.rsi5 ? v.rsi5.toFixed(0) : '—'} />
                    <Stat label="Day move" value={v.day_open ? points(v.day_trend, true) : '—'} />
                    {hasLevels && (
                        <>
                            <Stat label="Support" value={rupees(v.support)} sub={last && v.support ? points(last - v.support, true) : undefined} />
                            <Stat label="Resistance" value={rupees(v.resistance)} sub={last && v.resistance ? points(v.resistance - last, true) : undefined} />
                            <Stat label="Width" value={v.width ? points(v.width) : '—'} sub={v.class ? v.class.toLowerCase() : undefined} />
                        </>
                    )}
                </div>
                {!hasLevels && <p className={styles.note}>No range levels yet</p>}
                {!inPosition && <p className={styles.note}>Position: Flat</p>}
            </section>

            <div className={styles.chips}>
                <span className={styles.chip}>Trades {v.trades_today}/{v.max_trades}</span>
                <span className={styles.chip}>Entries on {v.entry_tf}m closes</span>
                <span className={styles.chip}>Flat by {hhmm(v.time_exit_min)}</span>
                <span className={`${styles.chip} ${v.breakout ? styles.chipOn : ''}`}>Breakout</span>
                <span className={`${styles.chip} ${v.flag ? styles.chipOn : ''}`}>Flag</span>
                <span className={`${styles.chip} ${v.mean_reversion ? styles.chipOn : ''}`}>Mean rev.</span>
            </div>
        </>
    );
}

/** Phone bottom sheet for the range strategy: half (chart stays usable) or full height. */
export function RangeSheet({ id, snap, onSnap, returnFocusRef }: RangeSheetProps) {
    const v = useRangeStore(s => s.view);
    const sheetRef = useRef<HTMLDivElement>(null);
    const drag = useRef<{ startY: number; startH: number } | null>(null);
    const [dragH, setDragH] = useState<number | null>(null);
    const open = snap !== 'closed';
    const wasOpen = useRef(open);

    // Closed sheet stays mounted for the slide; keep it out of tab order.
    // (React 18 types lack `inert`, so set the attribute directly.)
    useEffect(() => {
        sheetRef.current?.toggleAttribute('inert', !open);
    }, [open]);

    // Focus in on open, back to the strip on close.
    useEffect(() => {
        if (open && !wasOpen.current) sheetRef.current?.focus();
        if (!open && wasOpen.current) returnFocusRef.current?.focus();
        wasOpen.current = open;
    }, [open, returnFocusRef]);

    useEffect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onSnap('closed'); };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [open, onSnap]);

    const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
        if ((e.target as HTMLElement).closest('button')) return;
        const el = sheetRef.current;
        if (!el) return;
        drag.current = { startY: e.clientY, startH: el.getBoundingClientRect().height };
        e.currentTarget.setPointerCapture(e.pointerId);
    };
    const onPointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
        if (!drag.current) return;
        const h = drag.current.startH + (drag.current.startY - e.clientY);
        setDragH(Math.max(0, Math.min(h, window.innerHeight * FULL)));
    };
    const onPointerUp = () => {
        if (!drag.current) return;
        const moved = dragH !== null && Math.abs(dragH - drag.current.startH) > 8;
        drag.current = null;
        if (moved && dragH !== null) {
            const r = dragH / window.innerHeight;
            onSnap(r < HALF * 0.6 ? 'closed' : r < (HALF + FULL) / 2 ? 'half' : 'full');
        } else {
            // A tap on the handle toggles between half and full.
            onSnap(snap === 'full' ? 'half' : 'full');
        }
        setDragH(null);
    };

    const updated = v ? asOf(v.ts) : '';

    return (
        <>
            {snap === 'full' && <div className={styles.backdrop} onClick={() => onSnap('closed')} aria-hidden />}
            <div
                ref={sheetRef}
                id={id}
                className={`${styles.sheet} ${styles[`snap_${snap}`]}${dragH !== null ? ` ${styles.dragging}` : ''}`}
                style={dragH !== null ? { height: dragH } : undefined}
                role="dialog"
                aria-modal={snap === 'full'}
                aria-label="Range strategy"
                aria-hidden={!open}
                tabIndex={-1}
            >
                <div
                    className={styles.sheetHead}
                    onPointerDown={onPointerDown}
                    onPointerMove={onPointerMove}
                    onPointerUp={onPointerUp}
                    onPointerCancel={onPointerUp}
                >
                    <span className={styles.handle} aria-hidden />
                    <div className={styles.sheetTitleRow}>
                        <h2 className={styles.sheetTitle}>Range strategy</h2>
                        {v && <span className={`${styles.pill} ${styles[`regime${v.regime}`]}`}>{REGIME_LABEL[v.regime] ?? v.regime}</span>}
                        <span className={`${styles.pill} ${styles.regimeWARMING}`} title="The executor only sends real orders for NIFTY50_FNO">Paper</span>
                        {updated && <span className={styles.updated}>as of {updated} IST</span>}
                        <button type="button" className={styles.close} onClick={() => onSnap('closed')} aria-label="Close strategy details">
                            <X size={18} />
                        </button>
                    </div>
                </div>
                <div className={styles.sheetBody}>
                    <SheetBody />
                </div>
            </div>
        </>
    );
}
