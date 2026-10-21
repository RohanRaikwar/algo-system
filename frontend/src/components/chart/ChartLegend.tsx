import { useMemo } from 'react';
import { useCandleStore } from '../../store/useCandleStore';
import { tfLabel, entryKey, getEntryColor } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import type { OHLCData, IndCrosshairValue } from './hooks/useChartInteraction';
import type { CandleRaw } from '../../store/useCandleStore';

const legendRow = 'flex flex-wrap gap-x-[14px] gap-y-[4px] py-[2px] px-[6px] rounded-[6px] bg-[rgba(21,27,37,0.72)] [&_b]:font-semibold [&_b]:ml-[2px]';

interface ChartLegendProps {
    ohlcData: OHLCData | null;
    indValues: IndCrosshairValue[];
    activeEntries: IndicatorEntry[];
    latestCandle: CandleRaw | null;
}

const fmt = (v: number | undefined) => (v === undefined || Number.isNaN(v) ? '—' : v.toFixed(2));

/**
 * Inline OHLC + indicator values in the chart's top-left corner. Signal
 * details live on the chart's marker cards (tradeMarkersPrimitive).
 */
export function ChartLegend({
    ohlcData,
    indValues,
    activeEntries,
    latestCandle,
}: ChartLegendProps) {
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
    const toneClass = isUp ? 'text-sg-profit' : 'text-sg-loss';

    return (
        <div className="flex max-w-full flex-col items-start gap-[3px] pointer-events-none text-[0.8rem] tabular-nums text-sg-muted lte-640:text-[11px]">
            {ohlc && (
                <div className={`${legendRow} lte-640:flex-nowrap lte-640:gap-[8px] lte-640:max-w-full lte-640:overflow-hidden lte-640:whitespace-nowrap`}>
                    <span>O <b className={toneClass}>{fmt(ohlc.open)}</b></span>
                    <span>H <b className={toneClass}>{fmt(ohlc.high)}</b></span>
                    <span>L <b className={toneClass}>{fmt(ohlc.low)}</b></span>
                    <span>C <b className={toneClass}>{fmt(ohlc.close)}</b></span>
                </div>
            )}

            {indRows.length > 0 && (
                <div className={legendRow}>
                    {indRows.map((r) => (
                        <span key={r.key} className="inline-flex items-center gap-[5px] whitespace-nowrap [&_small]:text-sg-faint [&_small]:text-[0.9em]">
                            <i className="w-[10px] h-[2px] rounded-[2px]" style={{ background: r.color }} />
                            {r.name}<small>{r.tf}</small>
                            <b style={{ color: r.color }}>{r.value !== null ? r.value.toFixed(2) : '—'}</b>
                        </span>
                    ))}
                </div>
            )}

        </div>
    );
}
