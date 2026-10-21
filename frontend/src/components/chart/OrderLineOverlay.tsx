import { useEffect, useRef, useState, type MutableRefObject, type ReactNode } from 'react';
import type { IChartApi, ISeriesApi } from 'lightweight-charts';
import { X, GripHorizontal } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import { postPositionCommand, type PositionCommand } from '../../services/api';
import { useSRViewFor } from '../../store/useSRStore';
import { useLiveOrderStore } from '../../store/useLiveOrderStore';
import type { LevelOverride } from './hooks/useSRLines';
import {
    pointsFrom, ptsLabel, removeCommand, trailCommand, type IndexLevelKind, type LevelKind, type PositionRef,
} from '../signals/positionCommands';

type Row = 'target' | 'entry' | 'stop';
const ROWS: Row[] = ['target', 'entry', 'stop'];
/** Height of a chip row; the row is centred on its line. */
const ROW_PX = 24;

const chip = 'flex items-center h-[24px] rounded-ctrl border border-solid bg-sg-surface/90 text-[0.72rem] font-semibold tabular-nums whitespace-nowrap shadow-sm';
const chipTag = 'h-full flex items-center px-[6px] text-white';
const chipText = 'px-[6px]';

/**
 * Upstox-style chips on the SR position lines of the index chart: target
 * and stop distance from entry in index points with ✕ to remove, an entry
 * card with live points and Exit, "+ Trail" under the stop, and a drag
 * handle next to the price scale. Chips follow their lines every frame by
 * transform, without a React render. Real-order positions are read-only.
 */
export function OrderLineOverlay({ chartApi, candleSeries, selectedToken, enabled, price, override, beginDrag }: {
    chartApi: MutableRefObject<IChartApi | null>;
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>;
    selectedToken: string | null;
    enabled: boolean;
    /** Live index price, paise. */
    price: number | null;
    override: LevelOverride | null;
    beginDrag: (kind: LevelKind, e: PointerEvent) => void;
}) {
    const view = useSRViewFor(selectedToken);
    const live = useLiveOrderStore(s => (view ? s.ordersByKey[`${view.strategy}|${view.side}`] : undefined));
    const rows = useRef<Partial<Record<Row, HTMLDivElement | null>>>({});
    const [note, setNote] = useState('');
    const [confirmExit, setConfirmExit] = useState(false);
    const [trailInput, setTrailInput] = useState<string | null>(null);

    const shown = enabled && !!view && view.key === selectedToken && view.side !== 'NONE' && !!view.entry;
    const levels: Record<Row, number | undefined> = {
        target: override?.target ?? view?.target,
        entry: view?.entry,
        stop: override?.stop ?? view?.stop,
    };
    const levelsRef = useRef(levels);
    levelsRef.current = levels;

    useEffect(() => { setConfirmExit(false); setTrailInput(null); setNote(''); }, [view?.side, view?.entry]);

    // Place each row on its line every frame: the price scale moves on pan,
    // zoom and autoscale without an event for all of them.
    useEffect(() => {
        if (!shown) return;
        let frame = 0;
        const place = () => {
            const series = candleSeries.current;
            const chart = chartApi.current;
            const height = chart ? chart.paneSize().height : 0;
            const scaleW = chart ? chart.priceScale('right').width() : 0;
            for (const r of ROWS) {
                const el = rows.current[r];
                if (!el) continue;
                const p = levelsRef.current[r];
                const y = series && p ? series.priceToCoordinate(p / 100) : null;
                if (y === null || y < 0 || y > height) {
                    el.style.visibility = 'hidden';
                    continue;
                }
                el.style.visibility = 'visible';
                el.style.right = `${scaleW + 4}px`;
                el.style.transform = `translateY(${Math.round(y - ROW_PX / 2)}px)`;
            }
            frame = requestAnimationFrame(place);
        };
        frame = requestAnimationFrame(place);
        return () => cancelAnimationFrame(frame);
    }, [shown, chartApi, candleSeries]);

    if (!shown || !view) return null;

    const readOnly = live?.real === true;
    const entry = view.entry!;
    const pos: PositionRef = { strategy: view.strategy, side: view.side, fno_token: live?.fno_token };
    const send = (cmd: PositionCommand | null, done: string) => {
        if (!cmd) return;
        setNote('');
        postPositionCommand(cmd).then(
            () => setNote(done),
            (err: unknown) => setNote(err instanceof Error ? err.message : 'send failed'),
        );
    };
    const remove = (kind: IndexLevelKind) => send(removeCommand(pos, kind), `${kind === 'stop' ? 'SL' : kind === 'target' ? 'target' : 'trail'} removed`);
    const saveTrail = () => {
        const pts = Number(trailInput);
        setTrailInput(null);
        if (!(pts > 0)) return;
        send(trailCommand(pos, pts), `trail ${pts} pts sent`);
    };
    const openTrail = () => {
        const stop = levels.stop;
        const ref = price ?? view.close ?? 0;
        setTrailInput(stop && ref ? String(Number((Math.abs(ref - stop) / 100).toFixed(2))) : '');
    };

    const close = (kind: IndexLevelKind, label: string) => readOnly ? null : (
        <Button type="button" variant="ghost" size="icon-xs" aria-label={`Remove ${label}`} className="size-[22px] rounded-none" onClick={() => remove(kind)}>
            <X className="size-[12px]" />
        </Button>
    );
    const handle = (kind: LevelKind) => readOnly ? null : (
        <span
            role="slider"
            aria-label={`Drag ${kind === 'stop' ? 'SL' : 'target'}`}
            aria-valuenow={(levels[kind] ?? 0) / 100}
            className="flex items-center justify-center size-[22px] ml-[4px] rounded-pill border border-solid border-current bg-sg-surface cursor-ns-resize touch-none"
            onPointerDown={e => beginDrag(kind, e.nativeEvent)}
        >
            <GripHorizontal className="size-[12px]" />
        </span>
    );
    const row = (r: Row, color: string, body: ReactNode) => (
        <div
            key={r}
            ref={el => { rows.current[r] = el; }}
            className={cn('absolute top-0 left-[28%] flex items-center justify-between gap-[4px] pointer-events-none lte-640:left-[8px]', color)}
            style={{ visibility: 'hidden' }}
        >
            {body}
        </div>
    );

    const livePts = price ? pointsFrom(entry, price, view.side) : null;
    const symbol = live?.fno_symbol || (view.strike ? `${view.strike} ${view.side === 'PUT' ? 'PE' : 'CE'}` : view.side);

    return (
        <div className="absolute inset-0 z-[7] overflow-hidden pointer-events-none" aria-label="Order lines">
            {levels.target ? row('target', 'text-sg-profit', <>
                <div className={cn(chip, 'border-sg-profit pointer-events-auto')}>
                    <span className={cn(chipTag, 'bg-sg-profit')}>TG</span>
                    <span className={chipText}>{ptsLabel(pointsFrom(entry, levels.target, view.side))}</span>
                    {close('target', 'target')}
                </div>
                <span className="pointer-events-auto">{handle('target')}</span>
            </>) : null}

            {row('entry', 'text-sg-accent', <>
                <div className={cn(chip, 'border-sg-accent pointer-events-auto')}>
                    <span className={cn(chipTag, 'bg-sg-accent')}>{view.side}</span>
                    <span className={cn(chipText, 'text-sg-text')}>{symbol}</span>
                    {livePts !== null && <span className={cn(chipText, livePts >= 0 ? 'text-sg-profit' : 'text-sg-loss')}>{ptsLabel(livePts)}</span>}
                    {view.manual && <span className={cn(chipText, 'text-sg-faint font-normal')}>manual</span>}
                    {readOnly ? (
                        <span className={cn(chipText, 'text-sg-faint font-normal')} title="Real orders are not edited from the dashboard">real · read-only</span>
                    ) : confirmExit ? (
                        <>
                            <Button type="button" variant="destructive" size="xs" className="h-[20px] mr-[2px]" onClick={() => { setConfirmExit(false); send({ ...pos, action: 'exit' }, 'exit sent'); }}>Confirm exit</Button>
                            <Button type="button" variant="bordered" size="xs" tone="muted" className="h-[20px] mr-[2px]" onClick={() => setConfirmExit(false)}>No</Button>
                        </>
                    ) : (
                        <Button type="button" variant="destructive" size="xs" className="h-[20px] mr-[2px]" onClick={() => setConfirmExit(true)}>Exit</Button>
                    )}
                </div>
                {note && <span className="py-[2px] px-[6px] rounded-ctrl bg-sg-raised text-sg-text-2 text-[0.7rem] font-normal">{note}</span>}
            </>)}

            {levels.stop ? row('stop', 'text-sg-loss', <>
                <div className="relative">
                    <div className={cn(chip, 'border-sg-loss pointer-events-auto')}>
                        <span className={cn(chipTag, 'bg-sg-loss')}>SL</span>
                        <span className={chipText}>{ptsLabel(pointsFrom(entry, levels.stop, view.side))}</span>
                        {close('stop', 'SL')}
                    </div>
                    {!readOnly && (
                        <div className="absolute top-[28px] left-0 flex items-center gap-[4px] pointer-events-auto">
                            {trailInput !== null ? (
                                <>
                                    <Input
                                        variant="xs" type="number" step="0.05" min="0" autoFocus aria-label="Trail points"
                                        value={trailInput} onChange={e => setTrailInput(e.target.value)}
                                        onKeyDown={e => { if (e.key === 'Enter') saveTrail(); if (e.key === 'Escape') setTrailInput(null); }}
                                    />
                                    <Button type="button" variant="secondary" size="xs" onClick={saveTrail}>Trail</Button>
                                    <Button type="button" variant="bordered" size="xs" tone="muted" onClick={() => setTrailInput(null)}>Cancel</Button>
                                </>
                            ) : view.trail ? (
                                <div className={cn(chip, 'border-sg-loss border-dashed')}>
                                    <span className={chipText}>Trail {Number((view.trail / 100).toFixed(2))} pts</span>
                                    {close('trail', 'trail')}
                                </div>
                            ) : (
                                <Button type="button" variant="bordered" size="xs" className="border-dashed border-sg-loss text-sg-loss bg-sg-surface/90" onClick={openTrail}>+ Trail</Button>
                            )}
                        </div>
                    )}
                </div>
                <span className="pointer-events-auto">{handle('stop')}</span>
            </>) : null}
        </div>
    );
}
