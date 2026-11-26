import { useEffect, useRef, useState } from 'react';
import {
    createChart, ColorType, CrosshairMode,
    type IChartApi, type ISeriesApi, type UTCTimestamp,
} from 'lightweight-charts';
import { CHART, SG } from '../../styles/palette';
import { IST_OFFSET } from '../../utils/helpers';
import { useOptionLTPStore } from '../../store/useOptionLTPStore';
import { fetchOptionCandles, watchOption, WATCH_EVERY_MS, type OptBar } from '../../services/optionChain';
import { applyTick, rupees } from './liveBars';
import { X } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { TimeframeSelect } from '../chart/TradingChart';

// Option charts offer 1m and 5m only (the gateway builds both from 1m history).
const TFS = [60, 300];

function toChart(b: OptBar) {
    return { time: (b.t + IST_OFFSET) as UTCTimestamp, open: b.o / 100, high: b.h / 100, low: b.l / 100, close: b.c / 100 };
}

interface OptionChartProps {
    exchange: string;
    token: string;
    symbol: string;
    /** Fill the parent's height (split screen) instead of a fixed height. */
    fill?: boolean;
    /** Shows a close button in the header. */
    onClose?: () => void;
}

/**
 * Candles of one option contract: the last 7 days from the gateway (cached
 * there for one day) plus live bars built from its ticks. While mounted it
 * renews the contract's live lease every 20 s; after unmount the server
 * drops the feed a minute later.
 */
export function OptionChart({ exchange, token, symbol, fill = false, onClose }: OptionChartProps) {
    const box = useRef<HTMLDivElement | null>(null);
    const chart = useRef<IChartApi | null>(null);
    const series = useRef<ISeriesApi<'Candlestick'> | null>(null);
    const bars = useRef<OptBar[]>([]);
    const [tf, setTF] = useState(60);
    const [status, setStatus] = useState('Loading 7 days…');
    const ltp = useOptionLTPStore(s => s.ltp[token]);

    // Live lease: now and every 20 s while the chart is open.
    useEffect(() => {
        const renew = () => { void watchOption(exchange, token).catch(() => undefined); };
        renew();
        const id = window.setInterval(renew, WATCH_EVERY_MS);
        return () => window.clearInterval(id);
    }, [exchange, token]);

    useEffect(() => {
        const el = box.current;
        if (!el) return;
        const c = createChart(el, {
            layout: { background: { type: ColorType.Solid, color: SG.surface }, textColor: SG.muted, fontSize: 12 },
            grid: { vertLines: { color: CHART.grid }, horzLines: { color: CHART.grid } },
            crosshair: { mode: CrosshairMode.Normal },
            rightPriceScale: { borderColor: SG.line },
            timeScale: { borderColor: SG.line, timeVisible: true, secondsVisible: false, rightOffset: 6 },
            width: Math.max(el.clientWidth, 1),
            height: Math.max(el.clientHeight, 240),
        });
        chart.current = c;
        series.current = c.addCandlestickSeries({
            upColor: SG.profit, downColor: SG.loss, borderUpColor: SG.profit, borderDownColor: SG.loss,
            wickUpColor: SG.profit, wickDownColor: SG.loss,
        });
        const ro = new ResizeObserver(() => c.applyOptions({ width: el.clientWidth, height: Math.max(el.clientHeight, 240) }));
        ro.observe(el);
        return () => { ro.disconnect(); c.remove(); chart.current = null; series.current = null; };
    }, []);

    useEffect(() => {
        let cancelled = false;
        setStatus('Loading 7 days…');
        fetchOptionCandles(exchange, token, tf).then(res => {
            if (cancelled) return;
            bars.current = res.bars ?? [];
            series.current?.setData(bars.current.map(toChart));
            chart.current?.timeScale().scrollToRealTime();
            const until = res.expires_at ? new Date(res.expires_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '';
            setStatus(`${bars.current.length} bars${until ? ` · cached until ${until}` : ''}`);
        }).catch((e: Error) => {
            if (!cancelled) setStatus(`History unavailable: ${e.message}`);
        });
        return () => { cancelled = true; };
    }, [exchange, token, tf]);

    // Live ticks (pub:tick reaches every client; this picks our token).
    useEffect(() => {
        if (!ltp || !series.current) return;
        const changed = applyTick(bars.current, Math.floor(Date.now() / 1000), ltp, tf);
        if (changed) series.current.update(toChart(changed));
    }, [ltp, tf]);

    return (
        <div className={cn('flex flex-col gap-2 min-w-0', fill && 'h-full min-h-0 p-[8px] bg-sg-surface')}>
            <div className="flex items-center justify-between gap-2 flex-wrap">
                <div className="flex items-baseline gap-2 min-w-0">
                    <span className="font-semibold truncate">{symbol}</span>
                    <span className="tabular-nums">{rupees(ltp)}</span>
                    {ltp ? <span className="text-xs text-sg-profit">live</span> : null}
                </div>
                <div className="flex items-center gap-2">
                    <TimeframeSelect value={tf} tfs={TFS} onChange={setTF} />
                    {onClose && (
                        <Button variant="ghost" size="icon-32" onClick={onClose} aria-label="Close option chart">
                            <X size={18} />
                        </Button>
                    )}
                </div>
            </div>
            <div ref={box} className={cn('w-full', fill ? 'flex-1 min-h-0' : 'h-[min(60vh,460px)] min-h-[240px]')} />
            <div className="text-xs text-sg-muted">{status}</div>
        </div>
    );
}
