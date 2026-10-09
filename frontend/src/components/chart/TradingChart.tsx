import { useMemo } from 'react';
import { ChevronDown, SlidersHorizontal } from 'lucide-react';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { tfLabel, entryKey, getEntryColor, tokenLabel, tokenCaption } from '../../utils/helpers';
import { useChartInit } from './hooks/useChartInit';
import { useCandleSeries } from './hooks/useCandleSeries';
import { useIndicatorLines } from './hooks/useIndicatorLines';
import { useChartInteraction } from './hooks/useChartInteraction';
import { useChartSubscription, usePaneSubscription } from './hooks/useChartSubscription';
import { useChartLazyLoad } from './hooks/useChartLazyLoad';
import { useChartMarkers } from './hooks/useChartMarkers';
import { useSRLines } from './hooks/useSRLines';
import { useSRBox } from './hooks/useSRBox';
import { SRSituationCard } from './SRSituationCard';
import { ChartLegend } from './ChartLegend';
import { StatusDot } from '../layout/StatusDot';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../../hooks/useMediaQuery';
import { haptic } from '../../utils/haptic';
import styles from './Chart.module.css';

interface TradingChartProps {
    onOpenIndicators?: () => void;
    /**
     * Compact pane for the multi-chart layout: its own timeframe (paneTF),
     * candles only (no indicators), sharing the main chart's instrument.
     */
    compact?: boolean;
    paneTF?: number;
    onPaneTFChange?: (tf: number) => void;
    /** Phone: tapping the SR situation chip opens the SR sheet. */
    onOpenSR?: () => void;
}

const NO_INDICATORS: never[] = [];

const IST_DAY = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Kolkata' });

function fmtPrice(v: number): string {
    return v.toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

export function TradingChart({ onOpenIndicators, compact = false, paneTF, onPaneTFChange, onOpenSR }: TradingChartProps) {
    // App state (atomic selectors)
    const config = useAppStore(s => s.config);
    const selectedToken = useAppStore(s => s.selectedToken);
    const setSelectedToken = useAppStore(s => s.setSelectedToken);
    const globalTF = useAppStore(s => s.selectedTF);
    const setGlobalTF = useAppStore(s => s.setSelectedTF);
    const globalIndicators = useAppStore(s => s.activeIndicators);
    const selectedTF = compact ? (paneTF || 60) : globalTF;
    const setSelectedTF = compact ? (tf: number) => onPaneTFChange?.(tf) : setGlobalTF;
    const activeIndicators = compact ? NO_INDICATORS : globalIndicators;
    const srOverlay = useAppStore(s => s.overlays.srSituation) && !compact;
    // Phone portrait: one top bar (status, instrument, TF chip, price, indicators).
    const phoneBar = useMediaQuery(PHONE_PORTRAIT_QUERY) && !compact;

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
    const { ohlcData, indValues } = useChartInteraction(chartApi, candleSeries, indLineSeries, chartContainer);
    // The main chart owns the global subscription; a pane holds its own.
    useChartSubscription(compact ? 0 : selectedTF, compact ? null : selectedToken, activeEntries);
    usePaneSubscription(compact ? selectedToken : null, selectedTF);
    useChartLazyLoad(chartApi, selectedTF, selectedToken);
    useChartMarkers(candleSeries, selectedToken, selectedTF);
    const lastPaise = quote ? Math.round(quote.price * 100) : null;
    useSRLines(candleSeries, selectedToken, lastPaise, srOverlay);
    useSRBox(candleSeries, selectedToken, srOverlay);

    const tone = !quote || quote.change === 0 ? '' : quote.change > 0 ? styles.up : styles.down;
    const sign = !quote ? '' : quote.change > 0 ? '+' : quote.change < 0 ? '−' : '';

    return (
        <section className={`${styles.chartCard}${compact ? ` ${styles.compact}` : ''}`} aria-label={`Price chart ${tfLabel(selectedTF)}`}>
            {phoneBar ? (
                <div className={`${styles.toolbar} ${styles.phoneBar}`}>
                    <StatusDot />
                    <div className={styles.symbolStack}>
                        <select
                            className={styles.symbolSelect}
                            value={selectedToken || ''}
                            onChange={(e) => setSelectedToken(e.target.value)}
                            aria-label="Instrument"
                        >
                            {config.tokens.map((t) => (
                                <option key={t} value={t}>{tokenLabel(t)}</option>
                            ))}
                        </select>
                        <span className={styles.symbolCaption} aria-hidden>{tokenCaption(selectedToken)}</span>
                    </div>
                    <span className={styles.tfChip}>
                        {tfLabel(selectedTF)}
                        <ChevronDown size={12} aria-hidden />
                        <select
                            className={styles.tfChipSelect}
                            value={selectedTF}
                            onChange={(e) => { haptic(); setSelectedTF(Number(e.target.value)); }}
                            aria-label="Timeframe"
                        >
                            {config.tfs.map((tf) => (
                                <option key={tf} value={tf}>{tfLabel(tf)}</option>
                            ))}
                        </select>
                    </span>
                    {quote && (
                        <div className={styles.quote}>
                            <span className={styles.lastPrice}>{fmtPrice(quote.price)}</span>
                            <span className={`${styles.change} ${tone}`}>
                                {sign}{fmtPrice(Math.abs(quote.change))} ({sign}{Math.abs(quote.pct).toFixed(2)}%)
                            </span>
                        </div>
                    )}
                    {onOpenIndicators && (
                        <button
                            className={`${styles.toolBtn} ${styles.phoneIndBtn}`}
                            onClick={onOpenIndicators}
                            aria-label={`Indicators${activeEntries.length ? `, ${activeEntries.length} active` : ''}`}
                        >
                            <SlidersHorizontal size={16} />
                            {activeEntries.length > 0 && (
                                <span className={styles.toolCount} aria-hidden>{activeEntries.length}</span>
                            )}
                        </button>
                    )}
                </div>
            ) : (
                <div className={styles.toolbar}>
                    <div className={styles.symbolGroup}>
                        {compact ? (
                            <span className={styles.paneSymbol}>{tokenLabel(selectedToken)}</span>
                        ) : (
                        <div className={styles.symbolStack}>
                            <select
                                className={styles.symbolSelect}
                                value={selectedToken || ''}
                                onChange={(e) => setSelectedToken(e.target.value)}
                                aria-label="Instrument"
                                title={selectedToken || undefined}
                            >
                                {config.tokens.map((t) => (
                                    <option key={t} value={t}>{tokenLabel(t)}</option>
                                ))}
                            </select>
                            <span className={styles.symbolCaption} aria-hidden>{tokenCaption(selectedToken)}</span>
                        </div>
                        )}
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
                        {onOpenIndicators && !compact && (
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
            )}

            <div className={styles.plot}>
                <div ref={chartContainer} className={styles.chartContainer} />

                {!hasCandles && (
                    <div className={styles.emptyState}>
                        Waiting for market data for {tokenLabel(selectedToken) || 'this instrument'}…
                    </div>
                )}

                <div className={styles.overlays}>
                    <ChartLegend
                        ohlcData={ohlcData}
                        indValues={indValues}
                        activeEntries={activeEntries}
                        latestCandle={latestCandle}
                    />
                    {srOverlay && (
                        <SRSituationCard
                            selectedToken={selectedToken}
                            price={lastPaise}
                            onOpen={phoneBar ? onOpenSR : undefined}
                        />
                    )}
                </div>
            </div>
        </section>
    );
}
