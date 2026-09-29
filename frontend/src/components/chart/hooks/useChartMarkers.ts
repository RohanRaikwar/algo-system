import { useEffect, useRef, type MutableRefObject } from 'react';
import type { ISeriesApi, SeriesMarker, Time } from 'lightweight-charts';
import { useSignalStore } from '../../../store/useSignalStore';
import { useRefusedStore } from '../../../store/useRefusedStore';
import { IST_OFFSET } from '../../../utils/helpers';
import type { SignalRecord } from '../../../types/signal';
import type { RefusedEntry } from '../../../types/refused';

export const REFUSED_COLOR = '#8a94a6';

function matchesToken(token: string, exchange: string, selectedToken: string): boolean {
    const full = exchange ? `${exchange}:${token}` : token;
    return full === selectedToken || token === selectedToken;
}

/** Chart time (IST-shifted seconds) of an RFC3339 timestamp, aligned to the TF bucket. */
function bucketTime(ts: string, tfSec: number): number | null {
    const ms = new Date(ts).getTime();
    if (Number.isNaN(ms)) return null;
    const rawSec = Math.floor(ms / 1000) + IST_OFFSET;
    return Math.floor(rawSec / tfSec) * tfSec;
}

function sideShort(side: string | undefined): string {
    const s = (side || '').toUpperCase();
    return s === 'CALL' ? 'C' : s === 'PUT' ? 'P' : '';
}

/**
 * Chart markers for the selected instrument: entries (green, below the bar),
 * exits (red, above) and refused entries (grey circle, above), sorted by time.
 */
export function buildChartMarkers(
    signals: SignalRecord[],
    refused: RefusedEntry[],
    selectedToken: string,
    tfSec: number,
    compact: boolean,
): SeriesMarker<Time>[] {
    const out: SeriesMarker<Time>[] = [];

    for (const s of signals) {
        if (!matchesToken(s.token, s.exchange, selectedToken)) continue;
        const time = bucketTime(s.candle_ts || s.created_at, tfSec);
        if (time === null) continue;
        const isBuy = s.action.toUpperCase() === 'BUY';
        const side = sideShort(s.side);
        const text = compact
            ? `${isBuy ? '▲' : '▼'}${side}`
            : [isBuy ? 'BUY' : 'EXIT', side].filter(Boolean).join(' ');
        out.push({
            time: time as Time,
            position: isBuy ? 'belowBar' : 'aboveBar',
            color: isBuy ? '#3ecf8e' : '#f0616d',
            shape: isBuy ? 'arrowUp' : 'arrowDown',
            text,
        });
    }

    for (const r of refused) {
        if (!matchesToken(r.token, r.exchange, selectedToken)) continue;
        const time = bucketTime(r.ts, tfSec);
        if (time === null) continue;
        const side = sideShort(r.side);
        out.push({
            time: time as Time,
            position: 'aboveBar',
            color: REFUSED_COLOR,
            shape: 'circle',
            text: compact ? `✕${side}` : ['REFUSED', side].filter(Boolean).join(' '),
        });
    }

    return out.sort((a, b) => (a.time as number) - (b.time as number));
}

export function useChartMarkers(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedToken: string | null,
    selectedTF: number,
) {
    const signals = useSignalStore(s => s.signals);
    const refused = useRefusedStore(s => s.entries);
    const lastFingerprint = useRef<string>('');

    useEffect(() => {
        if (!candleSeries.current || !selectedToken) return;

        const fp = `${selectedToken}::${selectedTF}::`
            + signals.map(s => `${s.id}:${s.action}`).join('|')
            + '::' + refused.map(r => r.ts).join('|');
        if (fp === lastFingerprint.current) return;
        lastFingerprint.current = fp;

        const compact = typeof window !== 'undefined' && window.matchMedia('(max-width: 640px)').matches;
        candleSeries.current.setMarkers(buildChartMarkers(signals, refused, selectedToken, selectedTF || 60, compact));
    }, [signals, refused, selectedToken, selectedTF, candleSeries]);
}
