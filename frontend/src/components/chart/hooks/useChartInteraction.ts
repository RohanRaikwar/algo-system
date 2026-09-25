import { useEffect, useRef, useState, type MutableRefObject } from 'react';
import type { IChartApi, ISeriesApi, CandlestickData, LineData, MouseEventParams } from 'lightweight-charts';
import { useAppStore } from '../../../store/useAppStore';
import { entryKey, getEntryColor, tfLabel } from '../../../utils/helpers';

export interface OHLCData {
    open: number;
    high: number;
    low: number;
    close: number;
}

export interface IndCrosshairValue {
    key: string;
    label: string;
    value: number;
    color: string;
}

function normalizeCrosshairTime(param: MouseEventParams): number | null {
    const t = param.time;
    if (t === undefined || t === null) return null;
    if (typeof t === 'number') return t;

    // BusinessDay form: { year, month, day }
    if (typeof t === 'object' && 'year' in t && 'month' in t && 'day' in t) {
        const y = Number(t.year);
        const m = Number(t.month);
        const d = Number(t.day);
        if (!Number.isNaN(y) && !Number.isNaN(m) && !Number.isNaN(d)) {
            return Math.floor(Date.UTC(y, m - 1, d, 0, 0, 0) / 1000);
        }
    }
    return null;
}

/**
 * Manages chart interaction: wheel zoom on price axis, double-click reset,
 * and crosshair move for OHLC/indicator tooltips.
 */
export function useChartInteraction(
    chartApi: MutableRefObject<IChartApi | null>,
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    indLineSeries: MutableRefObject<Record<string, ISeriesApi<'Line'>>>,
    chartContainer: MutableRefObject<HTMLDivElement | null>,
) {
    const [ohlcData, setOhlcData] = useState<OHLCData | null>(null);
    const [indValues, setIndValues] = useState<IndCrosshairValue[]>([]);
    const [hoverTime, setHoverTime] = useState<number | null>(null);
    const [pinnedTime, setPinnedTime] = useState<number | null>(null);
    const priceMargins = useRef({ top: 0.1, bottom: 0.1 });

    useEffect(() => {
        const chart = chartApi.current;
        const el = chartContainer.current;
        if (!chart || !el) return;

        // Crosshair move — OHLC + indicator values
        const onCrosshairMove = (param: MouseEventParams) => {
            setHoverTime(normalizeCrosshairTime(param));
            if (param.time && param.seriesData && candleSeries.current) {
                const d = param.seriesData.get(candleSeries.current) as CandlestickData | undefined;
                if (d) setOhlcData({ open: d.open, high: d.high, low: d.low, close: d.close });
            } else {
                setOhlcData(null);
            }

            const vals: IndCrosshairValue[] = [];
            for (const [compositeKey, series] of Object.entries(indLineSeries.current)) {
                let val: number | null = null;
                if (param.time && param.seriesData) {
                    const d = param.seriesData.get(series) as LineData | undefined;
                    if (d?.value !== undefined) val = d.value;
                }
                if (val !== null) {
                    const parts = compositeKey.split(':');
                    const activeState = useAppStore.getState();
                    const activeConfig = activeState.activeIndicators || [];
                    const entry = activeConfig.find((e: { name: string; tf: number }) => entryKey(e) === compositeKey);
                    const color = entry ? getEntryColor(entry) : '#6366f1';
                    vals.push({ key: compositeKey, label: `${parts[0]}(${tfLabel(parseInt(parts[1]) || 0)})`, value: val, color });
                }
            }
            setIndValues(vals);
        };
        chart.subscribeCrosshairMove(onCrosshairMove);

        // Click/tap on candle to pin selected time bucket.
        const onClick = (param: MouseEventParams) => {
            const next = normalizeCrosshairTime(param);
            if (next === null) return;
            setPinnedTime(prev => (prev === next ? null : next));
        };
        chart.subscribeClick(onClick);

        // Y-axis wheel zoom
        const handleWheel = (e: WheelEvent) => {
            const rect = el.getBoundingClientRect();
            const mouseX = e.clientX - rect.left;
            if (mouseX >= rect.width - 65) {
                e.preventDefault();
                e.stopPropagation();
                const step = 0.02;
                const dir = e.deltaY > 0 ? 1 : -1;
                priceMargins.current.top = Math.max(0.01, Math.min(0.45, priceMargins.current.top + dir * step));
                priceMargins.current.bottom = Math.max(0.01, Math.min(0.45, priceMargins.current.bottom + dir * step));
                chart.applyOptions({ rightPriceScale: { autoScale: false, scaleMargins: { ...priceMargins.current } } });
            }
        };
        el.addEventListener('wheel', handleWheel, { passive: false });

        // Double-click reset
        const handleDblClick = (e: MouseEvent) => {
            const rect = el.getBoundingClientRect();
            if (e.clientX - rect.left >= rect.width - 65) {
                priceMargins.current = { top: 0.1, bottom: 0.1 };
                chart.applyOptions({ rightPriceScale: { autoScale: true, scaleMargins: { ...priceMargins.current } } });
            }
            setPinnedTime(null);
        };
        el.addEventListener('dblclick', handleDblClick);

        return () => {
            chart.unsubscribeCrosshairMove(onCrosshairMove);
            chart.unsubscribeClick(onClick);
            el.removeEventListener('wheel', handleWheel);
            el.removeEventListener('dblclick', handleDblClick);
        };
    }, [chartApi, candleSeries, indLineSeries, chartContainer]);

    return { ohlcData, indValues, crosshairTime: pinnedTime ?? hoverTime, isPinned: pinnedTime !== null };
}
