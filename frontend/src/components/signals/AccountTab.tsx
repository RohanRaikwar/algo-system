import { useCallback, useEffect, useState } from 'react';
import { User, Wallet, RefreshCw, TrendingUp, TrendingDown } from 'lucide-react';
import { fetchAccountBalance, fetchLastOrders, fetchUserProfile } from '../../services/api';
import type { AccountBalance, LastOrder, UserProfile } from '../../services/api';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Empty } from '@/components/ui/empty';
import { Spinner } from '@/components/ui/spinner';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { actionTone, dailyTitle, errorText, footerNote, sectionHeader, sectionTitleText, summaryGrid, summaryLabel, summaryValue, tableWrap, tabStack } from './classes';

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
            <Empty>
                <p>Loading account information…</p>
            </Empty>
        );
    }

    if (error) {
        return (
            <Empty>
                <p className={errorText}>{error}</p>
                <Button variant="outline" onClick={() => void load()}>
                    <RefreshCw size={14} /> Retry
                </Button>
            </Empty>
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
                <Button
                    variant="outline"
                    onClick={() => void load()}
                    disabled={loading}
                    title="Refresh account data"
                >
                    <Spinner size={14} spinning={loading} />
                    Refresh
                </Button>
            </div>

            {/* User Profile Section */}
            <div className="account-balance-section">
                <h3 className={sectionTitle}>
                    <User size={16} />
                    Account details
                </h3>
                <div className={summaryGrid}>
                    <Card variant="summary">
                        <div className={summaryLabel}>Client ID</div>
                        <div className={summaryValue(undefined, true)}>
                            {profile?.data?.clientcode || '—'}
                        </div>
                    </Card>
                    <Card variant="summary">
                        <div className={summaryLabel}>Name</div>
                        <div className={summaryValue(undefined, true)}>
                            {profile?.data?.name || '—'}
                        </div>
                    </Card>
                    <Card variant="summary">
                        <div className={summaryLabel}>Broker</div>
                        <div className={summaryValue(undefined, true)}>
                            {profile?.data?.broker || 'Angel One'}
                        </div>
                    </Card>
                    <Card variant="summary">
                        <div className={summaryLabel}>Status</div>
                        <div className={summaryValue('text-sg-profit', true)}>
                            {profile ? 'Active' : '—'}
                        </div>
                    </Card>
                </div>
            </div>

            {/* Account Balance Section */}
            <div className="account-balance-section">
                <h3 className={sectionTitle}>
                    <Wallet size={16} />
                    Margin and funds
                </h3>
                <div className={summaryGrid}>
                    <Card variant="summary">
                        <div className={summaryLabel}>Total balance</div>
                        <div className={summaryValue()}>
                            {balance ? formatINR(balance.total_balance) : '—'}
                        </div>
                    </Card>
                    <Card variant="summary">
                        <div className={summaryLabel}>
                            <TrendingUp size={14} />
                            Available cash
                        </div>
                        <div className={summaryValue('text-sg-profit')}>
                            {balance ? formatINR(balance.available_balance) : '—'}
                        </div>
                    </Card>
                    <Card variant="summary">
                        <div className={summaryLabel}>
                            <TrendingDown size={14} />
                            Used margin
                        </div>
                        <div className={summaryValue('text-sg-loss')}>
                            {balance ? formatINR(balance.used_margin) : '—'}
                        </div>
                    </Card>
                    <Card variant="summary">
                        <div className={summaryLabel}>Margin usage</div>
                        <div className={summaryValue(
                            parseFloat(marginUsagePercent) > 80 ? 'text-sg-loss' :
                                parseFloat(marginUsagePercent) > 60 ? 'text-sg-warn' : 'text-sg-profit',
                        )}>
                            {marginUsagePercent}%
                        </div>
                    </Card>
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
                    <Empty>
                        <p>No recent orders found.</p>
                    </Empty>
                ) : (
                    <div className={tableWrap()}>
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead style={{ width: '120px' }}>Time</TableHead>
                                    <TableHead style={{ width: '130px' }}>Strategy</TableHead>
                                    <TableHead style={{ width: '140px' }}>Instrument</TableHead>
                                    <TableHead style={{ width: '70px' }}>Side</TableHead>
                                    <TableHead style={{ width: '70px' }}>Action</TableHead>
                                    <TableHead numeric style={{ width: '60px' }}>Qty</TableHead>
                                    <TableHead numeric style={{ width: '110px' }}>Price</TableHead>
                                    <TableHead style={{ width: '90px' }}>Status</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {lastOrders.map((order) => (
                                    <TableRow key={order.id}>
                                        <TableCell density="compact" kind="time">{formatTime(order.created_at)}</TableCell>
                                        <TableCell density="compact" kind="strategy">{order.strategy}</TableCell>
                                        <TableCell density="compact">{order.instrument}</TableCell>
                                        <TableCell density="compact">
                                            <Badge tone={actionTone(order.side === 'PUT' ? 'put' : 'buy')}>
                                                {order.side}
                                            </Badge>
                                        </TableCell>
                                        <TableCell density="compact">
                                            <Badge tone={actionTone(order.action === 'BUY' ? 'buy' : 'exit')}>
                                                {order.action}
                                            </Badge>
                                        </TableCell>
                                        <TableCell density="compact" kind="price">{order.qty}</TableCell>
                                        <TableCell density="compact" kind="price">{formatINR(order.price)}</TableCell>
                                        <TableCell density="compact">
                                            <Badge tone="profit">
                                                {order.status}
                                            </Badge>
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </div>
                )}
            </div>
        </div>
    );
}
