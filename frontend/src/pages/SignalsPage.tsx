import { Fragment, useState } from 'react';
import { ClipboardList, CircleDot, BarChart3, Zap, ChevronDown, ChevronRight, TrendingUp, CalendarDays, User, Settings, Activity, Send, type LucideIcon } from 'lucide-react';
import { SignalRow } from '../components/signals/SignalRow';
import { LiveOrdersTab } from '../components/signals/LiveOrdersTab';
import { EventPriceTab } from '../components/signals/EventPriceTab';
import { PnLSummaryTab } from '../components/signals/PnLSummaryTab';
import { DailyAnalyticsTab } from '../components/signals/DailyAnalyticsTab';
import { AccountTab } from '../components/signals/AccountTab';
import { TradingSettingsTab } from '../components/signals/TradingSettingsTab';
import { SessionHealthTab } from '../components/signals/SessionHealthTab';
import { TelegramPanel } from '../components/telegram/TelegramPanel';
import { formatDateKey, signalKey, useSignalsData, type SignalsData } from '../components/signals/useSignalsData';
import { SignalsMobile } from '../components/signals/mobile/SignalsMobile';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Empty } from '@/components/ui/empty';
import { Input } from '@/components/ui/input';
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select';
import { Separator } from '@/components/ui/separator';
import { Table, TableBody, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { dayBody, dayDate, dayHead, dayLeft, dayList, dayMeta, dayRight, signalsPage, tableWrap } from '../components/signals/classes';

type SignalTab = 'LOG' | 'LIVE' | 'EVENT_PRICE' | 'DAILY' | 'PNL' | 'ACCOUNT' | 'SETTINGS' | 'SESSION' | 'TELEGRAM';

type TabGroup = 'activity' | 'performance' | 'system';

const TAB_GROUPS: TabGroup[] = ['activity', 'performance', 'system'];

const stat = 'flex flex-col justify-center gap-[2px] min-w-[104px] py-[8px] px-[18px] border-r border-r-sg-line-soft last:[border-right:none] lte-640:min-w-0 lte-640:px-[12px] lte-640:nth-[3n]:[border-right:none]';
const statLabel = 'text-[0.78rem] text-sg-muted whitespace-nowrap lte-640:overflow-hidden lte-640:text-ellipsis';
const statValue = 'inline-flex items-center gap-[8px] text-[1.6rem] font-semibold leading-[1.15] tracking-[-0.02em] tabular-nums lte-640:text-[1.3rem]';

const TABS: { id: SignalTab; label: string; Icon: LucideIcon; group: TabGroup }[] = [
    { id: 'LOG', label: 'Signal log', Icon: ClipboardList, group: 'activity' },
    { id: 'LIVE', label: 'Live orders', Icon: CircleDot, group: 'activity' },
    { id: 'EVENT_PRICE', label: 'Trade events', Icon: BarChart3, group: 'activity' },
    { id: 'DAILY', label: 'Today', Icon: CalendarDays, group: 'performance' },
    { id: 'PNL', label: 'P&L', Icon: TrendingUp, group: 'performance' },
    { id: 'ACCOUNT', label: 'Account', Icon: User, group: 'system' },
    { id: 'SESSION', label: 'Session health', Icon: Activity, group: 'system' },
    { id: 'TELEGRAM', label: 'Telegram', Icon: Send, group: 'system' },
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

            <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as SignalTab)}>
            <TabsList variant="line" activateOnFocus render={<nav />} aria-label="Signals views">
                {TAB_GROUPS.map((group, gi) => (
                    <Fragment key={group}>
                        {gi > 0 && <Separator variant="group" orientation="vertical" aria-hidden="true" />}
                        <div className="flex gap-[2px] shrink-0">
                            {TABS.filter(t => t.group === group).map(({ id, label, Icon }) => (
                                <TabsTrigger key={id} value={id} id={`signals-tab-${id}`}>
                                    <Icon size={15} /> {label}
                                    {id === 'LIVE' && openOrders.length > 0 && (
                                        <Badge variant="count-tab">{openOrders.length}</Badge>
                                    )}
                                </TabsTrigger>
                            ))}
                        </div>
                    </Fragment>
                ))}
            </TabsList>

            <TabsContent
                value={activeTab}
                id="signals-panel"
                render={<section />}
                className="flex flex-col gap-[12px] min-w-0 p-[14px] bg-sg-surface border border-solid border-sg-line rounded-panel lte-640:p-[10px]"
            >
            {(activeTab === 'LOG' || activeTab === 'LIVE' || activeTab === 'EVENT_PRICE') && (
                <div className="flex items-center gap-[8px] flex-wrap">
                    <NativeSelect variant="filter" value={stratFilter} onChange={e => setStratFilter(e.target.value)} aria-label="Strategy">
                        <NativeSelectOption value="ALL">All strategies</NativeSelectOption>
                        {strategyNames.map(name => (
                            <NativeSelectOption key={name} value={name}>{name}</NativeSelectOption>
                        ))}
                    </NativeSelect>
                    {activeTab === 'LOG' && (
                        <>
                            <NativeSelect variant="filter" value={actionFilter} onChange={e => setActionFilter(e.target.value)} aria-label="Action">
                                <NativeSelectOption value="ALL">All actions</NativeSelectOption>
                                <NativeSelectOption value="BUY">Entries (BUY)</NativeSelectOption>
                                <NativeSelectOption value="EXIT">Exits (EXIT)</NativeSelectOption>
                                <NativeSelectOption value="WATCH">Exit watch (WATCH_*)</NativeSelectOption>
                            </NativeSelect>
                            <Input
                                variant="filter"
                                className="min-w-[200px]"
                                type="search"
                                placeholder="Filter by token, e.g. NFO:43521"
                                aria-label="Filter by token"
                                value={tokenFilter}
                                onChange={e => setTokenFilter(e.target.value)}
                            />
                            {filtersActive && (
                                <Button
                                    variant="link"
                                    onClick={() => { setStratFilter('ALL'); setActionFilter('ALL'); setTokenFilter(''); }}
                                >
                                    Clear filters
                                </Button>
                            )}
                        </>
                    )}
                </div>
            )}

            {/* Tab Content */}
            {activeTab === 'LOG' && (
                <>
                    {loading ? (
                        <Empty>
                            <p>Loading signals…</p>
                        </Empty>
                    ) : filtered.length === 0 ? (
                        <Empty>
                            <Zap size={48} strokeWidth={1.5} />
                            <p>
                                {filtersActive
                                    ? 'No signals match these filters. Clear filters to see all signals.'
                                    : 'No signals yet. They appear here as soon as a strategy triggers.'}
                            </p>
                        </Empty>
                    ) : (
                        <div className={dayList}>
                            {logGroups.map((group) => {
                                const expanded = !!expandedLogDates[group.date];

                                return (
                                    <Collapsible
                                        key={group.date}
                                        open={expanded}
                                        onOpenChange={() => setExpandedLogDates(prev => ({ ...prev, [group.date]: !prev[group.date] }))}
                                        render={<Card render={<section />} />}
                                    >
                                        <CollapsibleTrigger className={dayHead}>
                                            <span className={dayLeft}>
                                                {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                                                <span className={dayDate}>{formatDateKey(group.date)}</span>
                                            </span>
                                            <span className={dayRight}>
                                                <span className={dayMeta}>{group.signals.length} signals</span>
                                                <span className={dayMeta}>{group.buys} entries · {group.exits} exits</span>
                                            </span>
                                        </CollapsibleTrigger>

                                        <CollapsibleContent className={dayBody}>
                                                <div className={tableWrap({ dayBody: true, fullHeight: true })}>
                                                    <Table>
                                                        <TableHeader>
                                                            <TableRow>
                                                                <TableHead style={{ width: '130px' }}>Time</TableHead>
                                                                <TableHead style={{ width: '76px' }}>Action</TableHead>
                                                                <TableHead style={{ width: '70px' }}>Side</TableHead>
                                                                <TableHead style={{ width: '112px' }}>Market</TableHead>
                                                                <TableHead style={{ width: '130px' }}>Strategy</TableHead>
                                                                <TableHead style={{ width: '104px' }}>Mode</TableHead>
                                                                <TableHead style={{ width: '140px' }}>Instrument</TableHead>
                                                                <TableHead numeric style={{ width: '110px' }}>Price</TableHead>
                                                                <TableHead>Reason</TableHead>
                                                            </TableRow>
                                                        </TableHeader>
                                                        <TableBody>
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
                                                        </TableBody>
                                                    </Table>
                                                </div>
                                        </CollapsibleContent>
                                    </Collapsible>
                                );
                            })}
                        </div>
                    )}

                    <div className="flex items-center gap-[8px] pt-[2px]">
                        <span className="mr-auto text-sg-muted text-[0.82rem] tabular-nums">
                            {filtered.length} signals
                            <span className="text-sg-faint"> over {logGroups.length} day{logGroups.length === 1 ? '' : 's'}</span>
                        </span>
                        <Button
                            variant="outline"
                            onClick={() => setExpandedLogDates(
                                logGroups.reduce<Record<string, boolean>>((acc, g) => {
                                    acc[g.date] = true;
                                    return acc;
                                }, {}),
                            )}
                            disabled={logGroups.length === 0 || logGroups.every(g => expandedLogDates[g.date])}
                        >
                            Expand all
                        </Button>
                        <Button
                            variant="outline"
                            onClick={() => setExpandedLogDates({})}
                            disabled={Object.keys(expandedLogDates).length === 0}
                        >
                            Collapse all
                        </Button>
                    </div>
                </>
            )}

            {activeTab === 'LIVE' && <LiveOrdersTab orders={openOrders} />}

            {activeTab === 'EVENT_PRICE' && <EventPriceTab strategyFilter={stratFilter} />}

            {activeTab === 'DAILY' && <DailyAnalyticsTab />}

            {activeTab === 'PNL' && <PnLSummaryTab />}

            {activeTab === 'ACCOUNT' && <AccountTab />}

            {activeTab === 'SESSION' && <SessionHealthTab />}

            {activeTab === 'TELEGRAM' && <TelegramPanel />}

            {activeTab === 'SETTINGS' && <TradingSettingsTab />}
            </TabsContent>
            </Tabs>
        </div>
    );
}
