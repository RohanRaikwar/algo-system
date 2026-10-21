import { useEffect, useRef, type MutableRefObject } from 'react';
import {
    createChart, CrosshairMode, PriceScaleMode, ColorType,
    type IChartApi, type ISeriesApi, type CandlestickData, type LineData,
    type MouseEventParams, type LineWidth,
} from 'lightweight-charts';
import { CHART, SG } from '../../../styles/palette';

export interface ChartRefs {
    chartApi: MutableRefObject<IChartApi | null>;
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>;
    indLineSeries: MutableRefObject<Record<string, ISeriesApi<'Line'>>>;
    chartContainer: MutableRefObject<HTMLDivElement | null>;
}

/**
 * Creates and manages the lightweight-charts instance.
 * Handles chart creation, theme, resize observer, and cleanup.
 */
export function useChartInit(): ChartRefs {
    const chartContainer = useRef<HTMLDivElement | null>(null);
    const chartApi = useRef<IChartApi | null>(null);
    const candleSeries = useRef<ISeriesApi<'Candlestick'> | null>(null);
    const indLineSeries = useRef<Record<string, ISeriesApi<'Line'>>>({});

    useEffect(() => {
        if (!chartContainer.current) return;
        const el = chartContainer.current;
        const initialWidth = Math.max(el.clientWidth, 1);
        const initialHeight = Math.max(el.clientHeight, 180);

        const chart = createChart(el, {
            layout: {
                background: { type: ColorType.Solid, color: SG.surface },
                textColor: SG.muted,
                fontFamily: "'Inter', sans-serif",
                fontSize: 12,
            },
            grid: {
                vertLines: { color: CHART.grid },
                horzLines: { color: CHART.grid },
            },
            crosshair: {
                mode: CrosshairMode.Normal,
                vertLine: { color: CHART.crosshair, width: 1, style: 2, labelBackgroundColor: CHART.crosshairLabel },
                horzLine: { color: CHART.crosshair, width: 1, style: 2, labelBackgroundColor: CHART.crosshairLabel },
            },
            rightPriceScale: {
                borderColor: SG.line,
                scaleMargins: { top: 0.12, bottom: 0.08 },
                mode: PriceScaleMode.Normal,
                autoScale: true,
            },
            timeScale: {
                borderColor: SG.line,
                timeVisible: true,
                secondsVisible: false,
                rightOffset: 8,
                barSpacing: 8,
            },
            handleScroll: { mouseWheel: true, pressedMouseMove: true, horzTouchDrag: true, vertTouchDrag: true },
            handleScale: {
                mouseWheel: true, pinch: true,
                axisPressedMouseMove: { price: true, time: true },
                axisDoubleClickReset: { price: true, time: true },
            },
            width: initialWidth,
            height: initialHeight,
        });

        chartApi.current = chart;
        candleSeries.current = chart.addCandlestickSeries({
            upColor: SG.profit, downColor: SG.loss,
            borderDownColor: SG.loss, borderUpColor: SG.profit,
            wickDownColor: SG.loss, wickUpColor: SG.profit,
        });

        // Resize observer
        const ro = new ResizeObserver(() => {
            chart.applyOptions({
                width: Math.max(el.clientWidth, 1),
                height: Math.max(el.clientHeight, 180),
            });
        });
        ro.observe(el);

        return () => {
            ro.disconnect();
            chart.remove();
            chartApi.current = null;
            candleSeries.current = null;
            indLineSeries.current = {};
        };
    }, []);

    return { chartApi, candleSeries, indLineSeries, chartContainer };
}
