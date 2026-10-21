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
import { useTradeLineDrag } from './hooks/useTradeLineDrag';
import { SRSituationCard } from './SRSituationCard';
import { ChartLegend } from './ChartLegend';
import { StatusDot } from '../layout/StatusDot';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../../hooks/useMediaQuery';
import { haptic } from '../../utils/haptic';
import { cn } from '@/lib/utils';

const up = 'text-sg-profit';
const down = 'text-sg-loss';
const focusRing = 'focus-visible:outline-none focus-visible:shadow-ring';
// Chevron drawn into the instrument select (same SVG, quotes and spaces percent-encoded).
const symbolSelect = `h-[34px] py-0 pr-[30px] pl-[10px] bg-sg-raised [background-image:url(data:image/svg+xml,%3Csvg%20xmlns=%27http://www.w3.org/2000/svg%27%20width=%2712%27%20height=%2712%27%20viewBox=%270%200%2024%2024%27%20fill=%27none%27%20stroke=%27%238a94a6%27%20stroke-width=%272.5%27%20stroke-linecap=%27round%27%20stroke-linejoin=%27round%27%3E%3Cpath%20d=%27m6%209%206%206%206-6%27/%3E%3C/svg%3E)] bg-no-repeat [background-position:right_10px_center] border border-solid border-sg-line rounded-ctrl text-sg-text text-[0.9rem] font-semibold appearance-none cursor-pointer max-w-[220px] hover:border-sg-line-hover ${focusRing} lte-640:h-[44px] lte-640:min-w-0 lte-640:max-w-full lte-640:pt-0 lte-640:pr-[22px] lte-640:pb-[14px] lte-640:pl-0 lte-640:border-0 lte-640:bg-transparent lte-640:[background-position:right_4px_top_9px] lte-640:text-[15px]`;
// No caption (empty span): the select drops its bottom padding and centres its chevron.
const symbolStackEmpty = 'lte-640:[&:has(>span:empty)>select]:pb-0 lte-640:[&:has(>span:empty)>select]:[background-position:right_4px_center]';
const symbolCaption = 'hidden lte-640:absolute lte-640:left-0 lte-640:bottom-[4px] lte-640:pointer-events-none lte-640:block lte-640:text-sg-faint lte-640:text-[11px] lte-640:leading-[1.3] lte-640:tabular-nums lte-640:whitespace-nowrap lte-640:overflow-hidden lte-640:text-ellipsis';
const quoteCls = 'flex items-baseline gap-[10px] whitespace-nowrap tabular-nums lte-640:flex-col lte-640:items-end lte-640:gap-0 lte-640:leading-[1.2]';
const lastPrice = 'text-sg-text font-semibold tracking-[-0.02em]';
const change = 'text-[0.86rem] font-medium lte-640:text-[12px]';
const toolCount = 'py-0 px-[5px] rounded-pill bg-sg-accent text-white font-semibold text-center tabular-nums';

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
    const lineDrag = useTradeLineDrag(chartApi, candleSeries, chartContainer, selectedToken, srOverlay);
    useSRLines(candleSeries, selectedToken, lastPaise, srOverlay, lineDrag.override);
    useSRBox(candleSeries, selectedToken, srOverlay);

    const tone = !quote || quote.change === 0 ? 'text-sg-muted' : quote.change > 0 ? up : down;
    const sign = !quote ? '' : quote.change > 0 ? '+' : quote.change < 0 ? '−' : '';

    return (
        <section className="relative flex flex-col flex-[1_1_auto] min-w-0 min-h-0 bg-sg-surface overflow-hidden" aria-label={`Price chart ${tfLabel(selectedTF)}`}>
            {phoneBar ? (
                <div className="flex flex-wrap items-center gap-[6px] min-h-[calc(52px+env(safe-area-inset-top))] pt-[env(safe-area-inset-top)] pr-[8px] pb-0 pl-[14px] border-b border-b-sg-line shrink-0 select-none [-webkit-touch-callout:none]">
                    <StatusDot />
                    <div className={cn('flex flex-col min-w-0 relative shrink max-w-none', symbolStackEmpty)}>
                        <select
                            className={symbolSelect}
                            value={selectedToken || ''}
                            onChange={(e) => setSelectedToken(e.target.value)}
                            aria-label="Instrument"
                        >
                            {config.tokens.map((t) => (
                                <option key={t} value={t}>{tokenLabel(t)}</option>
                            ))}
                        </select>
                        <span className={symbolCaption} aria-hidden>{tokenCaption(selectedToken)}</span>
                    </div>
                    <span className="relative inline-flex items-center gap-[3px] shrink-0 h-[28px] py-0 pr-[8px] pl-[10px] rounded-pill bg-sg-raised border border-solid border-sg-line text-sg-text text-[12px] font-semibold tabular-nums [&_svg]:text-sg-muted active:bg-sg-hover has-[select:active]:bg-sg-hover has-[select:focus-visible]:shadow-ring">
                        {tfLabel(selectedTF)}
                        <ChevronDown size={12} aria-hidden />
                        <select
                            className="absolute inset-y-[-8px] inset-x-[-4px] w-[calc(100%+8px)] opacity-0 text-[16px] cursor-pointer"
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
                        <div className={cn(quoteCls, 'ml-auto')}>
                            <span className={cn(lastPrice, 'text-[18px]')}>{fmtPrice(quote.price)}</span>
                            <span className={cn(change, tone)}>
                                {sign}{fmtPrice(Math.abs(quote.change))} ({sign}{Math.abs(quote.pct).toFixed(2)}%)
                            </span>
                        </div>
                    )}
                    {onOpenIndicators && (
                        <button
                            className={cn('relative inline-flex items-center justify-center gap-[6px] shrink-0 w-[40px] min-w-[40px] h-[40px] p-0 border-0 rounded-ctrl bg-transparent text-sg-text-2 text-[0.82rem] font-medium whitespace-nowrap cursor-pointer hover:bg-sg-hover hover:text-sg-text active:bg-sg-hover', focusRing, quote ? 'ml-0' : 'ml-auto')}
                            onClick={onOpenIndicators}
                            aria-label={`Indicators${activeEntries.length ? `, ${activeEntries.length} active` : ''}`}
                        >
                            <SlidersHorizontal size={16} />
                            {activeEntries.length > 0 && (
                                <span className={cn(toolCount, 'absolute top-[2px] right-0 min-w-[16px] leading-[16px] text-[10px]')} aria-hidden>{activeEntries.length}</span>
                            )}
                        </button>
                    )}
                </div>
            ) : (
                <div className={cn(
                    "flex items-center border-b border-b-sg-line shrink-0 lte-900:flex-wrap lte-640:grid lte-640:grid-cols-[minmax(0,1fr)_auto] lte-640:[grid-template-areas:'sym_sym'_'tf_ind'] phone-land:flex-nowrap",
                    compact
                        ? 'min-h-[40px] gap-[10px] py-[4px] px-[10px]'
                        : 'gap-[16px] min-h-[52px] py-[8px] px-[12px] lte-900:gap-x-[12px] lte-900:gap-y-[8px] lte-640:gap-x-[8px] lte-640:gap-y-[6px] lte-640:pt-[6px] lte-640:px-[12px] lte-640:pb-[8px] phone-land:min-h-[44px] phone-land:gap-[10px] phone-land:py-[4px] phone-land:px-[12px]',
                )}>
                    <div className="flex items-center gap-[14px] min-w-0 lte-640:[grid-area:sym] lte-640:justify-between lte-640:gap-[10px]">
                        {compact ? (
                            <span className="text-[12px] font-semibold text-sg-muted">{tokenLabel(selectedToken)}</span>
                        ) : (
                        <div className={cn('flex flex-col min-w-0 lte-640:relative lte-640:max-w-[60%]', symbolStackEmpty)}>
                            <select
                                className={symbolSelect}
                                value={selectedToken || ''}
                                onChange={(e) => setSelectedToken(e.target.value)}
                                aria-label="Instrument"
                                title={selectedToken || undefined}
                            >
                                {config.tokens.map((t) => (
                                    <option key={t} value={t}>{tokenLabel(t)}</option>
                                ))}
                            </select>
                            <span className={symbolCaption} aria-hidden>{tokenCaption(selectedToken)}</span>
                        </div>
                        )}
                        {quote && (
                            <div className={quoteCls}>
                                <span className={cn(lastPrice, compact ? 'text-[14px]' : 'text-[1.35rem] lte-640:text-[18px] phone-land:text-[16px]')}>{fmtPrice(quote.price)}</span>
                                <span className={cn(change, tone)} title="Change since the session's first candle">
                                    {sign}{fmtPrice(Math.abs(quote.change))} ({sign}{Math.abs(quote.pct).toFixed(2)}%)
                                </span>
                            </div>
                        )}
                    </div>

                    <div className="inline-flex p-[3px] gap-[2px] bg-sg-bg border border-solid border-sg-line rounded-[10px] shrink-0 lte-640:[grid-area:tf] lte-640:flex lte-640:min-w-0" role="radiogroup" aria-label="Timeframe">
                        {config.tfs.map((tf) => (
                            <button
                                key={tf}
                                role="radio"
                                aria-checked={tf === selectedTF}
                                className={cn(
                                    'min-w-[38px] h-[28px] py-0 px-[10px] border-none rounded-[7px] text-[0.82rem] font-medium tabular-nums cursor-pointer transition-[background,color] duration-150 ease-[ease] lte-640:flex-1 lte-640:min-w-0 lte-640:px-[4px] lte-640:h-[34px] lte-640:text-[13px] lte-640:font-semibold phone-land:h-[34px]',
                                    tf === selectedTF ? 'bg-sg-hover text-sg-text shadow-[inset_0_0_0_1px_var(--sg-line)]' : 'bg-transparent text-sg-muted hover:text-sg-text',
                                    focusRing,
                                )}
                                onClick={() => setSelectedTF(tf)}
                            >
                                {tfLabel(tf)}
                            </button>
                        ))}
                    </div>

                    <div className="flex items-center gap-[6px] ml-auto min-w-0 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden lte-900:order-3 lte-900:w-full lte-900:ml-0 lte-640:[grid-area:ind] lte-640:order-[0] lte-640:w-auto lte-640:m-0 lte-640:overflow-visible phone-land:order-[0] phone-land:w-auto phone-land:ml-auto">
                        {onOpenIndicators && !compact && (
                            <button
                                className={cn('inline-flex items-center gap-[6px] h-[30px] py-0 px-[11px] border border-solid border-sg-line rounded-ctrl bg-sg-raised text-sg-text-2 text-[0.82rem] font-medium whitespace-nowrap cursor-pointer shrink-0 hover:bg-sg-hover hover:text-sg-text lte-640:h-[40px] lte-640:min-w-[44px] lte-640:justify-center lte-640:px-[10px] phone-land:h-[40px] phone-land:min-w-[44px] phone-land:justify-center', focusRing)}
                                onClick={onOpenIndicators}
                                title="Indicators (press / or I)"
                                aria-keyshortcuts="/ I"
                            >
                                <SlidersHorizontal size={14} />
                                <span className="lte-640:hidden phone-land:hidden">Indicators</span>
                                {activeEntries.length > 0 && (
                                    <span className={cn(toolCount, 'min-w-[18px] leading-[18px] text-[0.7rem]')} aria-label={`${activeEntries.length} active`}>{activeEntries.length}</span>
                                )}
                                <kbd className="min-w-[18px] py-0 px-[4px] border border-solid border-sg-line border-b-2 rounded-[5px] bg-sg-bg text-sg-muted [font-family:inherit] text-[0.72rem] leading-[1.4] text-center phone-land:hidden touch:hidden">/</kbd>
                            </button>
                        )}
                    </div>
                </div>
            )}

            <div className="relative flex-[1_1_auto] min-h-0">
                <div ref={chartContainer} className="absolute inset-0" />
                {lineDrag.note && <div className="absolute right-[72px] bottom-[32px] z-[6] py-[3px] px-[8px] rounded-ctrl bg-sg-raised text-sg-text-2 text-[0.75rem] pointer-events-none" role="status">{lineDrag.note}</div>}

                {!hasCandles && (
                    <div className="absolute inset-0 z-[5] flex items-center justify-center text-sg-muted text-[0.9rem] pointer-events-none">
                        Waiting for market data for {tokenLabel(selectedToken) || 'this instrument'}…
                    </div>
                )}

                <div className="absolute top-[10px] left-[12px] right-[80px] z-[10] flex flex-col items-start gap-[4px] pointer-events-none lte-640:top-[6px] lte-640:left-[8px] lte-640:right-[64px]">
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
