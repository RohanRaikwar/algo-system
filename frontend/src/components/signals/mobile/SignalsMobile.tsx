import { useMemo, useState } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { CalendarDays, ChevronDown, ChevronLeft, ChevronRight, SlidersHorizontal, TrendingUp, X, Zap, type LucideIcon } from 'lucide-react';
import { PageAppBar } from '../../layout/PageAppBar';
import { DailyAnalyticsTab } from '../DailyAnalyticsTab';
import { PnLSummaryTab } from '../PnLSummaryTab';
import { useDailyOrders } from '../EventPriceTab';
import { formatDateKey, signalKey, type SignalsData } from '../useSignalsData';
import { formatSignedPaise, pnlClass } from '../signalFormat';
import type { SignalRecord } from '../../../types/signal';
import { haptic } from '../../../utils/haptic';
import { SignalCard } from './SignalCard';
import { PositionCard } from './PositionCard';
import { TradeCard } from './TradeCard';
import { SignalDetailSheet } from './SignalDetailSheet';
import { FilterSheet } from './FilterSheet';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Empty as EmptyState } from '@/components/ui/empty';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cardMid, instrument, stack, sub } from './classes';
import { signalsPageBase } from '../classes';

type Tab = 'log' | 'positions' | 'trades' | 'more';
type MoreView = 'today' | 'pnl';

const TABS: { id: Tab; label: string }[] = [
    { id: 'log', label: 'Log' },
    { id: 'positions', label: 'Positions' },
    { id: 'trades', label: 'Trades' },
    { id: 'more', label: 'More' },
];

const MORE: { id: MoreView; label: string; hint: string; Icon: LucideIcon }[] = [
    { id: 'today', label: 'Today', hint: 'Day analytics and trades', Icon: CalendarDays },
    { id: 'pnl', label: 'P&L', hint: 'Realized P&L by strategy', Icon: TrendingUp },
];

/* --bar-h / --seg-h feed the sticky offsets: app bar 48px (+ safe area) →
   segmented row 52px → day headers. */
const page = `${signalsPageBase} gap-0 pb-[12px] [--bar-h:calc(48px_+_env(safe-area-inset-top))] [--seg-h:52px]`;
const todayNum = 'font-semibold tabular-nums';
const dayHead = 'sticky top-[calc(var(--bar-h)_+_var(--seg-h))] z-40 flex items-center gap-[6px] w-[calc(100%_+_24px)] h-[40px] my-0 mx-[-12px] py-0 pr-[16px] pl-[12px] border-0 bg-sg-bg text-sg-text text-[13px] font-semibold text-left cursor-pointer';
const dayChev = 'text-sg-muted shrink-0';
const dayMeta = 'text-sg-muted text-[12px] font-medium tabular-nums';

const ACTION_LABEL: Record<string, string> = { BUY: 'Entries', EXIT: 'Exits', WATCH: 'Exit watch' };

/**
 * Phone Signals screen: today strip, Log / Positions / Trades / More, cards
 * instead of wide tables, details and filters in bottom sheets. The tab and
 * More sub-view live in the URL so Android back and reload work.
 */
export function SignalsMobile({ data }: { data: SignalsData }) {
    const [params, setParams] = useSearchParams();
    const navigate = useNavigate();
    const location = useLocation();
    const tab = (TABS.some(t => t.id === params.get('tab')) ? params.get('tab') : 'log') as Tab;
    const view = MORE.find(m => m.id === params.get('view'))?.id ?? null;

    const [detail, setDetail] = useState<SignalRecord | null>(null);
    const [filtersOpen, setFiltersOpen] = useState(false);
    const trades = useDailyOrders(data.stratFilter);

    const todayTrades = trades.groups.find(g => g.date === data.today.key);
    const todayPnl = todayTrades ? todayTrades.netPnl : null;

    const setTab = (t: Tab) => {
        if (t === tab && !view) return;
        haptic();
        setParams(t === 'log' ? {} : { tab: t }, { replace: true });
        window.scrollTo({ top: 0 });
    };
    const openMore = (v: MoreView) => {
        haptic();
        navigate({ search: `?tab=more&view=${v}` }, { state: { fromMore: true } });
        window.scrollTo({ top: 0 });
    };
    const closeMore = () => {
        if ((location.state as { fromMore?: boolean } | null)?.fromMore) navigate(-1);
        else setParams({ tab: 'more' }, { replace: true });
    };

    const chips = useMemo(() => {
        const out: { key: string; label: string; clear: () => void }[] = [];
        if (data.stratFilter !== 'ALL') out.push({ key: 's', label: data.stratFilter, clear: () => data.setStratFilter('ALL') });
        if (tab === 'log' && data.actionFilter !== 'ALL') out.push({ key: 'a', label: ACTION_LABEL[data.actionFilter] ?? data.actionFilter, clear: () => data.setActionFilter('ALL') });
        if (tab === 'log' && data.tokenFilter) out.push({ key: 't', label: data.tokenFilter, clear: () => data.setTokenFilter('') });
        return out;
    }, [data, tab]);

    if (view) {
        const item = MORE.find(m => m.id === view)!;
        return (
            <div className={page}>
                <header className="sticky top-0 z-[60] flex items-center gap-[4px] h-[calc(48px_+_env(safe-area-inset-top))] mt-[-12px] mx-[-12px] mb-[12px] pt-[env(safe-area-inset-top)] pr-[12px] pb-0 pl-[4px] bg-sg-bg border-b border-b-sg-line-soft">
                    <Button variant="ghost-touch" size="icon-44" tone="accent" onClick={closeMore} aria-label="Back to More">
                        <ChevronLeft size={22} />
                    </Button>
                    <h1 className="m-0 text-[17px] font-semibold">{item.label}</h1>
                </header>
                <div className="min-w-0">
                    {view === 'today' && <DailyAnalyticsTab />}
                    {view === 'pnl' && <PnLSummaryTab />}
                </div>
            </div>
        );
    }

    const detailEntry = detail ? data.entryPriceMap.get(signalKey(detail)) : undefined;
    const showFilter = tab !== 'more';

    return (
        <div className={page}>
            <PageAppBar title="Signals" />

            <div className="flex items-center gap-[14px] min-h-[40px] pt-0 px-[4px] pb-[8px] text-sg-muted text-[13px] whitespace-nowrap overflow-x-auto [scrollbar-width:none]" aria-label="Today">
                <span><b className={cn(todayNum, 'text-sg-text')}>{data.today.entries}</b> entries</span>
                <span><b className={cn(todayNum, 'text-sg-text')}>{data.today.exits}</b> exits</span>
                <button type="button" className="p-0 border-0 [background:none] text-inherit leading-[inherit] cursor-pointer" onClick={() => setTab('positions')}>
                    <b className={cn(todayNum, data.openOrders.length ? 'text-sg-accent' : 'text-sg-text')}>{data.openOrders.length}</b> open
                </button>
                <span className="ml-auto">
                    P&L <b className={cn(todayNum, todayPnl === null ? 'text-sg-text' : pnlClass(todayPnl))}>{todayPnl === null ? '—' : formatSignedPaise(todayPnl)}</b>
                </span>
            </div>

            <div className="sticky top-[var(--bar-h)] z-50 flex items-center gap-[8px] h-[var(--seg-h)] my-0 mx-[-12px] py-0 px-[12px] bg-sg-bg">
                <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
                    <TabsList variant="segment" className="flex-1" aria-label="Signals views">
                        {TABS.map(t => (
                            <TabsTrigger key={t.id} value={t.id}>
                                {t.label}
                                {t.id === 'positions' && data.openOrders.length > 0 && <Badge variant="count-xs">{data.openOrders.length}</Badge>}
                            </TabsTrigger>
                        ))}
                    </TabsList>
                </Tabs>
                {showFilter && (
                    <Button
                        variant="segment-icon"
                        size="icon-42"
                        data-active={chips.length > 0}
                        onClick={() => setFiltersOpen(true)}
                        aria-label={`Filters${chips.length ? `, ${chips.length} active` : ''}`}
                    >
                        <SlidersHorizontal size={18} />
                    </Button>
                )}
            </div>

            {showFilter && chips.length > 0 && (
                <div className="flex flex-wrap gap-[6px] pt-[2px] px-0 pb-[10px]">
                    {chips.map(c => (
                        <Button key={c.key} variant="filter-chip" onClick={c.clear} aria-label={`Remove filter ${c.label}`}>
                            {c.label} <X size={13} />
                        </Button>
                    ))}
                </div>
            )}

            {tab === 'log' && <LogView data={data} onOpen={setDetail} />}

            {tab === 'positions' && (
                data.openOrders.length === 0
                    ? <Empty text="No open positions. New entries show here with live P&L until they exit." />
                    : <div className={stack}>
                        {data.openOrders.map((o, i) => <PositionCard key={`${o.strategy}-${o.instrument}-${i}`} order={o} />)}
                    </div>
            )}

            {tab === 'trades' && <TradesView trades={trades} />}

            {tab === 'more' && (
                <nav className={stack} aria-label="More">
                    {MORE.map(m => (
                        <Card key={m.id} variant="row" tap render={<button type="button" />} onClick={() => openMore(m.id)}>
                            <m.Icon size={20} className="shrink-0 text-sg-accent" />
                            <span className={cardMid}>
                                <span className={instrument}>{m.label}</span>
                                <span className={sub}>{m.hint}</span>
                            </span>
                            <ChevronRight size={18} className="shrink-0 text-sg-faint" />
                        </Card>
                    ))}
                    <p className="py-[8px] px-[4px] text-sg-faint text-[12px]">Account, session health and trading settings are on the Health tab.</p>
                </nav>
            )}

            <SignalDetailSheet signal={detail} entry={detailEntry} onClose={() => setDetail(null)} />
            <FilterSheet open={filtersOpen} onClose={() => setFiltersOpen(false)} data={data} logFilters={tab === 'log'} />
        </div>
    );
}

function Empty({ text }: { text: string }) {
    return (
        <EmptyState variant="mobile">
            <Zap size={36} strokeWidth={1.5} />
            <p>{text}</p>
        </EmptyState>
    );
}

function LogView({ data, onOpen }: { data: SignalsData; onOpen: (s: SignalRecord) => void }) {
    if (data.loading) return <Empty text="Loading signals…" />;
    if (data.filtered.length === 0) {
        return <Empty text={data.filtersActive ? 'No signals match these filters.' : 'No signals yet. They appear here as soon as a strategy triggers.'} />;
    }
    return (
        <div className={stack}>
            {data.logGroups.map(group => {
                const expanded = !!data.expandedLogDates[group.date];
                return (
                    <Collapsible
                        key={group.date}
                        render={<section />}
                        open={expanded}
                        onOpenChange={open => data.setExpandedLogDates(prev => ({ ...prev, [group.date]: open }))}
                    >
                        <CollapsibleTrigger className={dayHead}>
                            {expanded ? <ChevronDown size={16} className={dayChev} /> : <ChevronRight size={16} className={dayChev} />}
                            <span className="flex-1 min-w-0">{formatDateKey(group.date)}</span>
                            <span className={dayMeta}>{group.buys} in · {group.exits} out</span>
                        </CollapsibleTrigger>
                        <CollapsibleContent className={stack}>
                            {group.signals.map(sig => {
                                const k = signalKey(sig);
                                return (
                                    <SignalCard
                                        key={k}
                                        signal={sig}
                                        entryPrice={data.entryPriceMap.get(k)?.price}
                                        isNew={data.newSignalIds.has(k)}
                                        onOpen={() => onOpen(sig)}
                                    />
                                );
                            })}
                        </CollapsibleContent>
                    </Collapsible>
                );
            })}
        </div>
    );
}

function TradesView({ trades }: { trades: ReturnType<typeof useDailyOrders> }) {
    if (trades.loading) return <Empty text="Loading trades…" />;
    if (trades.groups.length === 0) return <Empty text={`No completed trades${trades.activeStrategy ? ` for ${trades.activeStrategy}` : ''}.`} />;
    return (
        <div className={stack}>
            {trades.groups.map(group => {
                const expanded = !!trades.expandedDates[group.date];
                return (
                    <Collapsible
                        key={group.date}
                        render={<section />}
                        open={expanded}
                        onOpenChange={open => trades.setExpandedDates(prev => ({ ...prev, [group.date]: open }))}
                    >
                        <CollapsibleTrigger className={dayHead}>
                            {expanded ? <ChevronDown size={16} className={dayChev} /> : <ChevronRight size={16} className={dayChev} />}
                            <span className="flex-1 min-w-0">{formatDateKey(group.date)}</span>
                            <span className={dayMeta}>
                                {group.orders.length} trades · <b className={pnlClass(group.netPnl)}>{formatSignedPaise(group.netPnl)}</b>
                            </span>
                        </CollapsibleTrigger>
                        <CollapsibleContent className={stack}>
                            {group.orders.map((o, i) => <TradeCard key={`${o.strategy}-${o.exit_time}-${i}`} order={o} />)}
                        </CollapsibleContent>
                    </Collapsible>
                );
            })}
        </div>
    );
}
