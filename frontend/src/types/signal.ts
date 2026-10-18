// ── Signal Types ──

export interface SignalPayload {
    strategy_name: string;
    action: 'BUY' | 'EXIT' | 'WATCH_EXIT' | 'WATCH_BUY';
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
    /** Multi-leg basket fields — paper legs only */
    leg?: string;
    strike?: number;
    short?: boolean;
    fno_token?: string;
    fno_symbol?: string;
    /** Picker that chose the contract: 'old' or 'new' (entries only) */
    pick?: string;
    /** Other picker's contract when it differed (shadow mode) */
    pick_alt?: string;
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
    /** Traded option (journal + live WS); empty for older journal rows */
    fno_token?: string;
    fno_symbol?: string;
    /** Picker that chose the contract: 'old' or 'new' (entries only) */
    pick?: string;
    /** Other picker's contract when it differed (shadow mode) */
    pick_alt?: string;
    /** Multi-leg fields (live WS only; REST history carries a "[LEG STRIKE]" reason tag) */
    leg?: string;
    strike?: number;
    short?: boolean;
}

export interface LiveOrderStatePayload {
    strategy_name: string;
    side: string;
    fno_token?: string;
    fno_symbol?: string;
    entry_fno_price?: number;
    current_fno_price?: number;
    best_fno_price?: number;
    stoploss_price?: number;
    stoploss_kind?: string;
    /** Premium target, paise */
    target_price?: number;
    /** Index stop / target, paise */
    index_stop?: number;
    index_target?: number;
    /** Index levels moved by hand */
    manual?: boolean;
    /** Real-order strategy: read-only on the dashboard */
    real?: boolean;
    leg?: string;
    strike?: number;
    short?: boolean;
}

export interface LiveOrdersPayload {
    orders: LiveOrderStatePayload[];
}
