import { useEffect, useRef, type MutableRefObject } from 'react';
import type { ISeriesApi } from 'lightweight-charts';
import { useSRStore } from '../../../store/useSRStore';
import { SRBoxPrimitive, srBox } from '../srBoxPrimitive';

/** Shades SR's sideways box on the candle series while SR's index is shown and the overlay is on. */
export function useSRBox(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedToken: string | null,
    enabled: boolean,
) {
    const view = useSRStore(s => s.view);
    const primitive = useRef<SRBoxPrimitive | null>(null);

    useEffect(() => {
        const series = candleSeries.current;
        if (!series) return;
        if (!primitive.current) {
            primitive.current = new SRBoxPrimitive();
            series.attachPrimitive(primitive.current);
        }
        primitive.current.setBox(enabled && view && selectedToken === view.key ? srBox(view) : null);
    }, [candleSeries, view, selectedToken, enabled]);

    useEffect(() => () => {
        if (primitive.current) candleSeries.current?.detachPrimitive(primitive.current);
        primitive.current = null;
    }, [candleSeries]);
}
