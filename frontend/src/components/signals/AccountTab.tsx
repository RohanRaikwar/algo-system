import { useCallback, useEffect, useState } from 'react';
import { User, Wallet, RefreshCw, TrendingUp, TrendingDown } from 'lucide-react';
import { fetchAccountBalance, fetchLastOrders, fetchUserProfile } from '../../services/api';
import type { AccountBalance, LastOrder, UserProfile } from '../../services/api';

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
            <div className="signals-empty">
                <p>Loading account information…</p>
            </div>
        );
    }

    if (error) {
        return (
            <div className="signals-empty">
                <p className="error-text">{error}</p>
                <button onClick={() => void load()} className="daily-refresh-btn">
                    <RefreshCw size={14} /> Retry
                </button>
            </div>
        );
    }

    const marginUsagePercent = balance 
        ? ((balance.used_margin / balance.total_balance) * 100).toFixed(1)
        : '0';

    return (
        <div className="account-tab">
            <div className="daily-header">
                <div className="daily-title">
                    <User size={16} />
                    Angel One account
                </div>
                <button
                    className="daily-refresh-btn"
                    onClick={() => void load()}
                    disabled={loading}
                    title="Refresh account data"
                >
                    <RefreshCw size={14} className={loading ? 'spin' : ''} />
                    Refresh
                </button>
            </div>

            {/* User Profile Section */}
            <div className="account-balance-section">
                <h3 className="section-title">
                    <User size={16} />
                    Account details
                </h3>
                <div className="daily-summary-grid">
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">Client ID</div>
                        <div className="daily-summary-value sm">
                            {profile?.data?.clientcode || '—'}
                        </div>
                    </div>
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">Name</div>
                        <div className="daily-summary-value sm">
                            {profile?.data?.name || '—'}
                        </div>
                    </div>
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">Broker</div>
                        <div className="daily-summary-value sm">
                            {profile?.data?.broker || 'Angel One'}
                        </div>
                    </div>
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">Status</div>
                        <div className="daily-summary-value sm text-pos">
                            {profile ? 'Active' : '—'}
                        </div>
                    </div>
                </div>
            </div>

            {/* Account Balance Section */}
            <div className="account-balance-section">
                <h3 className="section-title">
                    <Wallet size={16} />
                    Margin and funds
                </h3>
                <div className="daily-summary-grid">
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">Total balance</div>
                        <div className="daily-summary-value">
                            {balance ? formatINR(balance.total_balance) : '—'}
                        </div>
                    </div>
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">
                            <TrendingUp size={14} />
                            Available cash
                        </div>
                        <div className="daily-summary-value price-up">
                            {balance ? formatINR(balance.available_balance) : '—'}
                        </div>
                    </div>
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">
                            <TrendingDown size={14} />
                            Used margin
                        </div>
                        <div className="daily-summary-value price-down">
                            {balance ? formatINR(balance.used_margin) : '—'}
                        </div>
                    </div>
                    <div className="daily-summary-card">
                        <div className="daily-summary-label">Margin usage</div>
                        <div className={`daily-summary-value ${
                            parseFloat(marginUsagePercent) > 80 ? 'text-neg' :
                                parseFloat(marginUsagePercent) > 60 ? 'text-warn' : 'text-pos'
                        }`}>
                            {marginUsagePercent}%
                        </div>
                    </div>
                </div>
                {balance && (
                    <div className="account-updated">
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
                <h3 className="section-title">Recent orders</h3>
                {lastOrders.length === 0 ? (
                    <div className="signals-empty">
                        <p>No recent orders found.</p>
                    </div>
                ) : (
                    <div className="signals-table-wrap">
                        <table className="signals-table compact-table">
                            <thead>
                                <tr>
                                    <th style={{ width: '120px' }}>Time</th>
                                    <th style={{ width: '130px' }}>Strategy</th>
                                    <th style={{ width: '140px' }}>Instrument</th>
                                    <th style={{ width: '70px' }}>Side</th>
                                    <th style={{ width: '70px' }}>Action</th>
                                    <th className="num" style={{ width: '60px' }}>Qty</th>
                                    <th className="num" style={{ width: '110px' }}>Price</th>
                                    <th style={{ width: '90px' }}>Status</th>
                                </tr>
                            </thead>
                            <tbody>
                                {lastOrders.map((order) => (
                                    <tr key={order.id} className="signal-row">
                                        <td className="signal-time">{formatTime(order.created_at)}</td>
                                        <td className="strategy-cell">{order.strategy}</td>
                                        <td>{order.instrument}</td>
                                        <td>
                                            <span className={order.side === 'PUT' ? 'action-badge buy-put' : 'action-badge buy-call'}>
                                                {order.side}
                                            </span>
                                        </td>
                                        <td>
                                            <span className={order.action === 'BUY' ? 'action-badge buy' : 'action-badge exit'}>
                                                {order.action}
                                            </span>
                                        </td>
                                        <td className="price-cell">{order.qty}</td>
                                        <td className="price-cell">{formatINR(order.price)}</td>
                                        <td>
                                            <span className="status-badge status-complete">
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
