import type { SignalRecord } from '../../types/signal';
import { isWatchAction, watchBadgeClass, watchExitPricedAtPremium } from './watch';

/** Display helpers shared by the desktop signal table and the phone cards. */

export function formatTime(ts: string): string {
    try {
        const d = new Date(ts);
        return d.toLocaleTimeString('en-IN', {
            hour: '2-digit', minute: '2-digit', second: '2-digit',
            hour12: false, timeZone: 'Asia/Kolkata',
        });
    } catch {
        return ts;
    }
}

export function formatDate(ts: string): string {
    try {
        const d = new Date(ts);
        return d.toLocaleDateString('en-IN', {
            day: '2-digit', month: 'short',
            timeZone: 'Asia/Kolkata',
        });
    } catch {
        return '';
    }
}

export function relativeTime(ts: string, now = Date.now()): string {
    try {
        const diff = now - new Date(ts).getTime();
        if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`;
        if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
        if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
        return `${Math.floor(diff / 86400000)}d ago`;
    } catch {
        return '';
    }
}

export function getActionBadge(action: string): string {
    if (isWatchAction(action)) return watchBadgeClass(action);
    if (action === 'EXIT') return 'action-badge exit';
    return 'action-badge buy-call';
}

export function getSideBadge(side: string): string {
    if (side === 'CALL') return 'action-badge buy-call';
    if (side === 'PUT') return 'action-badge buy-put';
    return 'action-badge';
}

export function normalizeMarketState(marketState?: string, reason?: string): string {
    const raw = (marketState || '').trim().toLowerCase();
    if (raw === 'trending' || raw === 'sideways' || raw === 'choppy' || raw === 'range') {
        return raw.toUpperCase();
    }

    const hay = `${reason || ''}`.toLowerCase();
    if (/\brange\b/.test(hay)) return 'RANGE';
    if (/\bchoppy\b/.test(hay)) return 'CHOPPY';
    if (/\bsideways\b/.test(hay)) return 'SIDEWAYS';
    if (/\bmomentum\b/.test(hay)) return 'TRENDING';
    return '—';
}

export function getMarketBadge(state: string): string {
    if (state === 'TRENDING') return 'market-badge trending';
    if (state === 'SIDEWAYS') return 'market-badge sideways';
    if (state === 'CHOPPY') return 'market-badge choppy';
    if (state === 'RANGE') return 'market-badge range';
    return 'market-badge';
}

/** Paise to "₹123.45"; "—" when missing. */
export function formatPrice(price?: number): string {
    if (price === undefined || price === null || price < 0) return '—';
    return `₹${(price / 100).toFixed(2)}`;
}

/** Paise to "+₹1.20" / "−₹0.85" / "₹0.00". */
export function formatSignedPaise(paise: number): string {
    const sign = paise > 0 ? '+' : paise < 0 ? '−' : '';
    return `${sign}₹${(Math.abs(paise) / 100).toFixed(2)}`;
}

export function pnlClass(paise: number | null): string {
    if (paise === null) return '';
    return paise > 0 ? 'price-up' : paise < 0 ? 'price-down' : 'price-flat';
}

export type OrderMode = 'paper' | 'shadow' | 'capped' | 'real';

export interface OrderModeInfo {
    mode: OrderMode;
    text: string;
    title: string;
}

/**
 * How the signal was executed. Watch advice is Shadow; the profit cap is
 * shown ahead of plain paper so it stands out.
 */
export function orderMode(signal: SignalRecord): OrderModeInfo {
    if (isWatchAction(signal.action)) {
        return { mode: 'shadow', text: 'Shadow', title: 'Exit watch advice only: no order placed' };
    }
    if (signal.profit_cap) {
        return { mode: 'capped', text: 'Capped', title: 'Paper trade: daily profit cap reached' };
    }
    if (signal.live_mode === true) {
        return { mode: 'real', text: 'Real', title: 'Real order placed via broker' };
    }
    return { mode: 'paper', text: 'Paper', title: 'Paper trade (simulation only)' };
}

/**
 * Exit P&L in paise against the paired entry, or null when it does not
 * apply. A WATCH_EXIT shows what exiting there would have locked in.
 */
export function signalPnL(signal: SignalRecord, entryPrice?: number): number | null {
    const isExit = signal.action === 'EXIT' || watchExitPricedAtPremium(signal);
    const exitPrice = signal.price;
    if (!isExit || !entryPrice || entryPrice <= 0 || !exitPrice || exitPrice <= 0) return null;
    return exitPrice - entryPrice;
}

/** Traded option symbol, else exchange:token. */
export function instrumentLabel(signal: SignalRecord): string {
    return signal.fno_symbol || `${signal.exchange}:${signal.token}`;
}
