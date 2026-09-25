import { create } from 'zustand';
import type { SignalPayload, SignalRecord } from '../types/signal';

const MAX_SIGNALS = 200;

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
    };
}

export const useSignalStore = create<SignalState>((set) => ({
    signals: [],
    unreadCount: 0,
    audioEnabled: true,

    addLiveSignal: (sig) => set((s) => {
        const record = liveToRecord(sig);
        const next = [record, ...s.signals].slice(0, MAX_SIGNALS);
        return { signals: next, unreadCount: s.unreadCount + 1 };
    }),

    setSignals: (sigs) => set({ signals: sigs }),

    clearUnread: () => set({ unreadCount: 0 }),

    toggleAudio: () => set((s) => ({ audioEnabled: !s.audioEnabled })),
}));
