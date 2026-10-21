import { Fragment, useState, type KeyboardEvent } from 'react';
import { ClipboardList, CircleDot, BarChart3, Zap, ChevronDown, ChevronRight, TrendingUp, CalendarDays, User, Settings, Activity, type LucideIcon } from 'lucide-react';
import { SignalRow } from '../components/signals/SignalRow';
import { LiveOrdersTab } from '../components/signals/LiveOrdersTab';
import { EventPriceTab } from '../components/signals/EventPriceTab';
import { PnLSummaryTab } from '../components/signals/PnLSummaryTab';
import { DailyAnalyticsTab } from '../components/signals/DailyAnalyticsTab';
import { AccountTab } from '../components/signals/AccountTab';
import { TradingSettingsTab } from '../components/signals/TradingSettingsTab';
import { SessionHealthTab } from '../components/signals/SessionHealthTab';
import { formatDateKey, signalKey, useSignalsData, type SignalsData } from '../components/signals/useSignalsData';
import { SignalsMobile } from '../components/signals/mobile/SignalsMobile';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';
import { cn } from '@/lib/utils';
import { ctrlBtn, dayBody, dayCard, dayDate, dayHead, dayLeft, dayList, dayMeta, dayRight, emptyState, signalsPage, table, tableWrap, th, thNum } from '../components/signals/classes';

type SignalTab = 'LOG' | 'LIVE' | 'EVENT_PRICE' | 'DAILY' | 'PNL' | 'ACCOUNT' | 'SETTINGS' | 'SESSION';

type TabGroup = 'activity' | 'performance' | 'system';

const TAB_GROUPS: TabGroup[] = ['activity', 'performance', 'system'];

const stat = 'flex flex-col justify-center gap-[2px] min-w-[104px] py-[8px] px-[18px] border-r border-r-sg-line-soft last:[border-right:none] lte-640:min-w-0 lte-640:px-[12px] lte-640:nth-[3n]:[border-right:none]';
const statLabel = 'text-[0.78rem] text-sg-muted whitespace-nowrap lte-640:overflow-hidden lte-640:text-ellipsis';
const statValue = 'inline-flex items-center gap-[8px] text-[1.6rem] font-semibold leading-[1.15] tracking-[-0.02em] tabular-nums lte-640:text-[1.3rem]';
const filterField = 'h-[34px] py-0 px-[10px] bg-sg-bg border border-solid border-sg-line rounded-ctrl text-sg-text [font-family:inherit] text-[0.85rem] [transition:border-color_0.15s] hover:border-sg-faint lte-640:flex-[1_1_140px] lte-640:min-w-0';

const TABS: { id: SignalTab; label: string; Icon: LucideIcon; group: TabGroup }[] = [
    { id: 'LOG', label: 'Signal log', Icon: ClipboardList, group: 'activity' },
    { id: 'LIVE', label: 'Live orders', Icon: CircleDot, group: 'activity' },
    { id: 'EVENT_PRICE', label: 'Trade events', Icon: BarChart3, group: 'activity' },
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
        <div className={signalsPage}>
            <header className="flex items-stretch gap-[12px] p-[6px] bg-sg-surface border border-solid border-sg-line rounded-panel lte-640:flex-col lte-640:p-[4px]">
                <div className="flex flex-1 min-w-0 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden lte-640:grid lte-640:grid-cols-[repeat(3,minmax(0,1fr))] lte-640:gap-y-[4px] lte-640:overflow-visible">
                    <div className={stat}>
                        <span className={statLabel}>Total signals</span>
                        <span className={cn(statValue, 'text-sg-text')}>{signals.length}</span>
                    </div>
                    <div className={stat}>
                        <span className={statLabel}>Today</span>
                        <span className={cn(statValue, 'text-sg-text')}>{todayCount}</span>
                    </div>
                    <div className={stat}>
                        <span className={statLabel}>Entries</span>
                        <span className={cn(statValue, 'text-sg-profit')}>{buyCount}</span>
                    </div>
                    <div className={stat}>
                        <span className={statLabel}>Exits</span>
                        <span className={cn(statValue, 'text-sg-text-2')}>{exitCount}</span>
                    </div>
                    <div className={stat}>
                        <span className={statLabel}>Open positions</span>
                        <span className={cn(statValue, 'text-sg-accent')}>
                            {openOrders.length > 0 && <span className="w-[8px] h-[8px] rounded-[50%] bg-sg-accent shadow-[0_0_0_0_rgba(91,141,239,0.55)] animate-[live-dot_2s_ease-out_infinite] motion-reduce:animate-none" aria-hidden="true" />}
                            {openOrders.length}
                        </span>
                    </div>
                </div>
            </header>

            <nav className="flex items-center gap-[4px] p-[4px] bg-sg-surface border border-solid border-sg-line rounded-panel overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" role="tablist" aria-label="Signals views">
                {TAB_GROUPS.map((group, gi) => (
                    <Fragment key={group}>
                        {gi > 0 && <span className="w-[1px] h-[20px] my-0 mx-[6px] bg-sg-line shrink-0" aria-hidden="true" />}
                        <div className="flex gap-[2px] shrink-0">
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
                                        className={cn(
                                            'inline-flex items-center gap-[7px] py-[8px] px-[12px] [border:none] rounded-[10px] text-[0.86rem] font-medium whitespace-nowrap cursor-pointer [transition:background_0.15s,color_0.15s]',
                                            selected
                                                ? 'text-sg-text bg-sg-hover shadow-[inset_0_0_0_1px_var(--sg-line)] focus-visible:aria-selected:shadow-[inset_0_0_0_1px_var(--sg-line)] [&_svg]:text-sg-accent'
                                                : 'bg-transparent text-sg-muted hover:text-sg-text hover:bg-sg-raised',
                                        )}
                                        onClick={() => setActiveTab(id)}
                                        onKeyDown={onTabKeyDown}
                                    >
                                        <Icon size={15} /> {label}
                                        {id === 'LIVE' && openOrders.length > 0 && (
                                            <span className="min-w-[18px] py-[1px] px-[6px] rounded-pill bg-sg-accent text-white text-[0.7rem] font-semibold text-center tabular-nums">{openOrders.length}</span>
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
                className="flex flex-col gap-[12px] min-w-0 p-[14px] bg-sg-surface border border-solid border-sg-line rounded-panel lte-640:p-[10px]"
                role="tabpanel"
                aria-labelledby={`signals-tab-${activeTab}`}
            >
            {(activeTab === 'LOG' || activeTab === 'LIVE' || activeTab === 'EVENT_PRICE') && (
                <div className="flex items-center gap-[8px] flex-wrap">
                    <select className={filterField} value={stratFilter} onChange={e => setStratFilter(e.target.value)} aria-label="Strategy">
                        <option value="ALL">All strategies</option>
                        {strategyNames.map(name => (
                            <option key={name} value={name}>{name}</option>
                        ))}
                    </select>
                    {activeTab === 'LOG' && (
                        <>
                            <select className={filterField} value={actionFilter} onChange={e => setActionFilter(e.target.value)} aria-label="Action">
                                <option value="ALL">All actions</option>
                                <option value="BUY">Entries (BUY)</option>
                                <option value="EXIT">Exits (EXIT)</option>
                                <option value="WATCH">Exit watch (WATCH_*)</option>
                            </select>
                            <input
                                className={cn(filterField, 'min-w-[200px] placeholder:text-sg-faint')}
                                type="search"
                                placeholder="Filter by token, e.g. NFO:43521"
                                aria-label="Filter by token"
                                value={tokenFilter}
                                onChange={e => setTokenFilter(e.target.value)}
                            />
                            {filtersActive && (
                                <button
                                    className="h-[34px] py-0 px-[10px] bg-transparent [border:none] rounded-ctrl text-sg-accent text-[0.85rem] cursor-pointer hover:bg-sg-accent-soft"
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
                        <div className={emptyState}>
                            <p>Loading signals…</p>
                        </div>
                    ) : filtered.length === 0 ? (
                        <div className={emptyState}>
                            <Zap size={48} strokeWidth={1.5} />
                            <p>
                                {filtersActive
                                    ? 'No signals match these filters. Clear filters to see all signals.'
                                    : 'No signals yet. They appear here as soon as a strategy triggers.'}
                            </p>
                        </div>
                    ) : (
                        <div className={dayList}>
                            {logGroups.map((group) => {
                                const expanded = !!expandedLogDates[group.date];

                                return (
                                    <section key={group.date} className={dayCard}>
                                        <button
                                            className={dayHead}
                                            aria-expanded={expanded}
                                            onClick={() => setExpandedLogDates(prev => ({ ...prev, [group.date]: !prev[group.date] }))}
                                        >
                                            <span className={dayLeft}>
                                                {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                                                <span className={dayDate}>{formatDateKey(group.date)}</span>
                                            </span>
                                            <span className={dayRight}>
                                                <span className={dayMeta}>{group.signals.length} signals</span>
                                                <span className={dayMeta}>{group.buys} entries · {group.exits} exits</span>
                                            </span>
                                        </button>

                                        {expanded && (
                                            <div className={dayBody}>
                                                <div className={tableWrap({ dayBody: true, fullHeight: true })}>
                                                    <table className={table}>
                                                        <thead>
                                                            <tr>
                                                                <th className={th} style={{ width: '130px' }}>Time</th>
                                                                <th className={th} style={{ width: '76px' }}>Action</th>
                                                                <th className={th} style={{ width: '70px' }}>Side</th>
                                                                <th className={th} style={{ width: '112px' }}>Market</th>
                                                                <th className={th} style={{ width: '130px' }}>Strategy</th>
                                                                <th className={th} style={{ width: '104px' }}>Mode</th>
                                                                <th className={th} style={{ width: '140px' }}>Instrument</th>
                                                                <th className={thNum} style={{ width: '110px' }}>Price</th>
                                                                <th className={th}>Reason</th>
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

                    <div className="flex items-center gap-[8px] pt-[2px]">
                        <span className="mr-auto text-sg-muted text-[0.82rem] tabular-nums">
                            {filtered.length} signals
                            <span className="text-sg-faint"> over {logGroups.length} day{logGroups.length === 1 ? '' : 's'}</span>
                        </span>
                        <button
                            className={ctrlBtn}
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
                            className={ctrlBtn}
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

            {activeTab === 'DAILY' && <DailyAnalyticsTab />}

            {activeTab === 'PNL' && <PnLSummaryTab />}

            {activeTab === 'ACCOUNT' && <AccountTab />}

            {activeTab === 'SESSION' && <SessionHealthTab />}

            {activeTab === 'SETTINGS' && <TradingSettingsTab />}
            </section>
        </div>
    );
}
