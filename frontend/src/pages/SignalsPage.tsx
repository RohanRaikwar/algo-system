import { Fragment, useEffect, useState, useMemo, type KeyboardEvent } from 'react';
import { ClipboardList, CircleDot, BarChart3, Crosshair, Bell, BellOff, Zap, ChevronDown, ChevronRight, TrendingUp, CalendarDays, User, Settings, Activity, type LucideIcon } from 'lucide-react';
import { useLiveOrderStore } from '../store/useLiveOrderStore';
import { useSignalStore } from '../store/useSignalStore';
import { fetchSignals } from '../services/api';
import { SignalRow } from '../components/signals/SignalRow';
import { LiveOrdersTab } from '../components/signals/LiveOrdersTab';
import { EventPriceTab } from '../components/signals/EventPriceTab';
import { FnoInstrumentsTab } from '../components/signals/FnoInstrumentsTab';
import { PnLSummaryTab } from '../components/signals/PnLSummaryTab';
import { DailyAnalyticsTab } from '../components/signals/DailyAnalyticsTab';
import { AccountTab } from '../components/signals/AccountTab';
import { TradingSettingsTab } from '../components/signals/TradingSettingsTab';
import { SessionHealthTab } from '../components/signals/SessionHealthTab';
import { buildOpenOrders, inferSide } from '../components/signals/signalAnalytics';
import type { SignalRecord } from '../types/signal';
import '../components/signals/signals.css';

type SignalTab = 'LOG' | 'LIVE' | 'EVENT_PRICE' | 'FNO' | 'DAILY' | 'PNL' | 'ACCOUNT' | 'SETTINGS' | 'SESSION';

type TabGroup = 'activity' | 'market' | 'performance' | 'system';

const TAB_GROUPS: TabGroup[] = ['activity', 'market', 'performance', 'system'];

const TABS: { id: SignalTab; label: string; Icon: LucideIcon; group: TabGroup }[] = [
    { id: 'LOG', label: 'Signal log', Icon: ClipboardList, group: 'activity' },
    { id: 'LIVE', label: 'Live orders', Icon: CircleDot, group: 'activity' },
    { id: 'EVENT_PRICE', label: 'Trade events', Icon: BarChart3, group: 'activity' },
    { id: 'FNO', label: 'FNO instruments', Icon: Crosshair, group: 'market' },
    { id: 'DAILY', label: 'Today', Icon: CalendarDays, group: 'performance' },
    { id: 'PNL', label: 'P&L', Icon: TrendingUp, group: 'performance' },
    { id: 'ACCOUNT', label: 'Account', Icon: User, group: 'system' },
    { id: 'SESSION', label: 'Session health', Icon: Activity, group: 'system' },
    { id: 'SETTINGS', label: 'Settings', Icon: Settings, group: 'system' },
];

const IST_DATE_FORMATTER = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Kolkata',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
});

function istDateKey(ts: string): string {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '';
    return IST_DATE_FORMATTER.format(d);
}

function signalKey(sig: SignalRecord): string {
    return `${sig.id}|${sig.strategy}|${sig.action}|${sig.exchange}|${sig.token}|${sig.created_at}`;
}

function formatDateKey(dateKey: string): string {
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

interface SignalDateGroup {
    date: string;
    signals: SignalRecord[];
    buys: number;
    exits: number;
}

export function SignalsPage() {
    const signals = useSignalStore(s => s.signals);
    const setSignals = useSignalStore(s => s.setSignals);
    const clearUnread = useSignalStore(s => s.clearUnread);
    const audioEnabled = useSignalStore(s => s.audioEnabled);
    const toggleAudio = useSignalStore(s => s.toggleAudio);
    const liveOrdersByKey = useLiveOrderStore(s => s.ordersByKey);

    const [activeTab, setActiveTab] = useState<SignalTab>('LOG');
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
            setSignals(data);
            setInitialCount(data.length);
            setLoading(false);
        }).catch(() => setLoading(false));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [clearUnread, setSignals]);

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
            if (actionFilter !== 'ALL' && s.action !== actionFilter) return false;
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

    // Build entry-price lookup: for each EXIT signal, find the matching BUY
    // from the same day (IST) so SignalRow can display P&L.
    const entryPriceMap = useMemo(() => {
        const map = new Map<number, { price: number; time: string }>();
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
            } else if (sig.action === 'EXIT') {
                const entry = pending.get(key);
                if (entry && entry.day === day && entry.price > 0) {
                    map.set(sig.id, { price: entry.price, time: entry.time });
                }
                pending.delete(key);
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

    const onTabKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
        if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft' && e.key !== 'Home' && e.key !== 'End') return;
        e.preventDefault();
        const idx = TABS.findIndex(t => t.id === activeTab);
        let next = idx;
        if (e.key === 'ArrowRight') next = (idx + 1) % TABS.length;
        if (e.key === 'ArrowLeft') next = (idx - 1 + TABS.length) % TABS.length;
        if (e.key === 'Home') next = 0;
        if (e.key === 'End') next = TABS.length - 1;
        setActiveTab(TABS[next].id);
        document.getElementById(`signals-tab-${TABS[next].id}`)?.focus();
    };

    return (
        <div className="signals-page">
            <header className="signals-header">
                <div className="signals-stats">
                    <div className="stat">
                        <span className="stat-label">Total signals</span>
                        <span className="stat-value">{signals.length}</span>
                    </div>
                    <div className="stat">
                        <span className="stat-label">Today</span>
                        <span className="stat-value">{todayCount}</span>
                    </div>
                    <div className="stat">
                        <span className="stat-label">Entries</span>
                        <span className="stat-value buy">{buyCount}</span>
                    </div>
                    <div className="stat">
                        <span className="stat-label">Exits</span>
                        <span className="stat-value exit">{exitCount}</span>
                    </div>
                    <div className="stat">
                        <span className="stat-label">Open positions</span>
                        <span className="stat-value open">
                            {openOrders.length > 0 && <span className="live-dot" aria-hidden="true" />}
                            {openOrders.length}
                        </span>
                    </div>
                </div>
                <button
                    className={`audio-toggle${audioEnabled ? '' : ' muted'}`}
                    onClick={toggleAudio}
                    aria-pressed={audioEnabled}
                    title={audioEnabled ? 'Mute signal alerts' : 'Play a sound on new signals'}
                >
                    {audioEnabled ? <><Bell size={14} /> Sound on</> : <><BellOff size={14} /> Sound off</>}
                </button>
            </header>

            <nav className="signals-tabs" role="tablist" aria-label="Signals views">
                {TAB_GROUPS.map((group, gi) => (
                    <Fragment key={group}>
                        {gi > 0 && <span className="signals-tab-sep" aria-hidden="true" />}
                        <div className="signals-tab-group">
                            {TABS.filter(t => t.group === group).map(({ id, label, Icon }) => {
                                const selected = activeTab === id;
                                return (
                                    <button
                                        key={id}
                                        id={`signals-tab-${id}`}
                                        role="tab"
                                        aria-selected={selected}
                                        aria-controls="signals-panel"
                                        tabIndex={selected ? 0 : -1}
                                        className={`signals-tab${selected ? ' active' : ''}`}
                                        onClick={() => setActiveTab(id)}
                                        onKeyDown={onTabKeyDown}
                                    >
                                        <Icon size={15} /> {label}
                                        {id === 'LIVE' && openOrders.length > 0 && (
                                            <span className="tab-count">{openOrders.length}</span>
                                        )}
                                    </button>
                                );
                            })}
                        </div>
                    </Fragment>
                ))}
            </nav>

            <section
                id="signals-panel"
                className="signals-panel"
                role="tabpanel"
                aria-labelledby={`signals-tab-${activeTab}`}
            >
            {(activeTab === 'LOG' || activeTab === 'LIVE' || activeTab === 'EVENT_PRICE') && (
                <div className="signals-filters">
                    <select value={stratFilter} onChange={e => setStratFilter(e.target.value)} aria-label="Strategy">
                        <option value="ALL">All strategies</option>
                        {strategyNames.map(name => (
                            <option key={name} value={name}>{name}</option>
                        ))}
                    </select>
                    {activeTab === 'LOG' && (
                        <>
                            <select value={actionFilter} onChange={e => setActionFilter(e.target.value)} aria-label="Action">
                                <option value="ALL">All actions</option>
                                <option value="BUY">Entries (BUY)</option>
                                <option value="EXIT">Exits (EXIT)</option>
                            </select>
                            <input
                                type="search"
                                placeholder="Filter by token, e.g. NFO:43521"
                                aria-label="Filter by token"
                                value={tokenFilter}
                                onChange={e => setTokenFilter(e.target.value)}
                            />
                            {filtersActive && (
                                <button
                                    className="filter-reset"
                                    onClick={() => { setStratFilter('ALL'); setActionFilter('ALL'); setTokenFilter(''); }}
                                >
                                    Clear filters
                                </button>
                            )}
                        </>
                    )}
                </div>
            )}

            {/* Tab Content */}
            {activeTab === 'LOG' && (
                <>
                    {loading ? (
                        <div className="signals-empty">
                            <p>Loading signals…</p>
                        </div>
                    ) : filtered.length === 0 ? (
                        <div className="signals-empty">
                            <Zap size={48} strokeWidth={1.5} />
                            <p>
                                {filtersActive
                                    ? 'No signals match these filters. Clear filters to see all signals.'
                                    : 'No signals yet. They appear here as soon as a strategy triggers.'}
                            </p>
                        </div>
                    ) : (
                        <div className="daily-day-list">
                            {logGroups.map((group) => {
                                const expanded = !!expandedLogDates[group.date];

                                return (
                                    <section key={group.date} className="daily-day-card">
                                        <button
                                            className="daily-day-head"
                                            aria-expanded={expanded}
                                            onClick={() => setExpandedLogDates(prev => ({ ...prev, [group.date]: !prev[group.date] }))}
                                        >
                                            <span className="daily-day-left">
                                                {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                                                <span className="daily-day-date">{formatDateKey(group.date)}</span>
                                            </span>
                                            <span className="daily-day-right">
                                                <span className="daily-day-meta">{group.signals.length} signals</span>
                                                <span className="daily-day-meta">{group.buys} entries · {group.exits} exits</span>
                                            </span>
                                        </button>

                                        {expanded && (
                                            <div className="daily-day-body">
                                                <div className="signals-table-wrap log-table-wrap">
                                                    <table className="signals-table">
                                                        <thead>
                                                            <tr>
                                                                <th style={{ width: '130px' }}>Time</th>
                                                                <th style={{ width: '76px' }}>Action</th>
                                                                <th style={{ width: '70px' }}>Side</th>
                                                                <th style={{ width: '112px' }}>Market</th>
                                                                <th style={{ width: '130px' }}>Strategy</th>
                                                                <th style={{ width: '104px' }}>Mode</th>
                                                                <th style={{ width: '140px' }}>Instrument</th>
                                                                <th className="num" style={{ width: '110px' }}>Price</th>
                                                                <th>Reason</th>
                                                            </tr>
                                                        </thead>
                                                        <tbody>
                                                            {group.signals.map((sig) => {
                                                                const entry = entryPriceMap.get(sig.id);
                                                                return (
                                                                    <SignalRow
                                                                        key={signalKey(sig)}
                                                                        signal={sig}
                                                                        isNew={newSignalIds.has(signalKey(sig))}
                                                                        entryPrice={entry?.price}
                                                                        entryTime={entry?.time}
                                                                    />
                                                                );
                                                            })}
                                                        </tbody>
                                                    </table>
                                                </div>
                                            </div>
                                        )}
                                    </section>
                                );
                            })}
                        </div>
                    )}

                    <div className="pagination-bar">
                        <span className="pagination-info">
                            {filtered.length} signals
                            <span className="pagination-total"> over {logGroups.length} day{logGroups.length === 1 ? '' : 's'}</span>
                        </span>
                        <button
                            className="pagination-btn"
                            onClick={() => setExpandedLogDates(
                                logGroups.reduce<Record<string, boolean>>((acc, g) => {
                                    acc[g.date] = true;
                                    return acc;
                                }, {}),
                            )}
                            disabled={logGroups.length === 0 || logGroups.every(g => expandedLogDates[g.date])}
                        >
                            Expand all
                        </button>
                        <button
                            className="pagination-btn"
                            onClick={() => setExpandedLogDates({})}
                            disabled={Object.keys(expandedLogDates).length === 0}
                        >
                            Collapse all
                        </button>
                    </div>
                </>
            )}

            {activeTab === 'LIVE' && <LiveOrdersTab orders={openOrders} />}

            {activeTab === 'EVENT_PRICE' && <EventPriceTab strategyFilter={stratFilter} />}

            {activeTab === 'FNO' && <FnoInstrumentsTab />}

            {activeTab === 'DAILY' && <DailyAnalyticsTab />}

            {activeTab === 'PNL' && <PnLSummaryTab />}

            {activeTab === 'ACCOUNT' && <AccountTab />}

            {activeTab === 'SESSION' && <SessionHealthTab />}

            {activeTab === 'SETTINGS' && <TradingSettingsTab />}
            </section>
        </div>
    );
}
