import { useMemo } from 'react';
import { useSignalStore } from '../../store/useSignalStore';
import { useCandleStore } from '../../store/useCandleStore';
import { tfLabel, IST_OFFSET, entryKey, getEntryColor } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import type { OHLCData, IndCrosshairValue } from './hooks/useChartInteraction';
import type { CandleRaw } from '../../store/useCandleStore';
import type { SignalRecord } from '../../types/signal';
import styles from './Chart.module.css';

interface ChartLegendProps {
    ohlcData: OHLCData | null;
    indValues: IndCrosshairValue[];
    activeEntries: IndicatorEntry[];
    crosshairTime: number | null;
    isPinned: boolean;
    latestCandle: CandleRaw | null;
    latestCandleTimeSec: number | null;
    selectedTF: number;
    selectedToken: string | null;
}

interface SignalLegendItem {
    key: string;
    isBuy: boolean;
    strategy: string;
    side: string;
    time: string;
    price: string;
    reason: string;
    triggerPairs: Array<{ key: string; value: string }>;
}

function signalTimeSec(sig: SignalRecord): number | null {
    const raw = sig.candle_ts || sig.created_at;
    if (!raw) return null;
    const ms = new Date(raw).getTime();
    if (Number.isNaN(ms)) return null;
    return Math.floor(ms / 1000) + IST_OFFSET;
}

function formatSignalTime(ts: string): string {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toLocaleTimeString('en-IN', {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        timeZone: 'Asia/Kolkata',
        hour12: false,
    });
}

function formatChartTimeFromSec(timeSec: number): string {
    const d = new Date((timeSec - IST_OFFSET) * 1000);
    if (Number.isNaN(d.getTime())) return '';
    return d.toLocaleTimeString('en-IN', {
        hour: '2-digit',
        minute: '2-digit',
        timeZone: 'Asia/Kolkata',
        hour12: false,
    });
}

function formatSignalPrice(price?: number): string {
    if (!price || price <= 0) return '';
    return `₹${(price / 100).toFixed(2)}`;
}

function inferSignalSide(signal: SignalRecord): string {
    if (signal.side && signal.side.trim() !== '') return signal.side.toUpperCase();
    const hay = `${signal.strategy} ${signal.reason}`.toUpperCase();
    if (hay.includes('CALL')) return 'CALL';
    if (hay.includes('PUT')) return 'PUT';
    return '';
}

function trimReason(reason: string, max = 140): string {
    const r = reason.trim();
    if (r.length <= max) return r;
    return `${r.slice(0, max - 1)}…`;
}

function parseTriggerPairs(reason: string): Array<{ key: string; value: string }> {
    const out: Array<{ key: string; value: string }> = [];
    const seen = new Set<string>();
    const pushPair = (keyRaw: string, valueRaw: string) => {
        const key = keyRaw.trim().toUpperCase();
        const value = valueRaw.trim();
        if (!key || !value) return;
        const fingerprint = `${key}:${value}`;
        if (seen.has(fingerprint)) return;
        seen.add(fingerprint);
        out.push({ key, value });
    };

    // Matches key=value and key: value patterns, e.g. EMA6=2310017, RSI:59.4
    const kvRegex = /\b([A-Za-z_][A-Za-z0-9_]*)\s*[=:]\s*(-?\d+(?:\.\d+)?)/g;
    let m: RegExpExecArray | null;
    while ((m = kvRegex.exec(reason)) !== null) {
        pushPair(m[1], m[2]);
    }

    // Matches key(value) patterns, e.g. MA21(2311389), SL(10400)
    const fnRegex = /\b([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*(-?\d+(?:\.\d+)?)\s*\)/g;
    while ((m = fnRegex.exec(reason)) !== null) {
        pushPair(m[1], m[2]);
    }

    return out.slice(0, 6);
}

const fmt = (v: number | undefined) => (v === undefined || Number.isNaN(v) ? '—' : v.toFixed(2));

/**
 * Inline OHLC + indicator values in the chart's top-left corner, plus a
 * signal card that appears only when the focused candle carries signals.
 */
export function ChartLegend({
    ohlcData,
    indValues,
    activeEntries,
    crosshairTime,
    isPinned,
    latestCandle,
    latestCandleTimeSec,
    selectedTF,
    selectedToken,
}: ChartLegendProps) {
    const signals = useSignalStore(s => s.signals);
    const indicators = useCandleStore(s => s.indicators);

    // One row per indicator: hovered value while the crosshair is on a
    // candle, otherwise the latest (live, else last confirmed) value.
    const indRows = useMemo(() => {
        const hovered = new Map(indValues.map(v => [v.key, v.value]));
        return activeEntries.map(e => {
            const key = entryKey(e);
            const st = indicators[key];
            const last = st?.history.length ? st.history[st.history.length - 1].value : null;
            const value = hovered.get(key) ?? st?.liveValue ?? last ?? st?.value ?? null;
            return { key, name: e.name.replace('_', ' '), tf: tfLabel(e.tf), color: getEntryColor(e), value };
        });
    }, [activeEntries, indicators, indValues]);

    const ohlc = ohlcData ?? (latestCandle
        ? { open: latestCandle.open, high: latestCandle.high, low: latestCandle.low, close: latestCandle.close }
        : null);
    const isUp = ohlc ? ohlc.close >= ohlc.open : true;
    const toneClass = isUp ? styles.up : styles.down;

    const tfSeconds = selectedTF || 60;
    const focusTimeSec = crosshairTime ?? latestCandleTimeSec;

    const signalItems = useMemo<SignalLegendItem[]>(() => {
        if (!selectedToken || !focusTimeSec) return [];
        const targetBucket = Math.floor(focusTimeSec / tfSeconds);

        return signals
            .filter(sig => {
                const full = sig.exchange ? `${sig.exchange}:${sig.token}` : sig.token;
                if (!(full === selectedToken || sig.token === selectedToken)) return false;
                const sec = signalTimeSec(sig);
                return sec !== null && Math.floor(sec / tfSeconds) === targetBucket;
            })
            .sort((a, b) => (signalTimeSec(b) ?? 0) - (signalTimeSec(a) ?? 0))
            .slice(0, 4)
            .map((sig, idx) => ({
                key: `${sig.id}|${sig.strategy}|${sig.action}|${sig.created_at}|${idx}`,
                isBuy: sig.action.toUpperCase() === 'BUY',
                strategy: sig.strategy,
                side: inferSignalSide(sig),
                time: formatSignalTime(sig.created_at || sig.candle_ts),
                price: formatSignalPrice(sig.price),
                reason: trimReason(sig.reason || ''),
                triggerPairs: parseTriggerPairs(sig.reason || ''),
            }));
    }, [signals, selectedToken, focusTimeSec, tfSeconds]);

    return (
        <div className={styles.legend}>
            {ohlc && (
                <div className={styles.ohlcRow}>
                    <span>O <b className={toneClass}>{fmt(ohlc.open)}</b></span>
                    <span>H <b className={toneClass}>{fmt(ohlc.high)}</b></span>
                    <span>L <b className={toneClass}>{fmt(ohlc.low)}</b></span>
                    <span>C <b className={toneClass}>{fmt(ohlc.close)}</b></span>
                </div>
            )}

            {indRows.length > 0 && (
                <div className={styles.indRow}>
                    {indRows.map((r) => (
                        <span key={r.key} className={styles.indItem}>
                            <i className={styles.indLine} style={{ background: r.color }} />
                            {r.name}<small>{r.tf}</small>
                            <b style={{ color: r.color }}>{r.value !== null ? r.value.toFixed(2) : '—'}</b>
                        </span>
                    ))}
                </div>
            )}

            {focusTimeSec && signalItems.length > 0 && (
                <div className={styles.signalCard}>
                    <div className={styles.signalCardHead}>
                        <span>{isPinned ? 'Pinned candle' : 'Signals on this candle'}</span>
                        <span>{formatChartTimeFromSec(focusTimeSec)}, {tfLabel(tfSeconds)}</span>
                    </div>
                    {signalItems.map((sig) => (
                        <div key={sig.key} className={styles.signalItem}>
                            <div className={styles.signalItemTop}>
                                <span className={`${styles.signalBadge} ${sig.isBuy ? styles.signalBuy : styles.signalExit}`}>
                                    {sig.isBuy ? 'Entry' : 'Exit'}
                                </span>
                                <span className={styles.signalStrategy}>{sig.strategy}</span>
                                {sig.side && <span className={styles.signalMuted}>{sig.side}</span>}
                                {sig.price && <span className={styles.signalPrice}>{sig.price}</span>}
                                <span className={styles.signalMuted}>{sig.time}</span>
                            </div>
                            {sig.reason && <div className={styles.signalReason}>{sig.reason}</div>}
                            {sig.triggerPairs.length > 0 && (
                                <div className={styles.signalPairs}>
                                    {sig.triggerPairs.map((p) => (
                                        <span key={`${sig.key}-${p.key}-${p.value}`} className={styles.signalPair}>
                                            {p.key} {p.value}
                                        </span>
                                    ))}
                                </div>
                            )}
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}
