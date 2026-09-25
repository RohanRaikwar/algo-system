// ── Signal Types ──

export interface SignalPayload {
    strategy_name: string;
    action: 'BUY' | 'EXIT';
    side?: string;
    market_state?: string;
    token: string;
    exchange: string;
    qty: number;
    price: number;
    reason: string;
    ts: string;
    entry_fno_price?: number;
    current_fno_price?: number;
    stoploss_price?: number;
    stoploss_kind?: string;
    order_mode?: string;
    profit_cap_hit?: boolean;
}

export interface SignalRecord {
    id: number;
    strategy: string;
    action: string;
    side?: string;
    market_state?: string;
    token: string;
    exchange: string;
    reason: string;
    ema_values: string;
    candle_ts: string;
    created_at: string;
    price?: number;
    qty?: number;
    stoploss_price?: number;
    live_mode?: boolean;
    profit_cap?: boolean;
}

export interface LiveOrderStatePayload {
    strategy_name: string;
    side: string;
    fno_token?: string;
    entry_fno_price?: number;
    current_fno_price?: number;
    best_fno_price?: number;
    stoploss_price?: number;
    stoploss_kind?: string;
}

export interface LiveOrdersPayload {
    orders: LiveOrderStatePayload[];
}
