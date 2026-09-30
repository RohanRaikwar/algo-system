/** NIFTY50_SR dashboard view (pub:sr). Prices are paise; strikes are index points. */
export interface SRLevel {
    price: number;
    touches: number;
    source: string;
}

export interface SRView {
    strategy: string;
    key: string;
    ts: string;
    regime: string; // RANGE | TREND_UP | TREND_DOWN | MIXED | DEAD | WARMING
    adx: number;
    rsi5: number;
    atr15?: number;
    vwap?: number;
    ema_fast?: number;
    ema_slow?: number;
    or_high?: number;
    or_low?: number;
    close?: number;
    levels: SRLevel[] | null;
    pending_side?: string;
    pending_level?: number;
    pending_left?: number;
    side: string; // NONE | CALL | PUT
    kind?: string;
    entry?: number;
    stop?: number;
    target?: number;
    strike?: number;
    block?: string;
    last_reject?: string;
    last_reject_ts?: string;
    rejects: Record<string, number> | null;
    trades_today: number;
    max_trades: number;
    consec_losses: number;
    day_pnl_pts: number;
    cooldown_left: number;
    entry_tf: number;
    entry_from_min: number;
    entry_to_min: number;
    min_confirmations: number;
    fade: boolean;
    retest: boolean;
    pullback: boolean;
}
