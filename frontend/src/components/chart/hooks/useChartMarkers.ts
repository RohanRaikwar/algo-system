import { useEffect, useRef, type MutableRefObject } from 'react';
import type { ISeriesApi } from 'lightweight-charts';
import { useSignalStore } from '../../../store/useSignalStore';
import { useRefusedStore } from '../../../store/useRefusedStore';
import { buildLiveOrderKey, useLiveOrderStore } from '../../../store/useLiveOrderStore';
import { IST_OFFSET } from '../../../utils/helpers';
import { reasonWarnings, warnLabel } from '../../sr/srFormat';
import { useMediaQuery, PHONE_QUERY } from '../../../hooks/useMediaQuery';
import type { LiveOrderStatePayload, SignalRecord } from '../../../types/signal';
import type { RefusedEntry } from '../../../types/refused';
import { inferSide, legInfo } from '../../signals/signalAnalytics';
import { TradeMarkersPrimitive, type TradeMarker } from '../tradeMarkersPrimitive';
import { isWatchAction, watchExitPricedAtPremium } from '../../signals/watch';

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
const IST_CLOCK = new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, timeZone: 'Asia/Kolkata',
});

/** "NIFTY50_SR · 14:08:01" plus any extra parts. */
function metaLine(strategy: string, ts: string, ...extra: string[]): string {
    const d = new Date(ts);
    const clock = Number.isNaN(d.getTime()) ? '' : IST_CLOCK.format(d);
    return [strategy, clock, ...extra].filter(Boolean).join(' · ');
}

// Price levels the strategy writes into its reason as paise ints, e.g. "level=2273620".
const LEVEL_KEYS: Array<[string, string]> = [['level', 'LVL'], ['stop', 'SL'], ['target', 'TGT']];

/** "LVL 22736.20  SL 22775.40  TGT 22663.32" from the reason; empty when none. */
export function levelsLine(reason: string | undefined): string {
    if (!reason) return '';
    const parts: string[] = [];
    for (const [key, short] of LEVEL_KEYS) {
        const m = reason.match(new RegExp(`\\b${key}\\s*[=:]\\s*(\\d+)\\b`, 'i'));
        if (!m) continue;
        const paise = parseInt(m[1], 10);
        parts.push(`${short} ${Math.floor(paise / 100)}.${String(paise % 100).padStart(2, '0')}`);
    }
    return parts.join('  ');
}

function signalTs(s: SignalRecord): string {
    return s.candle_ts || s.created_at;
}

function signalStrike(s: SignalRecord): number {
    return s.strike ?? legInfo(s)?.strike ?? 0;
}

/**
 * Full signal reason, word-wrapped like the old hover card showed it: up to
 * four rows of ~40 chars (about 140 chars). The leading leg tag is dropped
 * since the strike row already shows it.
 */
export function reasonLines(reason: string | undefined, width = 40, rows = 4): string[] {
    if (!reason) return [];
    const text = reason.replace(/^\[[^\]]*\]\s*/, '');
    const wrapped: string[] = [];
    let line = '';
    for (const word of text.split(/\s+/).filter(Boolean)) {
        if (line && line.length + 1 + word.length > width) {
            wrapped.push(line);
            line = word;
        } else {
            line = line ? `${line} ${word}` : word;
        }
    }
    if (line) wrapped.push(line);
    const out = wrapped.slice(0, rows).map(l => clip(l, width));
    if (wrapped.length > rows) out[rows - 1] = `${out[rows - 1].slice(0, width - 2)} …`;
    return out;
}

function entryLines(s: SignalRecord): string[] {
    const lines: string[] = [metaLine(s.strategy, s.created_at || s.candle_ts)];
    const strike = signalStrike(s);
    const sideCh = sideShort(s.side);
    const contract = s.fno_symbol || (strike > 0 ? `${strike}${sideCh}` : '');
    const head = [s.price && s.price > 0 ? `@ ${rupees(s.price)}` : '', contract]
        .filter(Boolean).join('  ');
    if (head) lines.push(head);
    if (s.qty && s.qty > 0) lines.push(`qty ${s.qty}`);
    const levels = levelsLine(s.reason);
    if (levels) lines.push(levels);
    const warns = reasonWarnings(s.reason);
    if (warns.length > 0) lines.push(`⚠ ${warns.map(warnLabel).join(', ')}`);
    lines.push(...reasonLines(s.reason));
    return lines;
}

/**
 * Card for an exitwatch WATCH_EXIT: premium and the P&L exiting there would
 * have locked in versus the open entry. Older rows carry the index LTP as
 * price, so they show only the reason.
 */
function watchExitMarker(
    s: SignalRecord, time: number, day: string,
    entry: { price: number; qty: number; day: string } | undefined, compact: boolean,
): TradeMarker {
    const marker: TradeMarker = { time, kind: 'watch', label: label('WATCH EXIT', s.side, compact), side: sideShort(s.side), lines: [] };
    const lines: string[] = [metaLine(s.strategy, s.created_at || s.candle_ts, 'advice')];
    if (watchExitPricedAtPremium(s) && s.price && s.price > 0) {
        lines.push(`@ ${rupees(s.price)}`);
        if (entry && entry.price > 0 && entry.day === day) {
            const qty = s.qty && s.qty > 0 ? s.qty : entry.qty > 0 ? entry.qty : 1;
            marker.pnl = (s.price - entry.price) * qty;
            lines.push(`P&L ${marker.pnl > 0 ? '+' : ''}${rupees(marker.pnl)} if exited`);
        }
    }
    lines.push(...reasonLines(s.reason));
    if (!compact) marker.lines = lines;
    return marker;
}

function label(verb: string, side: string | undefined, compact: boolean, warned = false): string {
    return compact ? '' : [verb, sideLong(side), warned ? '⚠' : ''].filter(Boolean).join(' ');
}

/**
 * Chart markers for the selected instrument: entries (below the bar), exits
 * and refused entries (above), sorted by time. Each carries detail lines for
 * its callout card (strategy and time, premium, strike, qty, levels, P&L,
 * reason). An entry still open today gets a live P&L row from the live
 * order state (pub:orders) and an OPEN label. Compact mode drops label and
 * lines and keeps the icon plus C/P.
 */
export function buildChartMarkers(
    signals: SignalRecord[],
    refused: RefusedEntry[],
    selectedToken: string,
    tfSec: number,
    compact: boolean,
    liveOrders: Record<string, LiveOrderStatePayload> = {},
): TradeMarker[] {
    const out: TradeMarker[] = [];

    // Oldest first so each EXIT pairs with the BUY before it (same key, same IST day).
    // WATCH_* exit-watch advice is not a trade: only WATCH_EXIT gets a card,
    // and it reads the open entry without closing it.
    const mine = signals
        .filter(s => (!isWatchAction(s.action) || s.action === 'WATCH_EXIT') && matchesToken(s.token, s.exchange, selectedToken))
        .sort((a, b) => Date.parse(signalTs(a)) - Date.parse(signalTs(b)));
    const open = new Map<string, { price: number; qty: number; day: string; marker: TradeMarker; signal: SignalRecord }>();

    for (const s of mine) {
        const ts = signalTs(s);
        const time = bucketTime(ts, tfSec);
        if (time === null) continue;
        const isBuy = s.action.toUpperCase() === 'BUY';
        const key = `${s.strategy}|${inferSide(s)}|${legInfo(s)?.leg ?? ''}`;
        const day = IST_DAY.format(new Date(ts));

        if (s.action === 'WATCH_EXIT') {
            out.push(watchExitMarker(s, time, day, open.get(key), compact));
            continue;
        }
        const marker: TradeMarker = {
            time,
            kind: isBuy ? 'entry' : 'exit',
            label: label(isBuy ? 'BUY' : 'EXIT', s.side, compact, isBuy && reasonWarnings(s.reason).length > 0),
            side: sideShort(s.side),
            lines: [],
        };

        if (isBuy) {
            open.set(key, { price: s.price && s.price > 0 ? s.price : 0, qty: s.qty ?? 0, day, marker, signal: s });
            if (!compact) marker.lines = entryLines(s);
        } else {
            const entry = open.get(key);
            open.delete(key);
            const lines: string[] = [metaLine(s.strategy, s.created_at || s.candle_ts)];
            if (s.price && s.price > 0) {
                lines.push(`@ ${rupees(s.price)}`);
                if (entry && entry.price > 0 && entry.day === day) {
                    const qty = s.qty && s.qty > 0 ? s.qty : entry.qty > 0 ? entry.qty : 1;
                    marker.pnl = (s.price - entry.price) * qty;
                    lines.push(`P&L ${marker.pnl > 0 ? '+' : ''}${rupees(marker.pnl)}`);
                }
            }
            lines.push(...reasonLines(s.reason));
            if (!compact) marker.lines = lines;
        }
        out.push(marker);
    }

    // Positions still open today: live P&L from the option's last price.
    const today = IST_DAY.format(new Date());
    for (const o of open.values()) {
        if (o.day !== today) continue;
        const s = o.signal;
        const live = liveOrders[buildLiveOrderKey(s.strategy, inferSide(s), legInfo(s)?.leg)];
        const ltp = live?.current_fno_price ?? 0;
        const entryPrice = o.price > 0 ? o.price : live?.entry_fno_price ?? 0;
        if (!live || ltp <= 0 || entryPrice <= 0) continue;
        const qty = o.qty > 0 ? o.qty : 1;
        o.marker.pnl = (ltp - entryPrice) * qty;
        if (!compact) {
            o.marker.label = label('OPEN', s.side, compact, reasonWarnings(s.reason).length > 0);
            o.marker.lines.push(`P&L ${o.marker.pnl > 0 ? '+' : ''}${rupees(o.marker.pnl)} · LTP ${rupees(ltp)}`);
        }
    }

    for (const r of refused) {
        if (!matchesToken(r.token, r.exchange, selectedToken)) continue;
        const time = bucketTime(r.ts, tfSec);
        if (time === null) continue;
        const strike = r.strike ? `${r.strike}${sideShort(r.side)}` : '';
        const lines = compact ? [] : [
            metaLine(r.strategy, r.ts, strike),
            ...reasonLines(r.strategy_reason),
            ...reasonLines(r.reason ? `Blocked: ${r.reason}` : ''),
        ];
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
    const liveOrders = useLiveOrderStore(s => s.ordersByKey);
    // Re-evaluated on resize/rotate, so badges switch to compact live.
    const compact = useMediaQuery(PHONE_QUERY);
    const lastFingerprint = useRef<string>('');
    const primitive = useRef<TradeMarkersPrimitive | null>(null);

    useEffect(() => {
        const series = candleSeries.current;
        if (!series || !selectedToken) return;
        if (!primitive.current) {
            primitive.current = new TradeMarkersPrimitive();
            series.attachPrimitive(primitive.current);
        }

        const fp = `${selectedToken}::${selectedTF}::${compact}::`
            + signals.map(s => `${s.id}:${s.action}:${s.price ?? ''}:${s.qty ?? ''}:${s.reason?.length ?? 0}`).join('|')
            + '::' + refused.map(r => r.ts).join('|')
            + '::' + Object.entries(liveOrders).map(([k, o]) => `${k}:${o.current_fno_price ?? 0}`).join('|');
        if (fp === lastFingerprint.current) return;
        lastFingerprint.current = fp;

        primitive.current.setMarkers(buildChartMarkers(signals, refused, selectedToken, selectedTF || 60, compact, liveOrders));
    }, [signals, refused, liveOrders, selectedToken, selectedTF, candleSeries, compact]);

    useEffect(() => () => {
        if (primitive.current) candleSeries.current?.detachPrimitive(primitive.current);
        primitive.current = null;
    }, [candleSeries]);
}
