import { useMemo, useState } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { CalendarDays, ChevronDown, ChevronLeft, ChevronRight, Crosshair, SlidersHorizontal, TrendingUp, X, Zap, type LucideIcon } from 'lucide-react';
import { PageAppBar } from '../../layout/PageAppBar';
import { FnoInstrumentsTab } from '../FnoInstrumentsTab';
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
import '../signals.css';
import styles from './signalsMobile.module.css';

type Tab = 'log' | 'positions' | 'trades' | 'more';
type MoreView = 'fno' | 'today' | 'pnl';

const TABS: { id: Tab; label: string }[] = [
    { id: 'log', label: 'Log' },
    { id: 'positions', label: 'Positions' },
    { id: 'trades', label: 'Trades' },
    { id: 'more', label: 'More' },
];

const MORE: { id: MoreView; label: string; hint: string; Icon: LucideIcon }[] = [
    { id: 'fno', label: 'FNO instruments', hint: 'Strike pick, ATM, live premiums', Icon: Crosshair },
    { id: 'today', label: 'Today', hint: 'Day analytics and trades', Icon: CalendarDays },
    { id: 'pnl', label: 'P&L', hint: 'Realized P&L by strategy', Icon: TrendingUp },
];

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
            <div className={`signals-page ${styles.page}`}>
                <header className={styles.subBar}>
                    <button type="button" className={styles.backBtn} onClick={closeMore} aria-label="Back to More">
                        <ChevronLeft size={22} />
                    </button>
                    <h1 className={styles.subTitle}>{item.label}</h1>
                </header>
                <div className={styles.subBody}>
                    {view === 'fno' && <FnoInstrumentsTab />}
                    {view === 'today' && <DailyAnalyticsTab />}
                    {view === 'pnl' && <PnLSummaryTab />}
                </div>
            </div>
        );
    }

    const detailEntry = detail ? data.entryPriceMap.get(signalKey(detail)) : undefined;
    const showFilter = tab !== 'more';

    return (
        <div className={`signals-page ${styles.page}`}>
            <PageAppBar title="Signals" />

            <div className={styles.today} aria-label="Today">
                <span><b>{data.today.entries}</b> entries</span>
                <span><b>{data.today.exits}</b> exits</span>
                <button type="button" className={styles.todayOpen} onClick={() => setTab('positions')}>
                    <b className={data.openOrders.length ? styles.openLive : undefined}>{data.openOrders.length}</b> open
                </button>
                <span className={styles.todayPnl}>
                    P&L <b className={todayPnl === null ? undefined : pnlClass(todayPnl)}>{todayPnl === null ? '—' : formatSignedPaise(todayPnl)}</b>
                </span>
            </div>

            <div className={styles.segRow}>
                <div className={styles.seg} role="tablist" aria-label="Signals views">
                    {TABS.map(t => (
                        <button
                            key={t.id}
                            type="button"
                            role="tab"
                            aria-selected={tab === t.id}
                            className={`${styles.segBtn}${tab === t.id ? ` ${styles.segActive}` : ''}`}
                            onClick={() => setTab(t.id)}
                        >
                            {t.label}
                            {t.id === 'positions' && data.openOrders.length > 0 && <span className={styles.segCount}>{data.openOrders.length}</span>}
                        </button>
                    ))}
                </div>
                {showFilter && (
                    <button
                        type="button"
                        className={`${styles.filterBtn}${chips.length ? ` ${styles.filterOn}` : ''}`}
                        onClick={() => setFiltersOpen(true)}
                        aria-label={`Filters${chips.length ? `, ${chips.length} active` : ''}`}
                    >
                        <SlidersHorizontal size={18} />
                    </button>
                )}
            </div>

            {showFilter && chips.length > 0 && (
                <div className={styles.chips}>
                    {chips.map(c => (
                        <button key={c.key} type="button" className={styles.chip} onClick={c.clear} aria-label={`Remove filter ${c.label}`}>
                            {c.label} <X size={13} />
                        </button>
                    ))}
                </div>
            )}

            {tab === 'log' && <LogView data={data} onOpen={setDetail} />}

            {tab === 'positions' && (
                data.openOrders.length === 0
                    ? <Empty text="No open positions. New entries show here with live P&L until they exit." />
                    : <div className={styles.list}>
                        {data.openOrders.map((o, i) => <PositionCard key={`${o.strategy}-${o.instrument}-${i}`} order={o} />)}
                    </div>
            )}

            {tab === 'trades' && <TradesView trades={trades} />}

            {tab === 'more' && (
                <nav className={styles.moreList} aria-label="More">
                    {MORE.map(m => (
                        <button key={m.id} type="button" className={styles.moreItem} onClick={() => openMore(m.id)}>
                            <m.Icon size={20} className={styles.moreIcon} />
                            <span className={styles.cardMid}>
                                <span className={styles.instrument}>{m.label}</span>
                                <span className={styles.sub}>{m.hint}</span>
                            </span>
                            <ChevronRight size={18} className={styles.chev} />
                        </button>
                    ))}
                    <p className={styles.moreNote}>Account, session health and trading settings are on the Health tab.</p>
                </nav>
            )}

            <SignalDetailSheet signal={detail} entry={detailEntry} onClose={() => setDetail(null)} />
            <FilterSheet open={filtersOpen} onClose={() => setFiltersOpen(false)} data={data} logFilters={tab === 'log'} />
        </div>
    );
}

function Empty({ text }: { text: string }) {
    return (
        <div className={styles.empty}>
            <Zap size={36} strokeWidth={1.5} />
            <p>{text}</p>
        </div>
    );
}

function LogView({ data, onOpen }: { data: SignalsData; onOpen: (s: SignalRecord) => void }) {
    if (data.loading) return <Empty text="Loading signals…" />;
    if (data.filtered.length === 0) {
        return <Empty text={data.filtersActive ? 'No signals match these filters.' : 'No signals yet. They appear here as soon as a strategy triggers.'} />;
    }
    return (
        <div className={styles.days}>
            {data.logGroups.map(group => {
                const expanded = !!data.expandedLogDates[group.date];
                return (
                    <section key={group.date} className={styles.day}>
                        <button
                            type="button"
                            className={styles.dayHead}
                            aria-expanded={expanded}
                            onClick={() => data.setExpandedLogDates(prev => ({ ...prev, [group.date]: !prev[group.date] }))}
                        >
                            {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                            <span className={styles.dayDate}>{formatDateKey(group.date)}</span>
                            <span className={styles.dayMeta}>{group.buys} in · {group.exits} out</span>
                        </button>
                        {expanded && (
                            <div className={styles.list}>
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
                            </div>
                        )}
                    </section>
                );
            })}
        </div>
    );
}

function TradesView({ trades }: { trades: ReturnType<typeof useDailyOrders> }) {
    if (trades.loading) return <Empty text="Loading trades…" />;
    if (trades.groups.length === 0) return <Empty text={`No completed trades${trades.activeStrategy ? ` for ${trades.activeStrategy}` : ''}.`} />;
    return (
        <div className={styles.days}>
            {trades.groups.map(group => {
                const expanded = !!trades.expandedDates[group.date];
                return (
                    <section key={group.date} className={styles.day}>
                        <button
                            type="button"
                            className={styles.dayHead}
                            aria-expanded={expanded}
                            onClick={() => trades.setExpandedDates(prev => ({ ...prev, [group.date]: !prev[group.date] }))}
                        >
                            {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                            <span className={styles.dayDate}>{formatDateKey(group.date)}</span>
                            <span className={styles.dayMeta}>
                                {group.orders.length} trades · <b className={pnlClass(group.netPnl)}>{formatSignedPaise(group.netPnl)}</b>
                            </span>
                        </button>
                        {expanded && (
                            <div className={styles.list}>
                                {group.orders.map((o, i) => <TradeCard key={`${o.strategy}-${o.exit_time}-${i}`} order={o} />)}
                            </div>
                        )}
                    </section>
                );
            })}
        </div>
    );
}
