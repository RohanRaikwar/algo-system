import { useEffect, useRef, type MutableRefObject } from 'react';
import type { ISeriesApi } from 'lightweight-charts';
import { useSignalStore } from '../../../store/useSignalStore';
import { useRefusedStore } from '../../../store/useRefusedStore';
import { IST_OFFSET } from '../../../utils/helpers';
import type { SignalRecord } from '../../../types/signal';
import type { RefusedEntry } from '../../../types/refused';
import { inferSide, legInfo } from '../../signals/signalAnalytics';
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

/** ₹ string from paise; money stays integer paise until here. */
function rupees(paise: number): string {
    const sign = paise < 0 ? '−' : '';
    const abs = Math.abs(paise);
    return `${sign}₹${Math.floor(abs / 100).toLocaleString('en-IN')}.${String(abs % 100).padStart(2, '0')}`;
}

function clip(text: string, max = 30): string {
    const t = text.trim();
    return t.length > max ? `${t.slice(0, max - 1)}…` : t;
}

const IST_DAY = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Kolkata' });

function signalTs(s: SignalRecord): string {
    return s.candle_ts || s.created_at;
}

function signalStrike(s: SignalRecord): number {
    return s.strike ?? legInfo(s)?.strike ?? 0;
}

function entryLines(s: SignalRecord): string[] {
    const lines: string[] = [];
    const strike = signalStrike(s);
    const sideCh = sideShort(s.side);
    const head = [s.price && s.price > 0 ? `@ ${rupees(s.price)}` : '', strike > 0 ? `${strike}${sideCh}` : '']
        .filter(Boolean).join('  ');
    if (head) lines.push(head);
    if (s.qty && s.qty > 0) lines.push(`qty ${s.qty}`);
    return lines;
}

function label(verb: string, side: string | undefined, compact: boolean): string {
    return compact ? '' : [verb, sideLong(side)].filter(Boolean).join(' ');
}

/**
 * Chart markers for the selected instrument: entries (below the bar), exits
 * and refused entries (above), sorted by time. Each carries detail lines for
 * its callout card (premium, strike, qty, P&L, refuse reason). Compact mode
 * drops label and lines and keeps the icon plus C/P.
 */
export function buildChartMarkers(
    signals: SignalRecord[],
    refused: RefusedEntry[],
    selectedToken: string,
    tfSec: number,
    compact: boolean,
): TradeMarker[] {
    const out: TradeMarker[] = [];

    // Oldest first so each EXIT pairs with the BUY before it (same key, same IST day).
    const mine = signals
        .filter(s => matchesToken(s.token, s.exchange, selectedToken))
        .sort((a, b) => Date.parse(signalTs(a)) - Date.parse(signalTs(b)));
    const open = new Map<string, { price: number; qty: number; day: string }>();

    for (const s of mine) {
        const ts = signalTs(s);
        const time = bucketTime(ts, tfSec);
        if (time === null) continue;
        const isBuy = s.action.toUpperCase() === 'BUY';
        const key = `${s.strategy}|${inferSide(s)}|${legInfo(s)?.leg ?? ''}`;
        const day = IST_DAY.format(new Date(ts));
        const marker: TradeMarker = {
            time,
            kind: isBuy ? 'entry' : 'exit',
            label: label(isBuy ? 'BUY' : 'EXIT', s.side, compact),
            side: sideShort(s.side),
            lines: [],
        };

        if (isBuy) {
            if (s.price && s.price > 0) open.set(key, { price: s.price, qty: s.qty ?? 0, day });
            if (!compact) marker.lines = entryLines(s);
        } else {
            const entry = open.get(key);
            open.delete(key);
            const lines: string[] = [];
            if (s.price && s.price > 0) {
                lines.push(`@ ${rupees(s.price)}`);
                if (entry && entry.day === day) {
                    const qty = s.qty && s.qty > 0 ? s.qty : entry.qty > 0 ? entry.qty : 1;
                    marker.pnl = (s.price - entry.price) * qty;
                    lines.push(`P&L ${marker.pnl > 0 ? '+' : ''}${rupees(marker.pnl)}`);
                }
            }
            if (!compact) marker.lines = lines;
        }
        out.push(marker);
    }

    for (const r of refused) {
        if (!matchesToken(r.token, r.exchange, selectedToken)) continue;
        const time = bucketTime(r.ts, tfSec);
        if (time === null) continue;
        const lines = compact ? [] : [r.strategy_reason, r.reason].filter(Boolean).map(t => clip(t));
        out.push({ time, kind: 'refused', label: label('REFUSED', r.side, compact), side: sideShort(r.side), lines });
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
            + signals.map(s => `${s.id}:${s.action}:${s.price ?? ''}:${s.qty ?? ''}`).join('|')
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
