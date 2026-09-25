// ── Market Analyst Types ──

export type MarketState =
    | 'TRENDING_UP'
    | 'TRENDING_DOWN'
    | 'SIDEWAYS'
    | 'CHOPPY'
    | 'MIXED';

export interface IndicatorVote {
    name: string;   // "ADX", "ATR_PCT", "RSI_SLOPE", "EMA_SPREAD", "VWAP_DEV"
    state: MarketState;
    value: number;
}

export interface MarketStateEvent {
    state: MarketState;
    confluence: number;         // 0–5
    direction: string;          // "BULLISH" | "BEARISH" | "NEUTRAL"
    votes: IndicatorVote[];
    token: string;
    exchange: string;
    tf: number;
    ts: string;
    previous_state?: MarketState;
    state_changed_at?: string;
}

export type LevelType =
    | 'HORIZONTAL'
    | 'VWAP'
    | 'EMA'
    | 'PREMARKET'
    | 'ROUND_NUMBER';

export type LevelStrength = 'STRONG' | 'MEDIUM' | 'WEAK';

export interface SRLevel {
    price: number;           // paise
    type: LevelType;
    strength: LevelStrength;
    touch_count: number;
    is_resistance: boolean;
    is_round_number: boolean;
    has_htf: boolean;
    has_vol_spike: boolean;
    last_touch_bar: number;
    created_at: string;
    token?: string;
    exchange?: string;
}

export interface LevelUpdateEvent {
    levels: SRLevel[];
    token: string;
    exchange: string;
    tf: number;
    ts: string;
}

export type BreakoutStage =
    | 'COMPRESSION'
    | 'BREAK'
    | 'RETEST'
    | 'CONFIRMED'
    | 'FAILED';

export interface BreakoutEvent {
    stage: BreakoutStage;
    level: number;           // paise
    direction: string;       // "UP" | "DOWN"
    break_price: number;
    break_volume: number;
    avg_volume: number;
    volume_ratio: number;
    adx_value: number;
    adx_rising: boolean;
    measured_move: number;
    range_height: number;
    bars_since_break: number;
    token: string;
    exchange: string;
    tf: number;
    ts: string;
}
