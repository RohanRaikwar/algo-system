/** NIFTY50_SR strike selection view (pub:strikesel). Premium is rupees, IV is %. */
export interface SRContract {
    strike: number;
    option: 'CE' | 'PE' | string;
    symbol?: string;
    token?: string;
    expiry: string; // YYYY-MM-DD
    dte: number;
    delta: number;
    gamma: number;
    theta: number;
    vega: number;
    iv: number;
    premium: number;
    liquidity: number;
}

export interface SRRejects {
    delta: number;
    premium: number;
    theta: number;
    gamma: number;
    liquidity: number;
    iv: number;
    cost: number;
}

export interface SRSide {
    pick?: SRContract;
    rejects: SRRejects;
    error?: string;
    /** Global option picker's live pick for the same SR intent (while the picker runs). */
    new_pick?: PickerLivePick;
    new_rejects?: string;
    new_error?: string;
    /** New pick has the same strike and expiry as the old one. */
    agree?: boolean;
}

/** Picker's live contract with the quote it was chosen on. Prices are rupees. */
export interface PickerLivePick {
    strike: number;
    option: string;
    symbol?: string;
    token?: string;
    expiry: string;
    dte: number;
    delta: number;
    iv: number;
    bid: number;
    ask: number;
    mid: number;
    spread_pct: number;
    quote_age_s: number;
    score: number;
    liquidity: number;
    /** Rules this pick breaks: nothing passed them all, so the best tradable contract was taken. */
    waived?: string[];
}

export interface SRLastPick extends SRContract {
    side: string;
    asked_strike: number;
    ts: string;
}

export interface PickerDecision {
    strategy: string;
    mode: string;
    result: 'picked' | 'refused' | string;
    reason?: string;
    strike?: number;
    symbol?: string;
    token?: string;
    delta?: number;
    iv?: number;
    bid?: number; // paise
    ask?: number; // paise
    /** Expected return on premium for the target move (0.25 = 25 %). */
    score?: number;
    /** Rules the pick breaks (nothing passed them all). */
    waived?: string[];
    /** What the old (non-picker) path chose for the same entry — shadow
     *  mode never substitutes it, this is only for comparison. */
    old_symbol?: string;
    agree?: boolean;
    ts: string;
}

export interface PickerView {
    mode: string;
    chain_at?: string;
    chain_error?: string;
    streamed: number;
    spot: number;
    rules: { max_spread_pct: number; max_quote_age_s: number; max_chain_age_s: number; rank?: string };
    decisions?: PickerDecision[];
}

export interface StrikeSelView {
    strategy: string;
    updated_at: string;
    params: {
        delta_min: number;
        delta_max: number;
        theta_max_gain_pct: number; // theta over theta_hold_min ≤ this % of delta × target
        theta_hold_min?: number;
        view_target_pts?: number; // target used for the live view (no signal)
        max_gamma: number;
        gamma_dte: number;
        min_liquidity: number;
        max_buy_iv?: number;
        min_dte?: number;
        cost_multiple?: number;
    };
    error?: string;
    call?: SRSide;
    put?: SRSide;
    last?: SRLastPick;
    picker?: PickerView;
}
