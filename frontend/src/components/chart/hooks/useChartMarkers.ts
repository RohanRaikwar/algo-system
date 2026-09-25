import { useEffect, useRef, type MutableRefObject } from 'react';
import type { ISeriesApi, SeriesMarker, Time } from 'lightweight-charts';
import { useSignalStore } from '../../../store/useSignalStore';
import { IST_OFFSET } from '../../../utils/helpers';

export function useChartMarkers(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedToken: string | null,
    selectedTF: number,
) {
    const signals = useSignalStore(s => s.signals);
    const lastFingerprint = useRef<string>('');

    useEffect(() => {
        if (!candleSeries.current || !selectedToken) return;

        // Filter signals that match the current token
        const matchingSig = signals.filter(s => {
            const full = s.exchange ? `${s.exchange}:${s.token}` : s.token;
            return full === selectedToken || s.token === selectedToken;
        });

        // Build fingerprint
        const fp = `${selectedTF}::` + matchingSig.map(s => `${s.id}:${s.action}`).join('|');

        if (fp === lastFingerprint.current) return;
        lastFingerprint.current = fp;

        if (matchingSig.length === 0) {
            candleSeries.current.setMarkers([]);
            return;
        }

        const tfSec = selectedTF || 60;
        const compactText = typeof window !== 'undefined' && window.matchMedia('(max-width: 640px)').matches;

        // Convert Signals to markers
        const sigMarkers: SeriesMarker<Time>[] = matchingSig
            .map(s => {
                const ts = s.candle_ts || s.created_at;
                if (!ts) return null;
                const rawSec = Math.floor(new Date(ts).getTime() / 1000) + IST_OFFSET;
                const timeSec = Math.floor(rawSec / tfSec) * tfSec;
                const isBuy = s.action.toUpperCase() === 'BUY';
                const side = s.side ? s.side.toUpperCase() : '';
                const sideShort = side === 'CALL' ? 'C' : side === 'PUT' ? 'P' : '';
                const markerLabel = compactText
                    ? (sideShort ? `${isBuy ? '▲' : '▼'}${sideShort}` : (isBuy ? '▲' : '▼'))
                    : [isBuy ? 'BUY' : 'EXIT', sideShort].filter(Boolean).join(' ');

                return {
                    time: timeSec as Time,
                    position: isBuy ? 'belowBar' : 'aboveBar',
                    color: isBuy ? '#3ecf8e' : '#f0616d',
                    shape: isBuy ? 'arrowUp' : 'arrowDown',
                    text: markerLabel || (isBuy ? 'BUY' : 'EXIT'),
                } as SeriesMarker<Time>;
            })
            .filter((m): m is SeriesMarker<Time> => m !== null);

        const allMarkers = sigMarkers.sort((a, b) => (a.time as number) - (b.time as number));

        candleSeries.current.setMarkers(allMarkers);
    }, [signals, selectedToken, selectedTF, candleSeries]);
}

