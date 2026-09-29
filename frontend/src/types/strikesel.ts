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
}

export interface SRLastPick extends SRContract {
    side: string;
    asked_strike: number;
    ts: string;
}

export interface StrikeSelView {
    strategy: string;
    updated_at: string;
    params: {
        delta_min: number;
        delta_max: number;
        max_theta_pct: number;
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
}
