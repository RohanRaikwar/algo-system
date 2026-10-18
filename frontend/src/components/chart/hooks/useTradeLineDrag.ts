import { useEffect, useRef, useState, type MutableRefObject } from 'react';
import type { IChartApi, ISeriesApi } from 'lightweight-charts';
import { useSRViewFor } from '../../../store/useSRStore';
import { useLiveOrderStore } from '../../../store/useLiveOrderStore';
import { postPositionCommand } from '../../../services/api';
import { levelOnRightSide, rupeesToPaise, snapToTick, type LevelKind } from '../../signals/positionCommands';
import type { LevelOverride } from './useSRLines';

/** Pixels from a line that still grab it. */
const GRAB_PX = 6;
/** How long a dropped level is held while the strategy's view catches up. */
const HOLD_MS = 8000;

/** Which line (if any) a pointer at y grabs; the nearer one wins. */
export function hitLine(y: number, stopY: number | null, targetY: number | null, tol = GRAB_PX): LevelKind | null {
    const ds = stopY === null ? Infinity : Math.abs(y - stopY);
    const dt = targetY === null ? Infinity : Math.abs(y - targetY);
    if (Math.min(ds, dt) > tol) return null;
    return ds <= dt ? 'stop' : 'target';
}

/**
 * Lets the SR stop and target lines be dragged on the chart for an open
 * paper position (NIFTY or SENSEX, whichever index is shown). On release
 * the level is sent to stratengine; until the strategy's view reports it
 * the line is held where it was dropped. Real-order positions are not
 * draggable. Returns the held levels for useSRLines and a short note.
 */
export function useTradeLineDrag(
    chartApi: MutableRefObject<IChartApi | null>,
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    chartContainer: MutableRefObject<HTMLDivElement | null>,
    selectedToken: string | null,
    enabled: boolean,
): { override: LevelOverride | null; note: string } {
    const view = useSRViewFor(selectedToken);
    const live = useLiveOrderStore(s => (view ? s.ordersByKey[`${view.strategy}|${view.side}`] : undefined));
    const [override, setOverride] = useState<LevelOverride | null>(null);
    const [note, setNote] = useState('');
    const holdTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

    const active = enabled && !!view && view.key === selectedToken && view.side !== 'NONE' && live?.real !== true;
    // Latest values for the pointer handlers, which are bound once per activation.
    const state = useRef({ view, live, override });
    state.current = { view, live, override };

    // Drop the held level once the view reports it.
    useEffect(() => {
        if (!override || !view) return;
        if ((override.stop === undefined || override.stop === view.stop) &&
            (override.target === undefined || override.target === view.target)) {
            setOverride(null);
        }
    }, [override, view]);

    useEffect(() => () => { if (holdTimer.current) clearTimeout(holdTimer.current); }, []);

    useEffect(() => {
        const el = chartContainer.current;
        if (!active || !el) return;
        let dragging: LevelKind | null = null;
        let level = 0; // paise

        const yOf = (e: PointerEvent) => e.clientY - el.getBoundingClientRect().top;
        const coord = (paise?: number) => {
            const s = candleSeries.current;
            if (!s || !paise) return null;
            const c = s.priceToCoordinate(paise / 100);
            return c === null ? null : c;
        };
        const grabbed = (e: PointerEvent) => {
            const v = state.current.view;
            const o = state.current.override;
            return hitLine(yOf(e), coord(o?.stop ?? v?.stop), coord(o?.target ?? v?.target));
        };
        // Chart pan/zoom is off while a line is held, then put back as it was.
        let saved: Pick<ReturnType<IChartApi['options']>, 'handleScroll' | 'handleScale'> | null = null;
        const setChartInput = (on: boolean) => {
            const chart = chartApi.current;
            if (!chart) return;
            if (!on) {
                const { handleScroll, handleScale } = chart.options();
                saved = { handleScroll, handleScale };
                chart.applyOptions({ handleScroll: false, handleScale: false });
            } else if (saved) {
                chart.applyOptions(saved);
                saved = null;
            }
        };

        const down = (e: PointerEvent) => {
            const kind = grabbed(e);
            if (!kind) return;
            e.preventDefault();
            e.stopPropagation();
            dragging = kind;
            level = 0;
            setNote('');
            setChartInput(false);
            el.setPointerCapture?.(e.pointerId);
        };
        const move = (e: PointerEvent) => {
            if (!dragging) {
                el.style.cursor = grabbed(e) ? 'ns-resize' : '';
                return;
            }
            const price = candleSeries.current?.coordinateToPrice(yOf(e));
            if (price === null || price === undefined) return;
            level = rupeesToPaise(snapToTick(price));
            const kind = dragging;
            setOverride(o => ({ ...o, [kind]: level }));
            e.stopPropagation();
        };
        const up = (e: PointerEvent) => {
            if (!dragging) return;
            const kind = dragging;
            dragging = null;
            setChartInput(true);
            el.releasePointerCapture?.(e.pointerId);
            e.stopPropagation();
            const { view: v, live: l } = state.current;
            const drop = () => setOverride(o => {
                if (!o) return null;
                const next = { ...o };
                delete next[kind];
                return next.stop === undefined && next.target === undefined ? null : next;
            });
            if (!v || !level) { drop(); return; }
            if (!levelOnRightSide(kind, level, v.close ?? 0, v.side, true)) {
                drop();
                setNote(kind === 'stop' ? 'stop must be on the losing side of price' : 'target must be on the winning side of price');
                return;
            }
            postPositionCommand({
                strategy: v.strategy, side: v.side, fno_token: l?.fno_token, action: 'set_levels',
                ...(kind === 'stop' ? { index_stop: level } : { index_target: level }),
            }).then(
                () => setNote(`${kind === 'stop' ? 'SL' : 'target'} ${(level / 100).toFixed(2)} sent`),
                (err: unknown) => { drop(); setNote(err instanceof Error ? err.message : 'send failed'); },
            );
            if (holdTimer.current) clearTimeout(holdTimer.current);
            holdTimer.current = setTimeout(drop, HOLD_MS);
        };

        el.addEventListener('pointerdown', down, true);
        el.addEventListener('pointermove', move, true);
        el.addEventListener('pointerup', up, true);
        el.addEventListener('pointercancel', up, true);
        return () => {
            el.removeEventListener('pointerdown', down, true);
            el.removeEventListener('pointermove', move, true);
            el.removeEventListener('pointerup', up, true);
            el.removeEventListener('pointercancel', up, true);
            el.style.cursor = '';
            if (dragging) setChartInput(true);
        };
    }, [active, chartApi, candleSeries, chartContainer]);

    return { override: active ? override : null, note: active ? note : '' };
}
