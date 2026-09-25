import type { AppConfig, CandleOut, IndPoint, IndicatorEntry } from '../types/api';

const BASE = import.meta.env.VITE_API_URL || '';

export async function fetchConfig(): Promise<AppConfig> {
    const res = await fetch(`${BASE}/api/config`);
    if (!res.ok) throw new Error('Config fetch failed');
    return res.json();
}

/** Fetch per-TF active indicator config from backend */
export async function fetchActiveConfig(): Promise<Record<number, IndicatorEntry[]>> {
    const res = await fetch(`${BASE}/api/indicators/active`);
    if (!res.ok) throw new Error('Active config fetch failed');
    const data = await res.json();
    // Support both old format {entries:[...]} and new format {byTF:{60:[...],...}}
    if (data.byTF) return data.byTF;
    // Legacy: flat entries array — group by TF
    if (data.entries) {
        const byTF: Record<number, IndicatorEntry[]> = {};
        for (const e of data.entries) {
            if (!byTF[e.tf]) byTF[e.tf] = [];
            byTF[e.tf].push(e);
        }
        return byTF;
    }
    return {};
}

/** Save per-TF active indicator config to backend */
export async function saveActiveConfig(byTF: Record<number, IndicatorEntry[]>): Promise<void> {
    // Send as flat entries array for backward compat with current backend
    const entries: IndicatorEntry[] = [];
    for (const tf of Object.keys(byTF)) {
        for (const e of byTF[Number(tf)]) {
            entries.push({ ...e, tf: Number(tf) });
        }
    }
    const res = await fetch(`${BASE}/api/indicators/active`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ entries }),
    });
    if (!res.ok) throw new Error('Failed to save config');
}

export async function fetchCandles(
    tf: number, token: string, limit: number, before?: string
): Promise<CandleOut[]> {
    let url = `${BASE}/api/candles?tf=${tf}&token=${encodeURIComponent(token)}&limit=${limit}`;
    if (before) url += `&before=${encodeURIComponent(before)}`;
    const res = await fetch(url);
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? data : [];
}

export async function fetchIndicatorHistory(
    name: string, tf: number, token: string, limit: number, before?: string
): Promise<IndPoint[]> {
    let url = `${BASE}/api/indicators/history?name=${encodeURIComponent(name)}&tf=${tf}&token=${encodeURIComponent(token)}&limit=${limit}`;
    if (before) url += `&before=${encodeURIComponent(before)}`;
    const res = await fetch(url);
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? data : [];
}

export async function fetchSignals(limit = 50, strategy?: string): Promise<import('../types/signal').SignalRecord[]> {
    let url = `${BASE}/api/signals?limit=${limit}`;
    if (strategy) url += `&strategy=${encodeURIComponent(strategy)}`;
    const res = await fetch(url);
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? data : [];
}

export async function fetchStrikeInfo(): Promise<import('../store/useStrikeStore').StrikeInfo | null> {
    const res = await fetch(`${BASE}/api/strike`);
    if (!res.ok) return null;
    return res.json();
}

export interface PnLSummary {
    realized_pnl: number;
    total_trades: number;
    wins: number;
    losses: number;
    win_rate: number;
    largest_win: number;
    largest_loss: number;
    open_positions: number;
    total_exposure: number;
    ts?: string;
}

export async function fetchPnLSummary(): Promise<PnLSummary> {
    const res = await fetch(`${BASE}/api/pnl`);
    if (!res.ok) return { realized_pnl: 0, total_trades: 0, wins: 0, losses: 0, win_rate: 0, largest_win: 0, largest_loss: 0, open_positions: 0, total_exposure: 0 };
    return res.json();
}

export interface DailyPnLItem {
    date: string;
    pnl: number;
    trades: number;
    wins: number;
    losses: number;
    win_rate: number;
    largest_win: number;
    largest_loss: number;
}

export interface DailyPnLResponse {
    days: number;
    items: DailyPnLItem[];
    ts?: string;
}

export interface DailyCompletedOrder {
    strategy: string;
    side: string;
    exchange: string;
    token: string;
    instrument: string;
    qty: number;
    entry_price: number;
    exit_price: number;
    realized_pnl: number;
    entry_time: string;
    exit_time: string;
}

export interface DailyOrdersGroup {
    date: string;
    pnl: number;
    trades: number;
    orders: DailyCompletedOrder[];
}

export interface DailyOrdersResponse {
    days: number;
    items: DailyOrdersGroup[];
    ts?: string;
}

export async function fetchDailyPnL(days = 30, strategy?: string): Promise<DailyPnLResponse> {
    let url = `${BASE}/api/pnl/daily?days=${days}`;
    if (strategy) url += `&strategy=${encodeURIComponent(strategy)}`;
    const res = await fetch(url);
    if (!res.ok) return { days, items: [] };
    const data = await res.json();
    return {
        days: typeof data.days === 'number' ? data.days : days,
        items: Array.isArray(data.items) ? data.items : [],
        ts: data.ts,
    };
}

export async function fetchDailyOrders(days = 30, strategy?: string): Promise<DailyOrdersResponse> {
    let url = `${BASE}/api/orders/daily?days=${days}`;
    if (strategy) url += `&strategy=${encodeURIComponent(strategy)}`;
    const res = await fetch(url);
    if (!res.ok) return { days, items: [] };
    const data = await res.json();
    return {
        days: typeof data.days === 'number' ? data.days : days,
        items: Array.isArray(data.items) ? data.items : [],
        ts: data.ts,
    };
}

// Account Balance & Orders (Angel One Integration)
export interface AccountBalance {
    available_balance: number;
    used_margin: number;
    total_balance: number;
    net_available: number;
    last_updated: string;
}

export interface LastOrder {
    id: string;
    strategy: string;
    instrument: string;
    side: string;
    action: string;
    price: number;
    qty: number;
    status: string;
    created_at: string;
}

export interface LastOrdersResponse {
    orders: LastOrder[];
}

export interface UserProfile {
    status?: boolean;
    message?: string;
    data?: {
        clientcode: string;
        name: string;
        email: string;
        mobileno: string;
        exchanges: string[];
        products: string[];
        lastlogintime: string;
        broker?: string;
    };
}

export async function fetchAccountBalance(): Promise<AccountBalance> {
    const res = await fetch(`${BASE}/api/account/balance`);
    if (!res.ok) throw new Error('Failed to fetch account balance');
    return res.json();
}

export async function fetchLastOrders(limit: number = 10): Promise<LastOrdersResponse> {
    const res = await fetch(`${BASE}/api/account/orders?limit=${limit}`);
    if (!res.ok) throw new Error('Failed to fetch last orders');
    return res.json();
}

export async function fetchUserProfile(): Promise<UserProfile> {
    const res = await fetch(`${BASE}/api/account/profile`);
    if (!res.ok) throw new Error('Failed to fetch user profile');
    return res.json();
}


export interface SessionHealth {
    service: string;
    purpose: string;
    healthy?: boolean;
    valid?: boolean;
    age_minutes?: number;
    time_until_refresh?: string;
    circuit_breaker_open?: boolean;
    total_refreshes?: number;
    successful_refreshes?: number;
    failed_refreshes?: number;
    last_refresh?: string;
    status?: string;
    message?: string;
}

export interface AllSessionsHealth {
    timestamp: string;
    sessions: {
        api_gateway: SessionHealth;
        stratengine: SessionHealth;
    };
    overall: {
        all_healthy: boolean;
        total_sessions: number;
        healthy_sessions: number;
    };
}

export async function fetchAllSessionsHealth(): Promise<AllSessionsHealth | null> {
    try {
        const res = await fetch(`${BASE}/api/sessions/health`);
        if (!res.ok) return null;
        return res.json();
    } catch (error) {
        console.error('Failed to fetch sessions health:', error);
        return null;
    }
}

/**
 * Stored dashboard trading config (GET/POST /api/trading/config).
 * stratengine honours only `killSwitch`; the other fields are kept so a
 * POST passes the gateway's range validation, but they change nothing.
 */
export type StoredTradingConfig = Record<string, unknown> & { killSwitch: boolean };

async function errorText(res: Response): Promise<string> {
    const body = (await res.text()).trim();
    try {
        const parsed = JSON.parse(body);
        if (parsed && typeof parsed.error === 'string') return parsed.error;
    } catch { /* plain-text body from http.Error */ }
    return body || `HTTP ${res.status}`;
}

export async function fetchTradingConfig(): Promise<StoredTradingConfig> {
    const res = await fetch(`${BASE}/api/trading/config`);
    if (!res.ok) throw new Error(await errorText(res));
    const data = await res.json();
    return { ...data, killSwitch: data?.killSwitch === true };
}

export async function saveTradingConfig(cfg: StoredTradingConfig): Promise<void> {
    const res = await fetch(`${BASE}/api/trading/config`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(cfg),
    });
    if (!res.ok) throw new Error(await errorText(res));
}
