import { useEffect, useRef, type MutableRefObject } from 'react';
import { LineStyle, type IPriceLine, type ISeriesApi } from 'lightweight-charts';
import { useSRStore } from '../../../store/useSRStore';
import type { SRView } from '../../../types/sr';
import { nearestN, sourceLabel } from '../../sr/srFormat';

export interface PriceLine {
    price: number; // rupees
    color: string;
    style: LineStyle;
    title: string;
}

const C = {
    support: '#3ecf8e', // --sg-profit
    resistance: '#f0616d', // --sg-loss
    day: '#8a94a6', // --sg-text-3
    prevDay: '#6b7385',
    entry: '#b8c0cd', // --sg-text-2
    target: '#3ecf8e',
    stop: '#f0616d',
};

/**
 * Price lines for NIFTY50_SR's view (pure, for testing): the 3 nearest
 * supports (S1 closest) and resistances (R1 closest) around price, today's
 * high/low/open, the previous day's high/low, and SR's open position.
 */
export function srLines(v: SRView | null, price: number | null): PriceLine[] {
    if (!v) return [];
    const out: PriceLine[] = [];
    const add = (paise: number | undefined, color: string, style: LineStyle, title: string) => {
        if (paise && paise > 0) out.push({ price: paise / 100, color, style, title });
    };
    const { supports, resistances } = nearestN(v.levels, price ?? v.close, 3);
    supports.forEach((l, i) => add(l.price, C.support, LineStyle.Dashed, `S${i + 1} ${sourceLabel(l.source)}×${l.touches}`));
    resistances.forEach((l, i) => add(l.price, C.resistance, LineStyle.Dashed, `R${i + 1} ${sourceLabel(l.source)}×${l.touches}`));
    add(v.day_high, C.day, LineStyle.SparseDotted, 'DH');
    add(v.day_low, C.day, LineStyle.SparseDotted, 'DL');
    add(v.day_open, C.day, LineStyle.SparseDotted, 'DO');
    add(v.prev_day_high, C.prevDay, LineStyle.Dotted, 'PDH');
    add(v.prev_day_low, C.prevDay, LineStyle.Dotted, 'PDL');
    if (v.side !== 'NONE') {
        add(v.entry, C.entry, LineStyle.Solid, `SR ${v.side}`);
        add(v.stop, C.stop, LineStyle.Dashed, 'SR stop');
        add(v.target, C.target, LineStyle.Dashed, 'SR target');
    }
    return out;
}

/** Draws srLines on the candle series while SR's index is shown and the overlay is on. */
export function useSRLines(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedToken: string | null,
    price: number | null,
    enabled: boolean,
) {
    const view = useSRStore(s => s.view);
    const drawn = useRef<IPriceLine[]>([]);
    const fingerprint = useRef('');

    useEffect(() => {
        const series = candleSeries.current;
        if (!series) return;
        const lines = enabled && view && selectedToken === view.key ? srLines(view, price) : [];
        const fp = lines.map(l => `${l.title}:${l.price}`).join('|');
        if (fp === fingerprint.current) return;
        fingerprint.current = fp;
        for (const pl of drawn.current) series.removePriceLine(pl);
        drawn.current = lines.map(l => series.createPriceLine({
            price: l.price, color: l.color, lineWidth: 1, lineStyle: l.style, axisLabelVisible: true, title: l.title,
        }));
    }, [candleSeries, view, selectedToken, price, enabled]);

    useEffect(() => () => {
        const series = candleSeries.current;
        if (series) for (const pl of drawn.current) series.removePriceLine(pl);
        drawn.current = [];
    }, [candleSeries]);
}
