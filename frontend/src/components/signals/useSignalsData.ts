import { useEffect, useMemo, useState } from 'react';
import { useLiveOrderStore } from '../../store/useLiveOrderStore';
import { useSignalStore } from '../../store/useSignalStore';
import { fetchSignals } from '../../services/api';
import { buildOpenOrders, inferSide } from './signalAnalytics';
import { matchesActionFilter } from './watch';
import type { SignalRecord } from '../../types/signal';

export const IST_DATE_FORMATTER = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Kolkata',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
});

export function istDateKey(ts: string): string {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '';
    return IST_DATE_FORMATTER.format(d);
}

export function signalKey(sig: SignalRecord): string {
    return `${sig.id}|${sig.strategy}|${sig.action}|${sig.exchange}|${sig.token}|${sig.created_at}`;
}

export function formatDateKey(dateKey: string): string {
    try {
        const d = new Date(`${dateKey}T00:00:00+05:30`);
        return d.toLocaleDateString('en-IN', {
            weekday: 'short',
            day: '2-digit',
            month: 'short',
            year: 'numeric',
            timeZone: 'Asia/Kolkata',
        });
    } catch {
        return dateKey;
    }
}

export interface SignalDateGroup {
    date: string;
    signals: SignalRecord[];
    buys: number;
    exits: number;
}

/**
 * Signals page state and derived data, shared by the desktop tables and the
 * phone cards: REST load, filters, day groups, entry pairing, open positions.
 */
export function useSignalsData() {
    const signals = useSignalStore(s => s.signals);
    const mergeSignals = useSignalStore(s => s.mergeSignals);
    const clearUnread = useSignalStore(s => s.clearUnread);
    const liveOrdersByKey = useLiveOrderStore(s => s.ordersByKey);

    const [stratFilter, setStratFilter] = useState('ALL');
    const [actionFilter, setActionFilter] = useState('ALL');
    const [tokenFilter, setTokenFilter] = useState('');
    const [loading, setLoading] = useState(true);
    const [expandedLogDates, setExpandedLogDates] = useState<Record<string, boolean>>({});

    // Track which signals are "new" (live from WS)
    const [initialCount, setInitialCount] = useState(0);

    // Load signals from REST on mount — always fetch fresh
    useEffect(() => {
        clearUnread();
        setLoading(true);
        fetchSignals(500).then(data => {
            mergeSignals(data);
            setInitialCount(useSignalStore.getState().signals.length);
            setLoading(false);
        }).catch(() => setLoading(false));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [clearUnread, mergeSignals]);

    // Clear unread on mount
    useEffect(() => {
        clearUnread();
    }, [clearUnread]);

    // Unique strategy names for the dropdown
    const strategyNames = useMemo(() => {
        const names = new Set(signals.map(s => s.strategy));
        return Array.from(names).sort();
    }, [signals]);

    // Strategy-filtered signals (shared across all tabs)
    const stratFiltered = useMemo(() => {
        if (stratFilter === 'ALL') return signals;
        return signals.filter(s => s.strategy === stratFilter);
    }, [signals, stratFilter]);

    // Fully filtered signals (for LOG tab — adds action + token filters)
    const filtered = useMemo(() => {
        return stratFiltered.filter(s => {
            if (!matchesActionFilter(s.action, actionFilter)) return false;
            if (tokenFilter && !`${s.exchange}:${s.token}`.toLowerCase().includes(tokenFilter.toLowerCase())) return false;
            return true;
        });
    }, [stratFiltered, actionFilter, tokenFilter]);

    // Reset groups when filters change
    useEffect(() => {
        setExpandedLogDates({});
    }, [stratFilter, actionFilter, tokenFilter]);

    const logGroups = useMemo<SignalDateGroup[]>(() => {
        const byDate = new Map<string, SignalDateGroup>();
        for (const sig of filtered) {
            const ts = sig.created_at || sig.candle_ts;
            const date = ts ? istDateKey(ts) : 'unknown';
            const existing = byDate.get(date);
            if (existing) {
                existing.signals.push(sig);
                if (sig.action === 'BUY') existing.buys++;
                if (sig.action === 'EXIT') existing.exits++;
                continue;
            }

            byDate.set(date, {
                date,
                signals: [sig],
                buys: sig.action === 'BUY' ? 1 : 0,
                exits: sig.action === 'EXIT' ? 1 : 0,
            });
        }

        const keys = Array.from(byDate.keys()).sort((a, b) => b.localeCompare(a));
        return keys.map(k => byDate.get(k)!).filter(Boolean);
    }, [filtered]);

    useEffect(() => {
        setExpandedLogDates(prev => {
            const next: Record<string, boolean> = {};
            for (const g of logGroups) {
                if (prev[g.date]) next[g.date] = true;
            }
            if (logGroups.length > 0 && Object.keys(next).length === 0) {
                next[logGroups[0].date] = true;
            }
            return next;
        });
    }, [logGroups]);

    const newSignalIds = useMemo(() => {
        const next = new Set<string>();
        const newCount = Math.max(0, signals.length - initialCount);
        for (const sig of signals.slice(0, newCount)) {
            next.add(signalKey(sig));
        }
        return next;
    }, [signals, initialCount]);

    const openOrders = useMemo(() => buildOpenOrders(stratFiltered, liveOrdersByKey), [stratFiltered, liveOrdersByKey]);

    // Build entry-price lookup: for each EXIT and WATCH_EXIT signal, find the
    // matching BUY from the same day (IST) so SignalRow can display P&L. Keyed
    // by signalKey: exitwatch rows come from a separate journal, so ids collide.
    const entryPriceMap = useMemo(() => {
        const map = new Map<string, { price: number; time: string }>();
        // Process oldest-first (signals are newest-first from store)
        const chronological = [...signals].reverse();
        const pending = new Map<string, { price: number; time: string; day: string }>();

        for (const sig of chronological) {
            const side = inferSide(sig);
            const key = `${sig.strategy}|${sig.exchange}:${sig.token}|${side}`;
            const ts = sig.created_at || sig.candle_ts;
            const day = ts ? istDateKey(ts) : '';

            if (sig.action === 'BUY') {
                const price = sig.price && sig.price > 0 ? sig.price : 0;
                if (price > 0) {
                    pending.set(key, { price, time: ts, day });
                }
            } else if (sig.action === 'EXIT' || sig.action === 'WATCH_EXIT') {
                const entry = pending.get(key);
                if (entry && entry.day === day && entry.price > 0) {
                    map.set(signalKey(sig), { price: entry.price, time: entry.time });
                }
                // WATCH_EXIT is advice: the position stays open for the strategy EXIT.
                if (sig.action === 'EXIT') pending.delete(key);
            }
        }
        return map;
    }, [signals]);

    // Stats
    const todayCount = useMemo(() => {
        const today = IST_DATE_FORMATTER.format(new Date());
        return signals.filter(s => {
            const ts = s.created_at || s.candle_ts;
            if (!ts) return false;
            return istDateKey(ts) === today;
        }).length;
    }, [signals]);

    const buyCount = signals.filter(s => s.action === 'BUY').length;
    const exitCount = signals.filter(s => s.action === 'EXIT').length;

    const filtersActive = stratFilter !== 'ALL' || actionFilter !== 'ALL' || tokenFilter !== '';


    // Today only, for the phone's today strip.
    const today = useMemo(() => {
        const key = IST_DATE_FORMATTER.format(new Date());
        let entries = 0;
        let exits = 0;
        for (const s of signals) {
            const ts = s.created_at || s.candle_ts;
            if (!ts || istDateKey(ts) !== key) continue;
            if (s.action === 'BUY') entries++;
            if (s.action === 'EXIT') exits++;
        }
        return { key, entries, exits };
    }, [signals]);

    const clearFilters = () => {
        setStratFilter('ALL');
        setActionFilter('ALL');
        setTokenFilter('');
    };

    return {
        signals, loading,
        stratFilter, setStratFilter, actionFilter, setActionFilter, tokenFilter, setTokenFilter,
        filtersActive, clearFilters, strategyNames, stratFiltered, filtered,
        logGroups, expandedLogDates, setExpandedLogDates, newSignalIds,
        openOrders, entryPriceMap, todayCount, buyCount, exitCount, today,
    };
}

export type SignalsData = ReturnType<typeof useSignalsData>;
