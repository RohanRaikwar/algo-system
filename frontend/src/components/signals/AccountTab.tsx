import { useCallback, useEffect, useState } from 'react';
import { User, Wallet, RefreshCw, TrendingUp, TrendingDown } from 'lucide-react';
import { fetchAccountBalance, fetchLastOrders, fetchUserProfile } from '../../services/api';
import type { AccountBalance, LastOrder, UserProfile } from '../../services/api';
import { actionBadge, cell, ctrlBtn, dailyTitle, emptyState, errorText, footerNote, sectionHeader, sectionTitleText, spin, statusCompleteBadge, summaryCard, summaryGrid, summaryLabel, summaryValue, table, tableWrap, tabStack, th, thNum } from './classes';

const sectionTitle = `${sectionTitleText} mb-[10px] text-[0.92rem]`;

export function AccountTab() {
    const [loading, setLoading] = useState(true);
    const [balance, setBalance] = useState<AccountBalance | null>(null);
    const [profile, setProfile] = useState<UserProfile | null>(null);
    const [lastOrders, setLastOrders] = useState<LastOrder[]>([]);
    const [error, setError] = useState<string | null>(null);

    const load = useCallback(async () => {
        setLoading(true);
        setError(null);
        try {
            const [balanceRes, ordersRes, profileRes] = await Promise.all([
                fetchAccountBalance(),
                fetchLastOrders(10),
                fetchUserProfile(),
            ]);
            setBalance(balanceRes);
            setLastOrders(ordersRes.orders || []);
            setProfile(profileRes);
        } catch (err) {
            console.error('Failed to load account data:', err);
            setError('Could not load account data from Angel One. Check the Session health tab, then retry.');
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const formatINR = (paise: number): string => {
        const rupees = paise / 100;
        return rupees.toLocaleString('en-IN', {
            style: 'currency',
            currency: 'INR',
            minimumFractionDigits: 2,
        });
    };

    const formatTime = (ts: string): string => {
        try {
            const d = new Date(ts);
            return d.toLocaleTimeString('en-IN', {
                hour: '2-digit',
                minute: '2-digit',
                second: '2-digit',
                hour12: false,
                timeZone: 'Asia/Kolkata',
            });
        } catch {
            return ts;
        }
    };

    if (loading && !balance) {
        return (
            <div className={emptyState}>
                <p>Loading account information…</p>
            </div>
        );
    }

    if (error) {
        return (
            <div className={emptyState}>
                <p className={errorText}>{error}</p>
                <button onClick={() => void load()} className={ctrlBtn}>
                    <RefreshCw size={14} /> Retry
                </button>
            </div>
        );
    }

    const marginUsagePercent = balance 
        ? ((balance.used_margin / balance.total_balance) * 100).toFixed(1)
        : '0';

    return (
        <div className={tabStack}>
            <div className={sectionHeader}>
                <div className={dailyTitle}>
                    <User size={16} />
                    Angel One account
                </div>
                <button
                    className={ctrlBtn}
                    onClick={() => void load()}
                    disabled={loading}
                    title="Refresh account data"
                >
                    <RefreshCw size={14} className={loading ? spin : ''} />
                    Refresh
                </button>
            </div>

            {/* User Profile Section */}
            <div className="account-balance-section">
                <h3 className={sectionTitle}>
                    <User size={16} />
                    Account details
                </h3>
                <div className={summaryGrid}>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>Client ID</div>
                        <div className={summaryValue(undefined, true)}>
                            {profile?.data?.clientcode || '—'}
                        </div>
                    </div>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>Name</div>
                        <div className={summaryValue(undefined, true)}>
                            {profile?.data?.name || '—'}
                        </div>
                    </div>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>Broker</div>
                        <div className={summaryValue(undefined, true)}>
                            {profile?.data?.broker || 'Angel One'}
                        </div>
                    </div>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>Status</div>
                        <div className={summaryValue('text-sg-profit', true)}>
                            {profile ? 'Active' : '—'}
                        </div>
                    </div>
                </div>
            </div>

            {/* Account Balance Section */}
            <div className="account-balance-section">
                <h3 className={sectionTitle}>
                    <Wallet size={16} />
                    Margin and funds
                </h3>
                <div className={summaryGrid}>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>Total balance</div>
                        <div className={summaryValue()}>
                            {balance ? formatINR(balance.total_balance) : '—'}
                        </div>
                    </div>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>
                            <TrendingUp size={14} />
                            Available cash
                        </div>
                        <div className={summaryValue('text-sg-profit')}>
                            {balance ? formatINR(balance.available_balance) : '—'}
                        </div>
                    </div>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>
                            <TrendingDown size={14} />
                            Used margin
                        </div>
                        <div className={summaryValue('text-sg-loss')}>
                            {balance ? formatINR(balance.used_margin) : '—'}
                        </div>
                    </div>
                    <div className={summaryCard}>
                        <div className={summaryLabel}>Margin usage</div>
                        <div className={summaryValue(
                            parseFloat(marginUsagePercent) > 80 ? 'text-sg-loss' :
                                parseFloat(marginUsagePercent) > 60 ? 'text-sg-warn' : 'text-sg-profit',
                        )}>
                            {marginUsagePercent}%
                        </div>
                    </div>
                </div>
                {balance && (
                    <div className={`${footerNote} mt-[8px]`}>
                        Last updated: {new Date(balance.last_updated).toLocaleTimeString('en-IN', {
                            hour: '2-digit',
                            minute: '2-digit',
                            timeZone: 'Asia/Kolkata'
                        })}
                    </div>
                )}
            </div>

            {/* Last Orders Section */}
            <div className="account-orders-section">
                <h3 className={sectionTitle}>Recent orders</h3>
                {lastOrders.length === 0 ? (
                    <div className={emptyState}>
                        <p>No recent orders found.</p>
                    </div>
                ) : (
                    <div className={tableWrap()}>
                        <table className={table}>
                            <thead>
                                <tr>
                                    <th className={th} style={{ width: '120px' }}>Time</th>
                                    <th className={th} style={{ width: '130px' }}>Strategy</th>
                                    <th className={th} style={{ width: '140px' }}>Instrument</th>
                                    <th className={th} style={{ width: '70px' }}>Side</th>
                                    <th className={th} style={{ width: '70px' }}>Action</th>
                                    <th className={thNum} style={{ width: '60px' }}>Qty</th>
                                    <th className={thNum} style={{ width: '110px' }}>Price</th>
                                    <th className={th} style={{ width: '90px' }}>Status</th>
                                </tr>
                            </thead>
                            <tbody>
                                {lastOrders.map((order) => (
                                    <tr key={order.id}>
                                        <td className={cell(true, 'time')}>{formatTime(order.created_at)}</td>
                                        <td className={cell(true, 'strategy')}>{order.strategy}</td>
                                        <td className={cell(true)}>{order.instrument}</td>
                                        <td className={cell(true)}>
                                            <span className={actionBadge(order.side === 'PUT' ? 'put' : 'buy')}>
                                                {order.side}
                                            </span>
                                        </td>
                                        <td className={cell(true)}>
                                            <span className={actionBadge(order.action === 'BUY' ? 'buy' : 'exit')}>
                                                {order.action}
                                            </span>
                                        </td>
                                        <td className={cell(true, 'price')}>{order.qty}</td>
                                        <td className={cell(true, 'price')}>{formatINR(order.price)}</td>
                                        <td className={cell(true)}>
                                            <span className={statusCompleteBadge}>
                                                {order.status}
                                            </span>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>
        </div>
    );
}
