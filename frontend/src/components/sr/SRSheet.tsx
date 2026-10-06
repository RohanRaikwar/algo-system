import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type RefObject } from 'react';
import { X } from 'lucide-react';
import { useSRStore } from '../../store/useSRStore';
import { rejectLabel } from '../signals/SRStatusCard';
import { KIND_LABEL, REGIME_LABEL, asOf, clock, hhmm, nearestLevels, pnlPaise, points, rupees, sourceLabel, watching } from './srFormat';
import { DayPosition } from './DayPosition';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import styles from './SRMobile.module.css';

export type SheetSnap = 'closed' | 'half' | 'full';

interface SRSheetProps {
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
    const v = useSRStore(s => s.view);
    const last = useLastNiftyPaise(v?.key);

    if (!v) return <p className={styles.muted}>Waiting for the strategy engine…</p>;

    const price = last ?? v.close ?? null;
    const watch = watching(v, price);
    const inPosition = v.side !== 'NONE';
    const kindLabel = v.kind ? KIND_LABEL[v.kind] ?? v.kind : '';
    const pnl = pnlPaise(v, last);
    const { support, resistance } = nearestLevels(v.levels, price);
    const rejectAt = clock(v.last_reject_ts);

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
                <p className={`${styles.watch}${watch.kind === 'pending' ? ` ${styles.warn}` : ''}`}>{watch.text}</p>
                {!inPosition && v.last_reject && (
                    <p className={styles.note}>Last refused{rejectAt ? ` ${rejectAt}` : ''}: {rejectLabel(v.last_reject)}</p>
                )}
            </section>

            <section className={styles.card} aria-label="Situation">
                <h3 className={styles.cardTitle}>Situation</h3>
                <div className={styles.stats}>
                    <Stat label="15m ADX" value={v.adx.toFixed(1)} />
                    <Stat label="5m RSI" value={v.rsi5 ? v.rsi5.toFixed(0) : '—'} />
                    <Stat label="15m ATR" value={v.atr15 ? points(v.atr15) : '—'} />
                    <Stat label="VWAP" value={rupees(v.vwap)} sub={price && v.vwap ? points(price - v.vwap, true) : undefined} />
                    <Stat label="Support" value={rupees(support?.price)}
                        sub={support && price ? `${points(price - support.price, true)} · ${sourceLabel(support.source)}` : undefined} />
                    <Stat label="Resistance" value={rupees(resistance?.price)}
                        sub={resistance && price ? `${points(resistance.price - price, true)} · ${sourceLabel(resistance.source)}` : undefined} />
                </div>
                <DayPosition v={v} price={price} />
                {!support && !resistance && <p className={styles.note}>No S/R levels yet</p>}
                {!inPosition && <p className={styles.note}>Position: Flat</p>}
            </section>

            <div className={styles.chips}>
                <span className={styles.chip}>Trades {v.trades_today}/{v.max_trades}</span>
                <span className={styles.chip}>Day {points(v.day_pnl_pts, true)}</span>
                {v.cooldown_left > 0 && <span className={styles.chip}>Cooldown {v.cooldown_left}m</span>}
                <span className={styles.chip}>Entries {hhmm(v.entry_from_min)}–{hhmm(v.entry_to_min)} on {v.entry_tf}m</span>
                <span className={`${styles.chip} ${v.fade ? styles.chipOn : ''}`}>Fade</span>
                <span className={`${styles.chip} ${v.retest ? styles.chipOn : ''}`}>Retest</span>
                <span className={`${styles.chip} ${v.pullback ? styles.chipOn : ''}`}>Pullback</span>
            </div>
        </>
    );
}

/** Phone bottom sheet for the S/R strategy: half (chart stays usable) or full height. */
export function SRSheet({ id, snap, onSnap, returnFocusRef }: SRSheetProps) {
    const v = useSRStore(s => s.view);
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
                aria-label="S/R strategy"
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
                        <h2 className={styles.sheetTitle}>S/R strategy</h2>
                        {v && <span className={`${styles.pill} ${styles[`regime${v.regime}`] ?? ''}`}>{REGIME_LABEL[v.regime] ?? v.regime}</span>}
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
