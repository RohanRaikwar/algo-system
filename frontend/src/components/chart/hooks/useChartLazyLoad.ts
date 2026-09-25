import { useEffect, useRef, useCallback, type MutableRefObject } from 'react';
import type { IChartApi } from 'lightweight-charts';
import { fetchCandles } from '../../../services/api';
import { useCandleStore } from '../../../store/useCandleStore';
import type { CandleRaw } from '../../../store/useCandleStore';

const FETCH_LIMIT = 150;
const EDGE_THRESHOLD = 10; // trigger when within 10 bars of left edge
const DEBOUNCE_MS = 400;

/**
 * Detects when the user scrolls near the left edge of the chart and
 * fetches older candles from /api/candles, prepending them into the store.
 */
export function useChartLazyLoad(
    chartApi: MutableRefObject<IChartApi | null>,
    selectedTF: number,
    selectedToken: string | null,
) {
    const loadingRef = useRef(false);
    const noMoreDataRef = useRef(false);
    const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    // Reset state when TF or token changes
    useEffect(() => {
        noMoreDataRef.current = false;
        loadingRef.current = false;
    }, [selectedTF, selectedToken]);

    const loadMore = useCallback(async () => {
        if (loadingRef.current || noMoreDataRef.current) return;
        const tf = selectedTF || 60;
        const token = selectedToken;
        if (!token) return;

        // Get the oldest candle's timestamp from the store
        const candles = useCandleStore.getState().candles[tf];
        if (!candles || candles.length === 0) return;

        const oldestTS = candles[0].ts; // earliest candle
        if (!oldestTS) return;

        loadingRef.current = true;
        try {
            const olderCandles = await fetchCandles(tf, token, FETCH_LIMIT, oldestTS);

            if (olderCandles.length === 0) {
                noMoreDataRef.current = true;
                console.log('[lazy-load] no more historical data');
                return;
            }

            // Convert CandleOut → CandleRaw with paise→rupees conversion
            // (backend sends paise, same conversion as setSnapshot's toRupees)
            const toRupees = (v: number) => v / 100;
            const rawCandles: CandleRaw[] = olderCandles.map(c => ({
                ts: c.ts,
                open: toRupees(c.open),
                high: toRupees(c.high),
                low: toRupees(c.low),
                close: toRupees(c.close),
                volume: c.volume,
                count: c.count,
                forming: false,
                exchange: c.exchange,
                token: c.token,
            }));

            useCandleStore.getState().mergeCandles(tf, rawCandles);
            console.log(`[lazy-load] prepended ${rawCandles.length} candles for tf=${tf}`);

            // If we got fewer than requested, we've hit the end
            if (olderCandles.length < FETCH_LIMIT) {
                noMoreDataRef.current = true;
            }
        } catch (err) {
            console.warn('[lazy-load] fetch error:', err);
        } finally {
            loadingRef.current = false;
        }
    }, [selectedTF, selectedToken]);

    useEffect(() => {
        const chart = chartApi.current;
        if (!chart) return;

        const timeScale = chart.timeScale();

        const handler = () => {
            const range = timeScale.getVisibleLogicalRange();
            if (!range) return;

            // range.from is the leftmost visible bar index (0-based from oldest)
            // When the user scrolls near the left edge, range.from approaches 0
            if (range.from <= EDGE_THRESHOLD) {
                if (debounceRef.current) clearTimeout(debounceRef.current);
                debounceRef.current = setTimeout(loadMore, DEBOUNCE_MS);
            }
        };

        timeScale.subscribeVisibleLogicalRangeChange(handler);

        return () => {
            timeScale.unsubscribeVisibleLogicalRangeChange(handler);
            if (debounceRef.current) clearTimeout(debounceRef.current);
        };
    }, [chartApi, loadMore]);
}
