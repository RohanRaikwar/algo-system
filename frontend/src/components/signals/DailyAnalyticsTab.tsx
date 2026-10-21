import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CalendarDays, ChevronDown, ChevronRight, RefreshCw } from 'lucide-react';
import {
    fetchDailyOrders,
    fetchDailyPnL,
    type DailyOrdersGroup,
    type DailyPnLItem,
} from '../../services/api';
import { useSignalStore } from '../../store/useSignalStore';
import { computeDailyWindowStats, createOrdersByDateMap } from './dailyAnalytics';
import { cn } from '@/lib/utils';
import { actionBadge, cell, ctrlBtn, dailyTitle, dayBody, dayCard, dayDate, dayHead, dayLeft, dayList, dayMeta, dayPnl, dayRight, emptyState, pnlTone, rowMono, sectionHeader, slKind, spin, summaryCard, summaryGrid, summaryLabel, summaryValue, table, tableWrap, tabStack, th, thNum } from './classes';

const DEFAULT_DAYS = 30;
const IST_DATE_FORMATTER = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Kolkata',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
});

function formatINR(paise: number): string {
    const rupees = paise / 100;
    return rupees.toLocaleString('en-IN', {
        style: 'currency',
        currency: 'INR',
        minimumFractionDigits: 2,
    });
}

function formatDayDate(dateKey: string): string {
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

function formatTime(ts: string): string {
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

function todayISTKey(): string {
    return IST_DATE_FORMATTER.format(new Date());
}

function istDateKeyFromTimestamp(ts: string): string {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '';
    return IST_DATE_FORMATTER.format(d);
}

export function DailyAnalyticsTab() {
    const [loading, setLoading] = useState(true);
    const [pnlRows, setPnlRows] = useState<DailyPnLItem[]>([]);
    const [orderRows, setOrderRows] = useState<DailyOrdersGroup[]>([]);
    const [expandedDays, setExpandedDays] = useState<Record<string, boolean>>({});
    const todayKey = todayISTKey();
    const signals = useSignalStore(s => s.signals);

    const refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
    const fetchInFlight = useRef(false);

    const load = useCallback(async (showSpinner = true) => {
        if (fetchInFlight.current) return;
        fetchInFlight.current = true;
        if (showSpinner) setLoading(true);
        try {
            const [pnlRes, orderRes] = await Promise.all([
                fetchDailyPnL(DEFAULT_DAYS),
                fetchDailyOrders(DEFAULT_DAYS),
            ]);
            setPnlRows(Array.isArray(pnlRes.items) ? pnlRes.items : []);
            setOrderRows(Array.isArray(orderRes.items) ? orderRes.items : []);
        } catch {
            setPnlRows([]);
            setOrderRows([]);
        } finally {
            fetchInFlight.current = false;
            if (showSpinner) setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load(true);
    }, [load]);

    useEffect(() => {
        const onWSMessage = (evt: Event) => {
            const e = evt as CustomEvent;
            if (e.detail?.type !== 'signal') return;
            if (refreshTimer.current) clearTimeout(refreshTimer.current);
            refreshTimer.current = setTimeout(() => {
                void load(false);
            }, 500);
        };
        window.addEventListener('ws:message', onWSMessage as EventListener);
        return () => {
            window.removeEventListener('ws:message', onWSMessage as EventListener);
            if (refreshTimer.current) clearTimeout(refreshTimer.current);
        };
    }, [load]);

    const todayPnLRows = useMemo(
        () => pnlRows.filter(row => row.date === todayKey),
        [pnlRows, todayKey],
    );

    const todaySignalCount = useMemo(
        () => signals.filter(sig => {
            const ts = sig.created_at || sig.candle_ts;
            if (!ts) return false;
            return istDateKeyFromTimestamp(ts) === todayKey;
        }).length,
        [signals, todayKey],
    );

    const latestCompletedDay = useMemo(
        () => pnlRows.find(row => row.date !== todayKey) ?? null,
        [pnlRows, todayKey],
    );

    const ordersByDate = useMemo(() => {
        return createOrdersByDateMap(orderRows.filter(row => row.date === todayKey));
    }, [orderRows, todayKey]);

    useEffect(() => {
        if (todayPnLRows.length === 0) {
            setExpandedDays({});
            return;
        }
        setExpandedDays(prev => {
            if (Object.keys(prev).length > 0) return prev;
            return { [todayPnLRows[0].date]: true };
        });
    }, [todayPnLRows]);

    const windowStats = useMemo(() => {
        return computeDailyWindowStats(todayPnLRows);
    }, [todayPnLRows]);

    const toggleDay = (date: string) => {
        setExpandedDays(prev => ({ ...prev, [date]: !prev[date] }));
    };

    if (loading && todayPnLRows.length === 0) {
        return (
            <div className={emptyState}>
                <p>Loading today&apos;s P&amp;L…</p>
            </div>
        );
    }

    if (todayPnLRows.length === 0) {
        const latestPnlClass = pnlTone(latestCompletedDay ? latestCompletedDay.pnl : 0);

        return (
            <div className={tabStack}>
                <div className={sectionHeader}>
                    <div className={dailyTitle}>
                        <CalendarDays size={16} />
                        Today's realized P&amp;L
                    </div>
                    <button
                        className={ctrlBtn}
                        onClick={() => void load(true)}
                        disabled={loading}
                        title="Refresh daily analytics"
                    >
                        <RefreshCw size={14} className={loading ? spin : ''} />
                        Refresh
                    </button>
                </div>
                <div className={cn(emptyState, 'border border-dashed border-sg-line rounded-[12px]')}>
                    <CalendarDays size={48} strokeWidth={1.5} />
                    <p>No completed trades for today ({todayKey}).</p>
                    <p className="text-sg-faint">
                        Today signal events: {todaySignalCount}. Realized P&amp;L counts only entries that have exited.
                    </p>
                    {latestCompletedDay && (
                        <div className="flex gap-[14px] flex-wrap justify-center mt-[4px] py-[8px] px-[14px] bg-sg-raised rounded-ctrl text-[0.84rem] tabular-nums">
                            <span>Latest completed day: {formatDayDate(latestCompletedDay.date)}</span>
                            <span className={latestPnlClass}>{formatINR(latestCompletedDay.pnl)}</span>
                            <span>{latestCompletedDay.trades} trades</span>
                        </div>
                    )}
                </div>
            </div>
        );
    }

    const netClass = pnlTone(windowStats.pnl);

    return (
        <div className={tabStack}>
            <div className={sectionHeader}>
                <div className={dailyTitle}>
                    <CalendarDays size={16} />
                    Today's realized P&amp;L
                </div>
                <button
                    className={ctrlBtn}
                    onClick={() => void load(true)}
                    disabled={loading}
                    title="Refresh daily analytics"
                >
                    <RefreshCw size={14} className={loading ? spin : ''} />
                    Refresh
                </button>
            </div>

            <div className={summaryGrid}>
                <div className={summaryCard}>
                    <div className={summaryLabel}>Date (IST)</div>
                    <div className={summaryValue()}>{formatDayDate(todayKey)}</div>
                </div>
                <div className={summaryCard}>
                    <div className={summaryLabel}>Realized P&amp;L</div>
                    <div className={summaryValue(netClass)}>{formatINR(windowStats.pnl)}</div>
                </div>
                <div className={summaryCard}>
                    <div className={summaryLabel}>Completed trades</div>
                    <div className={summaryValue()}>{windowStats.trades}</div>
                </div>
                <div className={summaryCard}>
                    <div className={summaryLabel}>Win rate</div>
                    <div className={summaryValue()}>{windowStats.winRate.toFixed(1)}%</div>
                </div>
            </div>

            <div className={dayList}>
                {todayPnLRows.map((day) => {
                    const orders = ordersByDate.get(day.date)?.orders ?? [];
                    const expanded = !!expandedDays[day.date];
                    const pnlClass = pnlTone(day.pnl);

                    return (
                        <section key={day.date} className={dayCard}>
                            <button className={dayHead} aria-expanded={expanded} onClick={() => toggleDay(day.date)}>
                                <span className={dayLeft}>
                                    {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                                    <span className={dayDate}>{formatDayDate(day.date)}</span>
                                </span>
                                <span className={dayRight}>
                                    <span className={dayPnl(pnlClass)}>{formatINR(day.pnl)}</span>
                                    <span className={dayMeta}>{day.trades} trades</span>
                                    <span className={dayMeta}>W {day.wins} / L {day.losses}</span>
                                    <span className={dayMeta}>{day.win_rate.toFixed(1)}%</span>
                                </span>
                            </button>

                            {expanded && (
                                <div className={dayBody}>
                                    {orders.length === 0 ? (
                                        <div className="p-[14px] text-sg-muted text-[0.85rem]">No completed trade pairs for this day.</div>
                                    ) : (
                                        <div className={tableWrap({ dayBody: true })}>
                                            <table className={table}>
                                                <thead>
                                                    <tr>
                                                        <th className={th} style={{ width: '140px' }}>Strategy</th>
                                                        <th className={th} style={{ width: '70px' }}>Side</th>
                                                        <th className={th} style={{ width: '130px' }}>Instrument</th>
                                                        <th className={thNum} style={{ width: '60px' }}>Qty</th>
                                                        <th className={thNum} style={{ width: '100px' }}>Entry</th>
                                                        <th className={th} style={{ width: '120px' }}>Entry time</th>
                                                        <th className={thNum} style={{ width: '100px' }}>Exit</th>
                                                        <th className={th} style={{ width: '120px' }}>Exit time</th>
                                                        <th className={thNum} style={{ width: '110px' }}>Realized P&amp;L</th>
                                                    </tr>
                                                </thead>
                                                <tbody>
                                                    {orders.map((order, idx) => {
                                                        const orderClass = pnlTone(order.realized_pnl);
                                                        return (
                                                            <tr key={`${day.date}-${order.strategy}-${order.instrument}-${idx}`} >
                                                                <td className={cell(true, 'strategy')} title={order.exit_reason || undefined}>
                                                                    {order.strategy}
                                                                    {order.real && <span className={slKind('hard')} style={{ marginLeft: 6 }}>real</span>}
                                                                </td>
                                                                <td className={cell(true)}>
                                                                    <span className={actionBadge(order.side === 'PUT' ? 'put' : 'buy')}>
                                                                        {order.side || '—'}
                                                                    </span>
                                                                </td>
                                                                <td className={cell(true)} title={order.instrument}><span className={order.fno_symbol ? rowMono : undefined}>{order.fno_symbol || order.instrument}</span></td>
                                                                <td className={cell(true, 'price')}>{order.qty}</td>
                                                                <td className={cell(true, 'price')}>{formatINR(order.entry_price)}</td>
                                                                <td className={cell(true, 'time')}>{formatTime(order.entry_time)}</td>
                                                                <td className={cell(true, 'price')}>{formatINR(order.exit_price)}</td>
                                                                <td className={cell(true, 'time')}>{formatTime(order.exit_time)}</td>
                                                                <td className={cell(true, 'price', orderClass)}>{formatINR(order.realized_pnl)}</td>
                                                            </tr>
                                                        );
                                                    })}
                                                </tbody>
                                            </table>
                                        </div>
                                    )}
                                </div>
                            )}
                        </section>
                    );
                })}
            </div>
        </div>
    );
}
