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

const TFS = [
    { tf: 60, label: '1m' },
    { tf: 300, label: '5m' },
    { tf: 900, label: '15m' },
    { tf: 3600, label: '1h' },
];

function toChart(b: OptBar) {
    return { time: (b.t + IST_OFFSET) as UTCTimestamp, open: b.o / 100, high: b.h / 100, low: b.l / 100, close: b.c / 100 };
}

interface OptionChartProps {
    exchange: string;
    token: string;
    symbol: string;
}

/**
 * Candles of one option contract: the last 7 days from the gateway (cached
 * there for one day) plus live bars built from its ticks. While mounted it
 * renews the contract's live lease every 20 s; after unmount the server
 * drops the feed a minute later.
 */
export function OptionChart({ exchange, token, symbol }: OptionChartProps) {
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
        <div className="flex flex-col gap-2 min-w-0">
            <div className="flex items-center justify-between gap-2 flex-wrap">
                <div className="flex items-baseline gap-2 min-w-0">
                    <span className="font-semibold truncate">{symbol}</span>
                    <span className="tabular-nums">{rupees(ltp)}</span>
                    {ltp ? <span className="text-xs text-sg-profit">live</span> : null}
                </div>
                <div className="flex gap-1" role="group" aria-label="Timeframe">
                    {TFS.map(t => (
                        <button
                            key={t.tf}
                            type="button"
                            aria-pressed={tf === t.tf}
                            onClick={() => setTF(t.tf)}
                            className={`px-2 py-0.5 rounded-md text-xs border border-sg-line ${tf === t.tf ? 'bg-sg-line text-sg-text' : 'text-sg-muted'}`}
                        >
                            {t.label}
                        </button>
                    ))}
                </div>
            </div>
            <div ref={box} className="w-full h-[min(60vh,460px)] min-h-[240px]" />
            <div className="text-xs text-sg-muted">{status}</div>
        </div>
    );
}
