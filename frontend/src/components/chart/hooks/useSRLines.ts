import { useEffect, useRef, type MutableRefObject } from 'react';
import { LineStyle, type IPriceLine, type ISeriesApi } from 'lightweight-charts';
import { useSRViewFor } from '../../../store/useSRStore';
import type { SRView } from '../../../types/sr';
import { nearestN, sourceLabel } from '../../sr/srFormat';
import { CHART, SG } from '../../../styles/palette';

export interface PriceLine {
    price: number; // rupees
    color: string;
    style: LineStyle;
    title: string;
}

const C = {
    support: SG.profit,
    resistance: SG.loss,
    day: SG.muted,
    prevDay: CHART.prevDay,
    entry: SG.text2,
    target: SG.profit,
    stop: SG.loss,
};

/** Stop/target held by a drag (paise) until the strategy's view catches up. */
export interface LevelOverride {
    stop?: number;
    target?: number;
}

/**
 * Price lines for NIFTY50_SR's view (pure, for testing): the 3 nearest
 * supports (S1 closest) and resistances (R1 closest) around price, today's
 * high/low/open, the previous day's high/low, and SR's open position.
 */
export function srLines(v: SRView | null, price: number | null, drag: LevelOverride | null = null): PriceLine[] {
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
        const tag = v.manual ? ' manual' : '';
        add(v.entry, C.entry, LineStyle.Solid, `SR ${v.side}`);
        add(drag?.stop ?? v.stop, C.stop, drag?.stop ? LineStyle.Solid : LineStyle.Dashed, `SR stop${tag}`);
        add(drag?.target ?? v.target, C.target, drag?.target ? LineStyle.Solid : LineStyle.Dashed, `SR target${tag}`);
    }
    return out;
}

/** Draws srLines on the candle series while SR's index is shown and the overlay is on. */
export function useSRLines(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedToken: string | null,
    price: number | null,
    enabled: boolean,
    drag: LevelOverride | null = null,
) {
    const view = useSRViewFor(selectedToken);
    const drawn = useRef<IPriceLine[]>([]);
    const fingerprint = useRef('');

    useEffect(() => {
        const series = candleSeries.current;
        if (!series) return;
        const lines = enabled && view && selectedToken === view.key ? srLines(view, price, drag) : [];
        const fp = lines.map(l => `${l.title}:${l.price}`).join('|');
        if (fp === fingerprint.current) return;
        fingerprint.current = fp;
        for (const pl of drawn.current) series.removePriceLine(pl);
        drawn.current = lines.map(l => series.createPriceLine({
            price: l.price, color: l.color, lineWidth: 1, lineStyle: l.style, axisLabelVisible: true, title: l.title,
        }));
    }, [candleSeries, view, selectedToken, price, enabled, drag]);

    useEffect(() => () => {
        const series = candleSeries.current;
        if (series) for (const pl of drawn.current) series.removePriceLine(pl);
        drawn.current = [];
    }, [candleSeries]);
}
