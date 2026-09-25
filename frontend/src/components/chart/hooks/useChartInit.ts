import { useEffect, useRef, type MutableRefObject } from 'react';
import {
    createChart, CrosshairMode, PriceScaleMode, ColorType,
    type IChartApi, type ISeriesApi, type CandlestickData, type LineData,
    type MouseEventParams, type LineWidth,
} from 'lightweight-charts';

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
                background: { type: ColorType.Solid, color: '#151b25' },
                textColor: '#8a94a6',
                fontFamily: "'Inter', sans-serif",
                fontSize: 12,
            },
            grid: {
                vertLines: { color: 'rgba(138, 148, 166, 0.07)' },
                horzLines: { color: 'rgba(138, 148, 166, 0.07)' },
            },
            crosshair: {
                mode: CrosshairMode.Normal,
                vertLine: { color: 'rgba(184, 192, 205, 0.35)', width: 1, style: 2, labelBackgroundColor: '#2b3546' },
                horzLine: { color: 'rgba(184, 192, 205, 0.35)', width: 1, style: 2, labelBackgroundColor: '#2b3546' },
            },
            rightPriceScale: {
                borderColor: '#263041',
                scaleMargins: { top: 0.12, bottom: 0.08 },
                mode: PriceScaleMode.Normal,
                autoScale: true,
            },
            timeScale: {
                borderColor: '#263041',
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
            upColor: '#3ecf8e', downColor: '#f0616d',
            borderDownColor: '#f0616d', borderUpColor: '#3ecf8e',
            wickDownColor: '#f0616d', wickUpColor: '#3ecf8e',
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
