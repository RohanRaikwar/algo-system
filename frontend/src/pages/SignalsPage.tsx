import { Fragment, useState, type KeyboardEvent } from 'react';
import { ClipboardList, CircleDot, BarChart3, Crosshair, Zap, ChevronDown, ChevronRight, TrendingUp, CalendarDays, User, Settings, Activity, type LucideIcon } from 'lucide-react';
import { SignalRow } from '../components/signals/SignalRow';
import { LiveOrdersTab } from '../components/signals/LiveOrdersTab';
import { EventPriceTab } from '../components/signals/EventPriceTab';
import { FnoInstrumentsTab } from '../components/signals/FnoInstrumentsTab';
import { PnLSummaryTab } from '../components/signals/PnLSummaryTab';
import { DailyAnalyticsTab } from '../components/signals/DailyAnalyticsTab';
import { AccountTab } from '../components/signals/AccountTab';
import { TradingSettingsTab } from '../components/signals/TradingSettingsTab';
import { SessionHealthTab } from '../components/signals/SessionHealthTab';
import '../components/signals/signals.css';
import { formatDateKey, signalKey, useSignalsData, type SignalsData } from '../components/signals/useSignalsData';
import { SignalsMobile } from '../components/signals/mobile/SignalsMobile';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';

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

export function SignalsPage() {
    const data = useSignalsData();
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    if (isPhone) return <SignalsMobile data={data} />;
    return <SignalsDesktop data={data} />;
}

function SignalsDesktop({ data }: { data: SignalsData }) {
    const {
        signals, loading,
        stratFilter, setStratFilter, actionFilter, setActionFilter, tokenFilter, setTokenFilter,
        filtersActive, strategyNames, filtered,
        logGroups, expandedLogDates, setExpandedLogDates, newSignalIds,
        openOrders, entryPriceMap, todayCount, buyCount, exitCount,
    } = data;
    const [activeTab, setActiveTab] = useState<SignalTab>('LOG');

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
                                <option value="WATCH">Exit watch (WATCH_*)</option>
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
                                                                const entry = entryPriceMap.get(signalKey(sig));
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
