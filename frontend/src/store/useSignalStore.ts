import { create } from 'zustand';
import type { SignalPayload, SignalRecord } from '../types/signal';
import { legInfo } from '../components/signals/signalAnalytics';

const MAX_SIGNALS = 500; // matches the REST history preload

interface SignalState {
    /** Signals from REST (historical) and WS (live) combined */
    signals: SignalRecord[];

    /** Unread count for the nav badge */
    unreadCount: number;

    /** Toggle for audio alert on new signal */
    audioEnabled: boolean;

    /** Add a live signal from WS */
    addLiveSignal: (sig: SignalPayload) => void;

    /** Bulk-set signals from REST */
    setSignals: (sigs: SignalRecord[]) => void;

    /** Merge REST history into the store, keeping live signals it lacks */
    mergeSignals: (history: SignalRecord[]) => void;

    /** Clear unread counter (on page visit) */
    clearUnread: () => void;

    /** Toggle audio alerts */
    toggleAudio: () => void;
}

/** Convert live WS payload to a SignalRecord shape */
function liveToRecord(sig: SignalPayload): SignalRecord {
    return {
        id: Date.now(),
        strategy: sig.strategy_name,
        action: sig.action,
        side: sig.side,
        market_state: sig.market_state,
        token: sig.token,
        exchange: sig.exchange,
        reason: sig.reason,
        ema_values: '',
        candle_ts: sig.ts,
        created_at: sig.ts,
        price: sig.price,
        qty: sig.qty,
        stoploss_price: sig.stoploss_price,
        live_mode: sig.order_mode === 'REAL',
        profit_cap: sig.profit_cap_hit === true,
        leg: sig.leg,
        strike: sig.strike,
        short: sig.short,
        fno_token: sig.fno_token,
        fno_symbol: sig.fno_symbol,
    };
}

/**
 * Identity of a signal, as the journal's UNIQUE key plus the leg. The journal
 * stores candle_ts in whole seconds (RFC3339) while the WS payload carries
 * RFC3339Nano, so the instant is compared at second precision. REST records
 * only carry the leg as a "[LEG STRIKE]" reason tag, so legInfo reads either.
 */
export function signalKey(r: SignalRecord): string {
    const sec = Math.floor(Date.parse(r.candle_ts) / 1000);
    const li = legInfo(r);
    return [r.strategy, r.action, r.side || '', r.exchange, r.token, sec, li?.leg ?? '', li?.strike || ''].join('|');
}

function signalMs(r: SignalRecord): number {
    const ms = Date.parse(r.created_at || r.candle_ts);
    return Number.isNaN(ms) ? 0 : ms;
}

/**
 * Union of held and REST signals, newest first. A REST record wins over a
 * live copy of the same signal, since it carries the journal id.
 */
export function mergeSignalLists(held: SignalRecord[], history: SignalRecord[]): SignalRecord[] {
    const byKey = new Map<string, SignalRecord>();
    for (const r of held) byKey.set(signalKey(r), r);
    for (const r of history) byKey.set(signalKey(r), r);
    return [...byKey.values()].sort((a, b) => signalMs(b) - signalMs(a)).slice(0, MAX_SIGNALS);
}

export const useSignalStore = create<SignalState>((set) => ({
    signals: [],
    unreadCount: 0,
    audioEnabled: true,

    addLiveSignal: (sig) => set((s) => {
        const record = liveToRecord(sig);
        // Reconnect replay, gap backfill and SNAPSHOT recovery can each
        // redeliver a signal already held; a repeat would double its marker.
        const key = signalKey(record);
        if (s.signals.some(x => signalKey(x) === key)) return s;
        const next = [record, ...s.signals].slice(0, MAX_SIGNALS);
        return { signals: next, unreadCount: s.unreadCount + 1 };
    }),

    setSignals: (sigs) => set({ signals: sigs }),

    mergeSignals: (history) => set((s) => ({ signals: mergeSignalLists(s.signals, history) })),

    clearUnread: () => set({ unreadCount: 0 }),

    toggleAudio: () => set((s) => ({ audioEnabled: !s.audioEnabled })),
}));
