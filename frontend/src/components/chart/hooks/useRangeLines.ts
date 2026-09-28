import { useEffect, useRef, type MutableRefObject } from 'react';
import { LineStyle, type IPriceLine, type ISeriesApi } from 'lightweight-charts';
import { useRangeStore } from '../../../store/useRangeStore';
import type { RangeView } from '../../../types/range';

export interface RangeLine {
    price: number; // rupees
    color: string;
    style: LineStyle;
    title: string;
}

const C = {
    level: '#4fb8d6', // --sg-info
    flag: '#e5a23a', // --sg-warn
    entry: '#b8c0cd', // --sg-text-2
    stop: '#f0616d', // --sg-loss
    target: '#3ecf8e', // --sg-profit
};

/** Price lines for the range strategy's view (pure, for testing). */
export function rangeLines(v: RangeView | null): RangeLine[] {
    if (!v) return [];
    const out: RangeLine[] = [];
    const add = (paise: number | undefined, color: string, style: LineStyle, title: string) => {
        if (paise && paise > 0) out.push({ price: paise / 100, color, style, title });
    };
    if (v.side !== 'NONE') {
        add(v.entry, C.entry, LineStyle.Solid, `Entry ${v.side}`);
        add(v.stop, C.stop, LineStyle.Dashed, 'Stop');
        add(v.target, C.target, LineStyle.Dashed, 'Target');
        return out;
    }
    if (v.pending_side) {
        add(v.pending_edge, C.flag, LineStyle.Solid, `Armed ${v.pending_side}`);
    }
    if (v.regime === 'RANGE') {
        add(v.support, C.level, LineStyle.Dashed, 'Support');
        add(v.resistance, C.level, LineStyle.Dashed, 'Resistance');
    } else if (v.flag && v.flag_tight && v.flag_trend_day) {
        add(v.flag_box_hi, C.flag, LineStyle.Dotted, 'Flag high');
        add(v.flag_box_lo, C.flag, LineStyle.Dotted, 'Flag low');
    }
    return out;
}

/** Draws rangeLines on the candle series while the strategy's index is shown. */
export function useRangeLines(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedToken: string | null,
) {
    const view = useRangeStore(s => s.view);
    const drawn = useRef<IPriceLine[]>([]);
    const fingerprint = useRef('');

    useEffect(() => {
        const series = candleSeries.current;
        if (!series) return;
        const lines = view && selectedToken === view.key ? rangeLines(view) : [];
        const fp = lines.map(l => `${l.title}:${l.price}`).join('|');
        if (fp === fingerprint.current) return;
        fingerprint.current = fp;
        for (const pl of drawn.current) series.removePriceLine(pl);
        drawn.current = lines.map(l => series.createPriceLine({
            price: l.price, color: l.color, lineWidth: 1, lineStyle: l.style, axisLabelVisible: true, title: l.title,
        }));
    }, [candleSeries, view, selectedToken]);

    useEffect(() => () => {
        const series = candleSeries.current;
        if (series) for (const pl of drawn.current) series.removePriceLine(pl);
        drawn.current = [];
    }, [candleSeries]);
}
