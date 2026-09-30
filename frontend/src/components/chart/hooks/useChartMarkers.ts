import { useEffect, useRef, type MutableRefObject } from 'react';
import type { ISeriesApi } from 'lightweight-charts';
import { useSignalStore } from '../../../store/useSignalStore';
import { useRefusedStore } from '../../../store/useRefusedStore';
import { IST_OFFSET } from '../../../utils/helpers';
import type { SignalRecord } from '../../../types/signal';
import type { RefusedEntry } from '../../../types/refused';
import { TradeMarkersPrimitive, type TradeMarker } from '../tradeMarkersPrimitive';

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

function sideLong(side: string | undefined): string {
    const s = (side || '').toUpperCase();
    return s === 'CALL' || s === 'PUT' ? s : '';
}

function label(verb: string, side: string | undefined, compact: boolean): string {
    return compact ? '' : [verb, sideLong(side)].filter(Boolean).join(' ');
}

/**
 * Chart markers for the selected instrument: entries (below the bar), exits
 * and refused entries (above), sorted by time. Compact mode drops the label
 * and keeps the icon plus C/P.
 */
export function buildChartMarkers(
    signals: SignalRecord[],
    refused: RefusedEntry[],
    selectedToken: string,
    tfSec: number,
    compact: boolean,
): TradeMarker[] {
    const out: TradeMarker[] = [];

    for (const s of signals) {
        if (!matchesToken(s.token, s.exchange, selectedToken)) continue;
        const time = bucketTime(s.candle_ts || s.created_at, tfSec);
        if (time === null) continue;
        const isBuy = s.action.toUpperCase() === 'BUY';
        out.push({
            time,
            kind: isBuy ? 'entry' : 'exit',
            label: label(isBuy ? 'BUY' : 'EXIT', s.side, compact),
            side: sideShort(s.side),
        });
    }

    for (const r of refused) {
        if (!matchesToken(r.token, r.exchange, selectedToken)) continue;
        const time = bucketTime(r.ts, tfSec);
        if (time === null) continue;
        out.push({ time, kind: 'refused', label: label('REFUSED', r.side, compact), side: sideShort(r.side) });
    }

    return out.sort((a, b) => a.time - b.time);
}

export function useChartMarkers(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedToken: string | null,
    selectedTF: number,
) {
    const signals = useSignalStore(s => s.signals);
    const refused = useRefusedStore(s => s.entries);
    const lastFingerprint = useRef<string>('');
    const primitive = useRef<TradeMarkersPrimitive | null>(null);

    useEffect(() => {
        const series = candleSeries.current;
        if (!series || !selectedToken) return;
        if (!primitive.current) {
            primitive.current = new TradeMarkersPrimitive();
            series.attachPrimitive(primitive.current);
        }

        const fp = `${selectedToken}::${selectedTF}::`
            + signals.map(s => `${s.id}:${s.action}`).join('|')
            + '::' + refused.map(r => r.ts).join('|');
        if (fp === lastFingerprint.current) return;
        lastFingerprint.current = fp;

        const compact = typeof window !== 'undefined' && window.matchMedia('(max-width: 640px)').matches;
        primitive.current.setMarkers(buildChartMarkers(signals, refused, selectedToken, selectedTF || 60, compact));
    }, [signals, refused, selectedToken, selectedTF, candleSeries]);

    useEffect(() => () => {
        if (primitive.current) candleSeries.current?.detachPrimitive(primitive.current);
        primitive.current = null;
    }, [candleSeries]);
}
