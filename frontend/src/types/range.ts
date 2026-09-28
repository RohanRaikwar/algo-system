/** NIFTY50_RANGE dashboard view (pub:range). Prices are paise; strikes are index points. */
export interface RangeView {
    strategy: string;
    key: string;
    ts: string;
    regime: 'RANGE' | 'TRENDING' | 'WARMING';
    adx: number;
    max_adx: number;
    support?: number;
    resistance?: number;
    width?: number;
    class?: 'NARROW' | 'MEDIUM' | 'WIDE' | '';
    rsi5: number;
    atr15?: number;
    day_open?: number;
    flag_box_lo?: number;
    flag_box_hi?: number;
    flag_tight: boolean;
    flag_trend_day: boolean;
    day_trend: number;
    pending_side?: 'CALL' | 'PUT';
    pending_kind?: string;
    pending_edge?: number;
    side: 'NONE' | 'CALL' | 'PUT';
    kind?: string;
    entry?: number;
    stop?: number;
    target?: number;
    strike?: number;
    trades_today: number;
    max_trades: number;
    entry_tf: number;
    mean_reversion: boolean;
    breakout: boolean;
    flag: boolean;
    time_exit_min: number;
}
