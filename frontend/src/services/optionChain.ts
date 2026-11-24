/** Option chain and on-click option chart API (gateway option_data.go). */

const BASE = import.meta.env.VITE_API_URL || '';

export interface OptChainLeg {
    token: string;
    symbol: string;
    ltp?: number; // paise
    iv?: number;
    delta?: number;
}

export interface OptChainRow {
    strike: number;
    ce?: OptChainLeg;
    pe?: OptChainLeg;
}

export interface OptChainView {
    index: string;
    name: string;
    exchange: string; // NFO, BFO
    spot: number; // paise
    atm: number;
    step: number;
    expiry: string;
    updated_at: string;
    priced_at?: string;
    price_note?: string;
    rows: OptChainRow[];
}

export interface OptChainIndex {
    index: string;
    name: string;
    expiry: string;
}

/** One bar; t is unix seconds (bar start), prices paise. */
export interface OptBar {
    t: number;
    o: number;
    h: number;
    l: number;
    c: number;
    v: number;
}

export interface OptCandles {
    exchange: string;
    token: string;
    tf: number;
    bars: OptBar[];
    expires_at?: string;
}

async function getJSON<T>(path: string): Promise<T> {
    const res = await fetch(`${BASE}${path}`);
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error((body as { error?: string }).error || `HTTP ${res.status}`);
    return body as T;
}

export function fetchChainIndexes(): Promise<{ indexes: OptChainIndex[] }> {
    return getJSON('/api/optchain');
}

export function fetchChain(index: string): Promise<OptChainView> {
    return getJSON(`/api/optchain?index=${encodeURIComponent(index)}`);
}

export function fetchOptionCandles(exchange: string, token: string, tf: number): Promise<OptCandles> {
    const q = new URLSearchParams({ exchange, token, tf: String(tf) });
    return getJSON(`/api/options/candles?${q}`);
}

/** Renews the live lease of a charted option; the server drops its feed a minute after the last renewal. */
export async function watchOption(exchange: string, token: string): Promise<void> {
    await fetch(`${BASE}/api/options/watch`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ exchange, token }),
    });
}

/** How often an open chart renews its lease (server linger is 60 s). */
export const WATCH_EVERY_MS = 20_000;
