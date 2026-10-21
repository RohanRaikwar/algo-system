import { useEffect, useMemo, useState, useCallback } from 'react';
import { Table2, ChevronDown, ChevronRight } from 'lucide-react';
import { fetchDailyOrders } from '../../services/api';
import type { DailyOrdersResponse, DailyCompletedOrder } from '../../services/api';
import { actionBadge, cell, cellSub, dayBody, dayCard, dayDate, dayHead, dayLeft, dayList, dayMeta, dayPnl, dayRight, emptyState, pnlTone, rowMono, table, tableWrap, tabStack, th, thNum } from './classes';

interface EventPriceTabProps {
    /** Strategy filter passed from parent (SignalsPage). When 'ALL', no filter is applied. */
    strategyFilter?: string;
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

function formatDate(ts: string): string {
    try {
        const d = new Date(ts);
        return d.toLocaleDateString('en-IN', {
            day: '2-digit', month: 'short',
            timeZone: 'Asia/Kolkata',
        });
    } catch {
        return '';
    }
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

function getSideBadge(side: string) {
    if (side === 'CALL') return actionBadge('buy');
    if (side === 'PUT') return actionBadge('put');
    return actionBadge('none');
}

function getMoveClass(move: number | null): string {
    if (move === null) return '';
    return pnlTone(move);
}

function formatMove(paise: number): string {
    const rupees = paise / 100;
    return `${rupees > 0 ? '+' : rupees < 0 ? '−' : ''}₹${Math.abs(rupees).toFixed(2)}`;
}

function formatPricePaise(paise: number): string {
    return `₹${(paise / 100).toFixed(2)}`;
}

export interface DateGroup {
    date: string;
    orders: DailyCompletedOrder[];
    netPnl: number;
    totalProfitPts: number;
}

/** Completed trades grouped by IST day (90 days), for the table and the phone cards. */
export function useDailyOrders(strategyFilter?: string) {
    const [data, setData] = useState<DailyOrdersResponse | null>(null);
    const [loading, setLoading] = useState(true);
    const [expandedDates, setExpandedDates] = useState<Record<string, boolean>>({});

    const activeStrategy = strategyFilter && strategyFilter !== 'ALL' ? strategyFilter : undefined;

    const loadData = useCallback(() => {
        setLoading(true);
        fetchDailyOrders(90, activeStrategy)
            .then(resp => {
                setData(resp);
                setLoading(false);
            })
            .catch(() => setLoading(false));
    }, [activeStrategy]);

    useEffect(() => {
        loadData();
    }, [loadData]);

    const groups = useMemo<DateGroup[]>(() => {
        if (!data) return [];
        return data.items.map(item => {
            const totalProfitPts = item.orders.reduce((sum, o) => sum + (o.exit_price - o.entry_price), 0) / 100;
            return {
                date: item.date,
                orders: item.orders,
                netPnl: item.pnl,
                totalProfitPts,
            };
        });
    }, [data]);

    useEffect(() => {
        setExpandedDates(prev => {
            const next: Record<string, boolean> = {};
            for (const g of groups) {
                if (prev[g.date]) next[g.date] = true;
            }
            if (groups.length > 0 && Object.keys(next).length === 0) {
                next[groups[0].date] = true;
            }
            return next;
        });
    }, [groups]);

    const totalTrades = data?.items.reduce((sum, item) => sum + item.trades, 0) ?? 0;

    return { data, loading, groups, expandedDates, setExpandedDates, totalTrades, activeStrategy, reload: loadData };
}

export function EventPriceTab({ strategyFilter }: EventPriceTabProps) {
    const { loading, groups, expandedDates, setExpandedDates, totalTrades, activeStrategy } = useDailyOrders(strategyFilter);

    if (loading) {
        return (
            <div className={emptyState}>
                <p>Loading trade events…</p>
            </div>
        );
    }

    if (groups.length === 0) {
        return (
            <div className={emptyState}>
                <Table2 size={48} strokeWidth={1.5} />
                <p>No completed trade events{activeStrategy ? ` for ${activeStrategy}` : ''}.</p>
            </div>
        );
    }

    return (
        <div className={tabStack}>
            <div className="flex items-center gap-[8px] flex-wrap">
                <span className="text-sg-muted text-[0.85rem]">
                    {totalTrades} trades across {groups.length} day{groups.length === 1 ? '' : 's'}
                </span>
            </div>

            <div className={dayList}>
                {groups.map((group) => {
                    const expanded = !!expandedDates[group.date];
                    const moveClass = getMoveClass(group.netPnl);

                    return (
                        <section key={group.date} className={dayCard}>
                            <button
                                className={dayHead}
                                aria-expanded={expanded}
                                onClick={() => setExpandedDates(prev => ({ ...prev, [group.date]: !prev[group.date] }))}
                            >
                                <span className={dayLeft}>
                                    {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                                    <span className={dayDate}>{formatDateKey(group.date)}</span>
                                </span>
                                <span className={dayRight}>
                                    <span className={dayPnl(moveClass)}>{formatMove(group.netPnl)}</span>
                                    <span className={dayPnl(getMoveClass(group.totalProfitPts), true)}>
                                        {group.totalProfitPts > 0 ? '+' : ''}{group.totalProfitPts.toFixed(1)} pts
                                    </span>
                                    <span className={dayMeta}>{group.orders.length} trades</span>
                                </span>
                            </button>

                            {expanded && (
                                <div className={dayBody}>
                                    <div className={tableWrap({ dayBody: true, fullHeight: true })}>
                                        <table className={table}>
                                            <thead>
                                                <tr>
                                                    <th className={th} style={{ width: '140px' }}>Signal</th>
                                                    <th className={th} style={{ width: '70px' }}>Side</th>
                                                    <th className={th} style={{ width: '140px' }}>Instrument</th>
                                                    <th className={thNum} style={{ width: '100px' }}>Entry</th>
                                                    <th className={th} style={{ width: '120px' }}>Entry time</th>
                                                    <th className={thNum} style={{ width: '100px' }}>Exit</th>
                                                    <th className={th} style={{ width: '120px' }}>Exit time</th>
                                                    <th className={thNum} style={{ width: '100px' }}>P&L</th>
                                                    <th className={thNum} style={{ width: '90px' }}>Points</th>
                                                </tr>
                                            </thead>
                                            <tbody>
                                                {group.orders.map((o, i) => (
                                                    <tr key={`${group.date}-${o.strategy}-${o.instrument}-${o.exit_time}-${i}`} >
                                                        <td className={cell(true, 'strategy')}>{o.strategy}</td>
                                                        <td className={cell(true)}>
                                                            <span className={getSideBadge(o.side)}>{o.side}</span>
                                                        </td>
                                                        <td className={cell(true)} title={o.instrument}><span className={o.fno_symbol ? rowMono : undefined}>{o.fno_symbol || o.instrument}</span></td>
                                                        <td className={cell(true, 'price')}>
                                                            {formatPricePaise(o.entry_price)}
                                                        </td>
                                                        <td className={cell(true, 'time')} title={o.entry_time}>
                                                            {formatTime(o.entry_time)}
                                                            <span className={cellSub}>
                                                                {formatDate(o.entry_time)}
                                                            </span>
                                                        </td>
                                                        <td className={cell(true, 'price')}>
                                                            {formatPricePaise(o.exit_price)}
                                                        </td>
                                                        <td className={cell(true, 'time')} title={o.exit_time}>
                                                            {formatTime(o.exit_time)}
                                                            <span className={cellSub}>
                                                                {formatDate(o.exit_time)}
                                                            </span>
                                                        </td>
                                                        <td className={cell(true, 'price', getMoveClass(o.realized_pnl))}>
                                                            {formatMove(o.realized_pnl)}
                                                        </td>
                                                        <td className={cell(true, 'price', getMoveClass(o.exit_price - o.entry_price))}>
                                                            {((o.exit_price - o.entry_price) / 100).toFixed(1)} pts
                                                        </td>
                                                    </tr>
                                                ))}
                                            </tbody>
                                        </table>
                                    </div>
                                </div>
                            )}
                        </section>
                    );
                })}
            </div>
        </div>
    );
}
