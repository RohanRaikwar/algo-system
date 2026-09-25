import { useMemo } from 'react';
import { SlidersHorizontal } from 'lucide-react';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { tfLabel, entryKey, getEntryColor, IST_OFFSET } from '../../utils/helpers';
import { useChartInit } from './hooks/useChartInit';
import { useCandleSeries } from './hooks/useCandleSeries';
import { useIndicatorLines } from './hooks/useIndicatorLines';
import { useChartInteraction } from './hooks/useChartInteraction';
import { useChartSubscription } from './hooks/useChartSubscription';
import { useChartLazyLoad } from './hooks/useChartLazyLoad';
import { useChartMarkers } from './hooks/useChartMarkers';
import { ChartLegend } from './ChartLegend';
import styles from './Chart.module.css';

interface TradingChartProps {
    onOpenIndicators?: () => void;
}

const IST_DAY = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Kolkata' });

function fmtPrice(v: number): string {
    return v.toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

export function TradingChart({ onOpenIndicators }: TradingChartProps) {
    // App state (atomic selectors)
    const config = useAppStore(s => s.config);
    const selectedToken = useAppStore(s => s.selectedToken);
    const setSelectedToken = useAppStore(s => s.setSelectedToken);
    const selectedTF = useAppStore(s => s.selectedTF);
    const setSelectedTF = useAppStore(s => s.setSelectedTF);
    const activeIndicators = useAppStore(s => s.activeIndicators);

    // Show only indicators whose TF is <= chart TF (e.g. allow 3m on 5m, block >5m)
    const activeEntries = useMemo(
        () => {
            const chartTF = selectedTF || 60;
            return (activeIndicators || []).filter((e) => e.tf <= chartTF);
        },
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [selectedTF, JSON.stringify((activeIndicators || []).map(e => `${entryKey(e)}:${getEntryColor(e)}`))]
    );

    const candles = useCandleStore(s => s.candles);
    const tfCandles = useMemo(() => {
        const raw = candles[selectedTF || 60] || [];
        if (!selectedToken) return raw;
        return raw.filter(c => (c.exchange + ':' + c.token) === selectedToken || c.token === selectedToken);
    }, [candles, selectedTF, selectedToken]);
    const hasCandles = tfCandles.length > 0;

    // Latest candle by timestamp; array order differs between REST and WS paths.
    const latestCandle = useMemo(() => {
        let best: (typeof tfCandles)[number] | null = null;
        let bestMs = -Infinity;
        for (const c of tfCandles) {
            const ms = new Date(c.ts).getTime();
            if (ms > bestMs) { bestMs = ms; best = c; }
        }
        return best;
    }, [tfCandles]);

    // Last price and change since the first candle of the latest session day.
    const quote = useMemo(() => {
        if (!latestCandle) return null;
        const day = IST_DAY.format(new Date(latestCandle.ts));
        let open = latestCandle.open;
        let firstMs = new Date(latestCandle.ts).getTime();
        for (const c of tfCandles) {
            const ms = new Date(c.ts).getTime();
            if (ms < firstMs && IST_DAY.format(new Date(c.ts)) === day) {
                firstMs = ms;
                open = c.open;
            }
        }
        const change = latestCandle.close - open;
        const pct = open > 0 ? (change / open) * 100 : 0;
        return { price: latestCandle.close, change, pct };
    }, [latestCandle, tfCandles]);

    // Chart hooks
    const { chartApi, candleSeries, indLineSeries, chartContainer } = useChartInit();
    useCandleSeries(candleSeries, selectedTF, selectedToken);
    useIndicatorLines(chartApi, indLineSeries, activeEntries, selectedTF, selectedToken);
    const { ohlcData, indValues, crosshairTime, isPinned } = useChartInteraction(chartApi, candleSeries, indLineSeries, chartContainer);
    useChartSubscription(selectedTF, selectedToken, activeEntries);
    useChartLazyLoad(chartApi, selectedTF, selectedToken);
    useChartMarkers(candleSeries, selectedToken, selectedTF);

    const tone = !quote || quote.change === 0 ? '' : quote.change > 0 ? styles.up : styles.down;
    const sign = !quote ? '' : quote.change > 0 ? '+' : quote.change < 0 ? '−' : '';

    return (
        <section className={styles.chartCard} aria-label="Price chart">
            <div className={styles.toolbar}>
                <div className={styles.symbolGroup}>
                    <select
                        className={styles.symbolSelect}
                        value={selectedToken || ''}
                        onChange={(e) => setSelectedToken(e.target.value)}
                        aria-label="Instrument"
                    >
                        {config.tokens.map((t) => (
                            <option key={t} value={t}>{t}</option>
                        ))}
                    </select>
                    {quote && (
                        <div className={styles.quote}>
                            <span className={styles.lastPrice}>{fmtPrice(quote.price)}</span>
                            <span className={`${styles.change} ${tone}`} title="Change since the session's first candle">
                                {sign}{fmtPrice(Math.abs(quote.change))} ({sign}{Math.abs(quote.pct).toFixed(2)}%)
                            </span>
                        </div>
                    )}
                </div>

                <div className={styles.tfGroup} role="radiogroup" aria-label="Timeframe">
                    {config.tfs.map((tf) => (
                        <button
                            key={tf}
                            role="radio"
                            aria-checked={tf === selectedTF}
                            className={`${styles.tfBtn}${tf === selectedTF ? ` ${styles.tfActive}` : ''}`}
                            onClick={() => setSelectedTF(tf)}
                        >
                            {tfLabel(tf)}
                        </button>
                    ))}
                </div>

                <div className={styles.indGroup}>
                    {onOpenIndicators && (
                        <button
                            className={styles.toolBtn}
                            onClick={onOpenIndicators}
                            title="Indicators (press / or I)"
                            aria-keyshortcuts="/ I"
                        >
                            <SlidersHorizontal size={14} />
                            <span className={styles.toolBtnLabel}>Indicators</span>
                            {activeEntries.length > 0 && (
                                <span className={styles.toolCount} aria-label={`${activeEntries.length} active`}>{activeEntries.length}</span>
                            )}
                            <kbd className={styles.kbd}>/</kbd>
                        </button>
                    )}
                </div>
            </div>

            <div className={styles.plot}>
                <div ref={chartContainer} className={styles.chartContainer} />

                {!hasCandles && (
                    <div className={styles.emptyState}>
                        Waiting for market data for {selectedToken || 'this instrument'}…
                    </div>
                )}

                <ChartLegend
                    ohlcData={ohlcData}
                    indValues={indValues}
                    activeEntries={activeEntries}
                    crosshairTime={crosshairTime}
                    isPinned={isPinned}
                    latestCandle={latestCandle}
                    latestCandleTimeSec={latestCandle ? Math.floor(new Date(latestCandle.ts).getTime() / 1000) + IST_OFFSET : null}
                    selectedTF={selectedTF}
                    selectedToken={selectedToken}
                />
            </div>
        </section>
    );
}
