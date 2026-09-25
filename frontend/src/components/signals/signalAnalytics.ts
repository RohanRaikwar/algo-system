import type { LiveOrderStatePayload, SignalRecord } from '../../types/signal';

// ── Types ──

export interface OpenOrder {
    strategy: string;
    side: string;
    instrument: string;
    buyPrice: number | null;
    currentPrice: number | null;
    entryTime: string;
    stoplossPrice: number | null;
    stoplossKind: string | null;
}

export interface CompletedEvent {
    strategy: string;
    side: string;
    instrument: string;
    buyPrice: number | null;
    buyTime: string;
    exitPrice: number | null;
    exitTime: string;
    move: number | null;
}

// ── Pure Helpers ──

/** Infer CALL / PUT — prefers the explicit `side` field from backend. */
export function inferSide(signal: SignalRecord): string {
    if (signal.side && signal.side.length > 0) return signal.side.toUpperCase();
    const hay = `${signal.strategy} ${signal.reason}`.toUpperCase();
    if (hay.includes('CALL')) return 'CALL';
    if (hay.includes('PUT')) return 'PUT';
    return '—';
}

/**
 * Extract a display price from signal.
 * Prefers signal.price (FNO option LTP set by backend) over reason text.
 * Prices are in paise (int) from the backend, so divide by 100 for display.
 */
export function extractPrice(signal: SignalRecord): number | null {
    // Prefer FNO LTP set by backend (paise)
    if (signal.price && signal.price > 0) return signal.price / 100;

    // Fallback: parse from reason text (legacy / NSE close)
    const m = signal.reason?.match(/close[=:]?\s*(\d+)/i);
    if (m) return parseInt(m[1], 10) / 100;

    return null;
}

/**
 * Extract stoploss price from signal reason text.
 * Patterns: "stoploss set to 10400", "SL(10400)"
 * Returns price in rupees (paise / 100).
 */
export function extractStoploss(reason: string): number | null {
    // "stoploss set to 10400 (2nd candle High)"
    const m1 = reason?.match(/stoploss set to (\d+)/i);
    if (m1) return parseInt(m1[1], 10) / 100;

    // "SL(10400)"
    const m2 = reason?.match(/SL\((\d+)\)/i);
    if (m2) return parseInt(m2[1], 10) / 100;

    return null;
}

/**
 * Build the list of currently open (unmatched) orders.
 * An order is open if a BUY has no subsequent matching EXIT
 * (matched by strategy + exchange:token).
 */
function liveOrderKey(strategy: string, side: string): string {
    return `${strategy}|${side.toUpperCase()}`;
}

export function buildOpenOrders(
    signals: SignalRecord[],
    liveOrdersByKey: Record<string, LiveOrderStatePayload> = {},
): OpenOrder[] {
    // signals are newest-first from store; process oldest-first
    const chronological = [...signals].reverse();

    // Track open positions: key → OpenOrder
    const open = new Map<string, OpenOrder>();

    for (const sig of chronological) {
        const key = `${sig.strategy}|${sig.exchange}:${sig.token}`;
        const side = inferSide(sig);
        const live = liveOrdersByKey[liveOrderKey(sig.strategy, side)];

        if (sig.action === 'BUY') {
            // Use backend-computed SL price (paise → rupees), fallback to reason parsing
            let sl: number | null = null;
            if (live?.stoploss_price && live.stoploss_price > 0) {
                sl = live.stoploss_price / 100;
            } else if (sig.stoploss_price && sig.stoploss_price > 0) {
                sl = sig.stoploss_price / 100;
            } else {
                sl = extractStoploss(sig.reason);
            }
            open.set(key, {
                strategy: sig.strategy,
                side,
                instrument: `${sig.exchange}:${sig.token}`,
                buyPrice: extractPrice(sig) ?? (live?.entry_fno_price ? live.entry_fno_price / 100 : null),
                currentPrice: live?.current_fno_price ? live.current_fno_price / 100 : null,
                entryTime: sig.created_at,
                stoplossPrice: sl,
                stoplossKind: live?.stoploss_kind || null,
            });
        } else if (sig.action === 'EXIT') {
            open.delete(key);
        }

        // Legacy fallback: try to extract stoploss from any signal's reason
        const order = open.get(key);
        if (order && order.stoplossPrice === null) {
            const sl = extractStoploss(sig.reason);
            if (sl !== null) {
                order.stoplossPrice = sl;
            }
        }
    }

    for (const order of open.values()) {
        const live = liveOrdersByKey[liveOrderKey(order.strategy, order.side)];
        if (!live) continue;
        if (live.entry_fno_price && order.buyPrice === null) {
            order.buyPrice = live.entry_fno_price / 100;
        }
        if (live.current_fno_price && live.current_fno_price > 0) {
            order.currentPrice = live.current_fno_price / 100;
        }
        if (live.stoploss_price && live.stoploss_price > 0) {
            order.stoplossPrice = live.stoploss_price / 100;
        }
        if (live.stoploss_kind) {
            order.stoplossKind = live.stoploss_kind;
        }
    }

    // Return newest-open first
    return Array.from(open.values()).reverse();
}

/**
 * Build completed trade events by pairing BUY → EXIT signals
 * (matched by strategy + exchange:token) in chronological order.
 */
export function buildCompletedEvents(signals: SignalRecord[]): CompletedEvent[] {
    const chronological = [...signals].reverse();
    const events: CompletedEvent[] = [];

    // Pending BUY signals waiting for a matching EXIT
    const pending = new Map<string, SignalRecord>();

    for (const sig of chronological) {
        const key = `${sig.strategy}|${sig.exchange}:${sig.token}`;

        if (sig.action === 'BUY') {
            pending.set(key, sig);
        } else if (sig.action === 'EXIT' && pending.has(key)) {
            const buy = pending.get(key)!;
            pending.delete(key);

            const buyP = extractPrice(buy);
            const exitP = extractPrice(sig);
            const side = inferSide(buy);

            let move: number | null = null;
            if (buyP !== null && exitP !== null) {
                // FNO options: both CALL and PUT are BUY(entry) → SELL(exit)
                // P&L = sell_price - buy_price (option premium appreciation)
                move = +(exitP - buyP).toFixed(2);
            }

            events.push({
                strategy: buy.strategy,
                side,
                instrument: `${buy.exchange}:${buy.token}`,
                buyPrice: buyP,
                buyTime: buy.created_at,
                exitPrice: exitP,
                exitTime: sig.created_at,
                move,
            });
        }
    }

    // Return newest events first
    return events.reverse();
}
