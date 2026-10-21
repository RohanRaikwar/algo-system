import { useEffect, useRef, useState, type RefObject } from 'react';
import { X } from 'lucide-react';
import { useSelectedSRView } from '../../store/useSRStore';
import { FnoInstrumentsTab } from '../signals/FnoInstrumentsTab';
import { rejectLabel } from '../signals/SRStatusCard';
import { KIND_LABEL, REGIME_LABEL, asOf, clock, hhmm, nearestLevels, pnlPaise, points, rupees, sourceLabel, watching } from './srFormat';
import { DayPosition } from './DayPosition';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import { FLICK_SPEED, useSheetDrag } from '../../hooks/useSheetDrag';
import { useBackToClose } from '../../hooks/useBackToClose';
import { haptic } from '../../utils/haptic';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { REGIME_TONE, sheetHandle } from './srClasses';

const navH = '[--nav-h:calc(56px+env(safe-area-inset-bottom))] phone-land:[--nav-h:0px]';
const cardTitle = 'mt-0 mx-0 mb-[6px] text-sg-muted text-[11px] font-semibold tracking-[0.06em] uppercase';
const stats = 'grid grid-cols-[repeat(3,minmax(0,1fr))] gap-x-[10px] gap-y-[12px]';
const note = 'mt-[10px] mx-0 mb-0 text-sg-muted text-[12px]';
const sheetBase = `${navH} fixed left-0 right-0 bottom-[var(--nav-h)] z-[90] flex flex-col border-t border-t-sg-line rounded-t-panel bg-sg-surface shadow-[0_-12px_32px_rgba(0,0,0,0.4)] text-sg-text-2 text-[13px] focus:outline-none motion-reduce:[transition:none] phone-land:left-auto phone-land:top-0 phone-land:w-[min(420px,45vw)] phone-land:h-auto phone-land:rounded-tr-none phone-land:rounded-bl-panel`;
const sheetOpenT = '[transition:transform_0.2s_ease-out,height_0.2s_ease-out,visibility_0s_linear_0s]';
const sheetClosed = 'invisible [transform:translateY(calc(100%_+_var(--nav-h)))] phone-land:[transform:translateX(100%)]';
const sheetClosedT = '[transition:transform_0.2s_ease-out,height_0.2s_ease-out,visibility_0s_linear_0.2s]';

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
        <div className="flex flex-col min-w-0">
            <span className="text-sg-muted text-[11px]">{label}</span>
            <span className="text-sg-text text-[15px] font-semibold tabular-nums">{value}</span>
            {sub && <span className="text-sg-muted text-[11px] tabular-nums">{sub}</span>}
        </div>
    );
}

function SheetBody() {
    const v = useSelectedSRView();
    const last = useLastNiftyPaise(v?.key);

    if (!v) return <p className="text-sg-muted">Waiting for the strategy engine…</p>;

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
                <Card variant="sub" render={<section />} aria-label="Position">
                    <div className="flex items-center flex-wrap gap-[8px] mb-[10px]">
                        <Badge variant="side" tone={v.side === 'CALL' ? 'profit' : 'loss'}>{v.side}</Badge>
                        <span className="text-sg-text font-medium">{kindLabel}</span>
                        {v.strike ? <span className="text-sg-muted">{v.strike} {v.side === 'CALL' ? 'CE' : 'PE'}</span> : null}
                        {pnl !== null && <span className={cn('ml-auto text-[18px] font-semibold tabular-nums', pnl >= 0 ? 'text-sg-profit' : 'text-sg-loss')}>{points(pnl, true)}</span>}
                    </div>
                    <div className={stats}>
                        <Stat label="Entry" value={rupees(v.entry)} />
                        <Stat label="Stop" value={rupees(v.stop)} sub={last && v.stop ? points(Math.abs(last - v.stop)) : undefined} />
                        <Stat label="Target" value={rupees(v.target)} sub={last && v.target ? points(Math.abs(v.target - last)) : undefined} />
                    </div>
                </Card>
            )}

            <Card variant="sub" render={<section />} aria-label="Watching">
                <h3 className={cardTitle}>Watching</h3>
                <p className={cn('m-0 leading-[1.45]', watch.kind === 'pending' && 'text-sg-warn')}>{watch.text}</p>
                {!inPosition && v.last_reject && (
                    <p className={note}>Last refused{rejectAt ? ` ${rejectAt}` : ''}: {rejectLabel(v.last_reject)}</p>
                )}
            </Card>

            <Card variant="sub" render={<section />} aria-label="Situation">
                <h3 className={cardTitle}>Situation</h3>
                <div className={stats}>
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
                {!support && !resistance && <p className={note}>No S/R levels yet</p>}
                {!inPosition && <p className={note}>Position: Flat</p>}
            </Card>

            <div className="flex flex-wrap gap-[6px]">
                <Badge variant="chip">Trades {v.trades_today}/{v.max_trades}</Badge>
                <Badge variant="chip">Day {points(v.day_pnl_pts, true)}</Badge>
                {v.cooldown_left > 0 && <Badge variant="chip">Cooldown {v.cooldown_left}m</Badge>}
                <Badge variant="chip">Entries {hhmm(v.entry_from_min)}–{hhmm(v.entry_to_min)} on {v.entry_tf}m</Badge>
                <Badge variant="chip" tone={v.fade ? 'profit' : undefined}>Fade</Badge>
                <Badge variant="chip" tone={v.retest ? 'profit' : undefined}>Retest</Badge>
                <Badge variant="chip" tone={v.pullback ? 'profit' : undefined}>Pullback</Badge>
            </div>
        </>
    );
}

type SheetTab = 'sr' | 'fno';

/** Phone bottom sheet for the S/R strategy and F&O instruments: half (chart stays usable) or full height. */
export function SRSheet({ id, snap, onSnap, returnFocusRef }: SRSheetProps) {
    const v = useSelectedSRView();
    const [tab, setTab] = useState<SheetTab>('sr');
    const sheetRef = useRef<HTMLDivElement>(null);
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

    const { dragH, handlers } = useSheetDrag(sheetRef, () => window.innerHeight * FULL, ({ height, velocity, moved }) => {
        let next: SheetSnap;
        if (!moved) {
            // A tap on the handle toggles between half and full.
            next = snap === 'full' ? 'half' : 'full';
        } else if (velocity <= -FLICK_SPEED) {
            next = snap === 'full' && height > window.innerHeight * HALF * 0.6 ? 'half' : 'closed';
        } else if (velocity >= FLICK_SPEED) {
            next = 'full';
        } else {
            const r = height / window.innerHeight;
            next = r < HALF * 0.6 ? 'closed' : r < (HALF + FULL) / 2 ? 'half' : 'full';
        }
        if (next !== snap) haptic();
        onSnap(next);
    });

    // Android back closes the sheet before leaving the dashboard.
    useBackToClose(open, () => onSnap('closed'));

    const updated = v ? asOf(v.ts) : '';

    return (
        <>
            {snap === 'full' && <div className={cn(navH, 'fixed inset-x-0 top-0 bottom-[var(--nav-h)] z-[89] bg-[rgba(5,8,12,0.55)]')} onClick={() => onSnap('closed')} aria-hidden />}
            <div
                ref={sheetRef}
                id={id}
                className={cn(
                    sheetBase,
                    snap === 'full' ? 'h-[90dvh]' : 'h-[50dvh]',
                    snap === 'closed' && sheetClosed,
                    dragH !== null ? '[transition:none]' : snap === 'closed' ? sheetClosedT : sheetOpenT,
                )}
                style={dragH !== null ? { height: dragH } : undefined}
                role="dialog"
                aria-modal={snap === 'full'}
                aria-label="S/R strategy"
                aria-hidden={!open}
                tabIndex={-1}
            >
                <div
                    className="shrink-0 flex flex-col gap-[6px] pt-[6px] px-[12px] pb-[8px] border-b border-b-sg-line-soft touch-none cursor-grab"
                    {...handlers}
                >
                    <span className={sheetHandle} aria-hidden />
                    <div className="flex items-center gap-[8px] min-w-0">
                        <h2 className="m-0 text-sg-text text-[15px] font-semibold whitespace-nowrap">S/R strategy</h2>
                        {v && <Badge variant="mobile" tone={REGIME_TONE[v.regime]}>{REGIME_LABEL[v.regime] ?? v.regime}</Badge>}
                        <Badge variant="mobile" tone={REGIME_TONE.WARMING} title="The executor only sends real orders for NIFTY50_FNO">Paper</Badge>
                        {updated && <span className="ml-auto text-sg-muted text-[11px] whitespace-nowrap">as of {updated} IST</span>}
                        <Button variant="ghost-quiet" size="icon-40" className="mr-[-8px]" onClick={() => onSnap('closed')} aria-label="Close strategy details">
                            <X size={18} />
                        </Button>
                    </div>
                    <Tabs value={tab} onValueChange={(t) => setTab(t as SheetTab)}>
                        <TabsList variant="sheet" aria-label="Sheet view">
                            {(['sr', 'fno'] as const).map(t => (
                                <TabsTrigger
                                    key={t}
                                    value={t}
                                    onPointerDown={e => e.stopPropagation()}
                                    onClick={() => { haptic(); setTab(t); if (t === 'fno' && snap === 'half') onSnap('full'); }}
                                >
                                    {t === 'sr' ? 'S/R strategy' : 'F&O instruments'}
                                </TabsTrigger>
                            ))}
                        </TabsList>
                    </Tabs>
                </div>
                <div className="flex-1 min-h-0 flex flex-col gap-[10px] pt-[12px] px-[12px] pb-[16px] overflow-y-auto overscroll-contain">
                    {tab === 'sr' ? <SheetBody /> : <FnoInstrumentsTab />}
                </div>
            </div>
        </>
    );
}
