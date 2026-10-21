import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CalendarDays, ChevronDown, ChevronRight } from 'lucide-react';
import {
    fetchDailyOrders,
    fetchDailyPnL,
    type DailyOrdersGroup,
    type DailyPnLItem,
} from '../../services/api';
import { useSignalStore } from '../../store/useSignalStore';
import { computeDailyWindowStats, createOrdersByDateMap } from './dailyAnalytics';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Empty } from '@/components/ui/empty';
import { Spinner } from '@/components/ui/spinner';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { actionTone, dailyTitle, dayBody, dayDate, dayHead, dayLeft, dayList, dayMeta, dayPnl, dayRight, pnlTone, pnlToneKey, rowMono, sectionHeader, slKindTone, summaryGrid, summaryLabel, summaryValue, tableWrap, tabStack } from './classes';

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
            <Empty>
                <p>Loading today&apos;s P&amp;L…</p>
            </Empty>
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
                    <Button
                        variant="outline"
                        onClick={() => void load(true)}
                        disabled={loading}
                        title="Refresh daily analytics"
                    >
                        <Spinner size={14} spinning={loading} />
                        Refresh
                    </Button>
                </div>
                <Empty variant="dashed">
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
                </Empty>
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
                <Button
                    variant="outline"
                    onClick={() => void load(true)}
                    disabled={loading}
                    title="Refresh daily analytics"
                >
                    <Spinner size={14} spinning={loading} />
                    Refresh
                </Button>
            </div>

            <div className={summaryGrid}>
                <Card variant="summary">
                    <div className={summaryLabel}>Date (IST)</div>
                    <div className={summaryValue()}>{formatDayDate(todayKey)}</div>
                </Card>
                <Card variant="summary">
                    <div className={summaryLabel}>Realized P&amp;L</div>
                    <div className={summaryValue(netClass)}>{formatINR(windowStats.pnl)}</div>
                </Card>
                <Card variant="summary">
                    <div className={summaryLabel}>Completed trades</div>
                    <div className={summaryValue()}>{windowStats.trades}</div>
                </Card>
                <Card variant="summary">
                    <div className={summaryLabel}>Win rate</div>
                    <div className={summaryValue()}>{windowStats.winRate.toFixed(1)}%</div>
                </Card>
            </div>

            <div className={dayList}>
                {todayPnLRows.map((day) => {
                    const orders = ordersByDate.get(day.date)?.orders ?? [];
                    const expanded = !!expandedDays[day.date];
                    const pnlClass = pnlTone(day.pnl);

                    return (
                        <Collapsible
                            key={day.date}
                            open={expanded}
                            onOpenChange={() => toggleDay(day.date)}
                            render={<Card render={<section />} />}
                        >
                            <CollapsibleTrigger className={dayHead}>
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
                            </CollapsibleTrigger>

                            <CollapsibleContent className={dayBody}>
                                    {orders.length === 0 ? (
                                        <div className="p-[14px] text-sg-muted text-[0.85rem]">No completed trade pairs for this day.</div>
                                    ) : (
                                        <div className={tableWrap({ dayBody: true })}>
                                            <Table>
                                                <TableHeader>
                                                    <TableRow>
                                                        <TableHead style={{ width: '140px' }}>Strategy</TableHead>
                                                        <TableHead style={{ width: '70px' }}>Side</TableHead>
                                                        <TableHead style={{ width: '130px' }}>Instrument</TableHead>
                                                        <TableHead numeric style={{ width: '60px' }}>Qty</TableHead>
                                                        <TableHead numeric style={{ width: '100px' }}>Entry</TableHead>
                                                        <TableHead style={{ width: '120px' }}>Entry time</TableHead>
                                                        <TableHead numeric style={{ width: '100px' }}>Exit</TableHead>
                                                        <TableHead style={{ width: '120px' }}>Exit time</TableHead>
                                                        <TableHead numeric style={{ width: '110px' }}>Realized P&amp;L</TableHead>
                                                    </TableRow>
                                                </TableHeader>
                                                <TableBody>
                                                    {orders.map((order, idx) => {
                                                        const orderTone = pnlToneKey(order.realized_pnl);
                                                        return (
                                                            <TableRow key={`${day.date}-${order.strategy}-${order.instrument}-${idx}`} >
                                                                <TableCell density="compact" kind="strategy" title={order.exit_reason || undefined}>
                                                                    {order.strategy}
                                                                    {order.real && <Badge variant="kind" tone={slKindTone('hard')} style={{ marginLeft: 6 }}>real</Badge>}
                                                                </TableCell>
                                                                <TableCell density="compact">
                                                                    <Badge tone={actionTone(order.side === 'PUT' ? 'put' : 'buy')}>
                                                                        {order.side || '—'}
                                                                    </Badge>
                                                                </TableCell>
                                                                <TableCell density="compact" title={order.instrument}><span className={order.fno_symbol ? rowMono : undefined}>{order.fno_symbol || order.instrument}</span></TableCell>
                                                                <TableCell density="compact" kind="price">{order.qty}</TableCell>
                                                                <TableCell density="compact" kind="price">{formatINR(order.entry_price)}</TableCell>
                                                                <TableCell density="compact" kind="time">{formatTime(order.entry_time)}</TableCell>
                                                                <TableCell density="compact" kind="price">{formatINR(order.exit_price)}</TableCell>
                                                                <TableCell density="compact" kind="time">{formatTime(order.exit_time)}</TableCell>
                                                                <TableCell density="compact" kind="price" tone={orderTone}>{formatINR(order.realized_pnl)}</TableCell>
                                                            </TableRow>
                                                        );
                                                    })}
                                                </TableBody>
                                            </Table>
                                        </div>
                                    )}
                            </CollapsibleContent>
                        </Collapsible>
                    );
                })}
            </div>
        </div>
    );
}
