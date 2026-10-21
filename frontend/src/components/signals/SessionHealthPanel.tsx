import { useEffect, useState } from 'react';
import { Activity, CheckCircle, AlertTriangle, XCircle, RefreshCw, Clock, Shield } from 'lucide-react';
import { fetchAllSessionsHealth, type AllSessionsHealth, type SessionHealth } from '../../services/api';
import { cn } from '@/lib/utils';
import { Card } from '@/components/ui/card';
import { Empty } from '@/components/ui/empty';
import { Spinner } from '@/components/ui/spinner';
import { errorText, footerNote, summaryGrid, summaryLabel, summaryValue } from './classes';

const STATUS_TONE: Record<string, string> = {
    healthy: 'text-sg-profit bg-sg-profit-soft',
    unhealthy: 'text-sg-loss bg-sg-loss-soft',
    unknown: 'text-sg-muted bg-sg-raised',
};

export function SessionHealthPanel() {
    const [health, setHealth] = useState<AllSessionsHealth | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        const loadHealth = async () => {
            try {
                const data = await fetchAllSessionsHealth();
                setHealth(data);
                setError(null);
            } catch (err) {
                setError('Could not reach the API gateway for session health. Retrying every 30 seconds.');
                console.error(err);
            } finally {
                setLoading(false);
            }
        };

        loadHealth();
        const interval = setInterval(loadHealth, 30000); // Refresh every 30 seconds

        return () => clearInterval(interval);
    }, []);

    if (loading) {
        return (
            <Empty>
                <Spinner size={48} strokeWidth={1.5} />
                <p>Loading session health…</p>
            </Empty>
        );
    }

    if (error || !health) {
        return (
            <Empty>
                <XCircle size={48} strokeWidth={1.5} />
                <p className={errorText}>{error || 'No session data returned by the gateway.'}</p>
            </Empty>
        );
    }

    const renderSessionCard = (session: SessionHealth) => {
        const isHealthy = session.healthy === true;
        const isConfigured = session.status !== 'not_configured' && session.status !== 'unknown';
        const statusClass = isHealthy ? 'healthy' : isConfigured ? 'unhealthy' : 'unknown';

        return (
            <Card className="p-[14px]">
                {/* Header */}
                <div className="flex items-center justify-between gap-[10px]">
                    <div className="flex items-center gap-[8px] [&_svg]:text-sg-muted">
                        <Activity size={18} />
                        <span className="font-semibold">{session.service}</span>
                    </div>
                    <div className={cn('inline-flex items-center gap-[5px] py-[3px] px-[10px] rounded-pill text-[0.76rem] font-semibold', STATUS_TONE[statusClass])}>
                        {isHealthy ? (
                            <><CheckCircle size={14} /> Healthy</>
                        ) : isConfigured ? (
                            <><AlertTriangle size={14} /> Unhealthy</>
                        ) : (
                            <><XCircle size={14} /> Unknown</>
                        )}
                    </div>
                </div>

                {/* Purpose */}
                <div className="mt-[6px] text-sg-muted text-[0.84rem]">{session.purpose}</div>

                {/* Message (if any) */}
                {session.message && (
                    <div className="flex items-start gap-[8px] mt-[10px] py-[9px] px-[12px] rounded-ctrl bg-sg-warn-soft text-sg-warn text-[0.84rem] [&_svg]:shrink-0 [&_svg]:mt-[2px]">
                        <AlertTriangle size={14} />
                        {session.message}
                    </div>
                )}

                {/* Details Grid */}
                {isConfigured && session.healthy !== undefined && (
                    <div className={cn(summaryGrid, 'mt-[12px]')}>
                        <Card variant="summary-surface">
                            <div className={summaryLabel}>Session valid</div>
                            <div className={summaryValue(session.valid ? 'text-sg-profit' : 'text-sg-loss')}>
                                {session.valid ? 'Yes' : 'No'}
                            </div>
                        </Card>

                        <Card variant="summary-surface">
                            <div className={summaryLabel}>
                                <Clock size={12} />
                                Session age
                            </div>
                            <div className={summaryValue(undefined, true)}>
                                {session.age_minutes?.toFixed(1)} min
                            </div>
                        </Card>

                        <Card variant="summary-surface">
                            <div className={summaryLabel}>
                                <RefreshCw size={12} />
                                Next refresh
                            </div>
                            <div className={summaryValue(undefined, true)}>
                                {session.time_until_refresh || 'N/A'}
                            </div>
                        </Card>

                        <Card variant="summary-surface">
                            <div className={summaryLabel}>
                                <Shield size={12} />
                                Circuit breaker
                            </div>
                            <div className={summaryValue(session.circuit_breaker_open ? 'text-sg-loss' : 'text-sg-profit')}>
                                {session.circuit_breaker_open ? 'OPEN' : 'Closed'}
                            </div>
                        </Card>

                        <Card variant="summary-surface">
                            <div className={summaryLabel}>Total refreshes</div>
                            <div className={summaryValue()}>
                                {session.total_refreshes || 0}
                            </div>
                        </Card>

                        <Card variant="summary-surface">
                            <div className={summaryLabel}>Success rate</div>
                            <div className={summaryValue('text-sg-profit')}>
                                {session.total_refreshes && session.total_refreshes > 0 ? 
                                    `${((session.successful_refreshes || 0) / session.total_refreshes * 100).toFixed(1)}%` : 
                                    'N/A'}
                            </div>
                        </Card>

                        {session.last_refresh && (
                            <Card variant="summary-surface" className="[grid-column:span_2] lte-640:[grid-column:auto]">
                                <div className={summaryLabel}>Last refresh</div>
                                <div className={summaryValue(undefined, true)}>
                                    {new Date(session.last_refresh).toLocaleString('en-IN', {
                                        hour: '2-digit',
                                        minute: '2-digit',
                                        second: '2-digit',
                                        hour12: false,
                                        timeZone: 'Asia/Kolkata'
                                    })}
                                </div>
                            </Card>
                        )}
                    </div>
                )}
            </Card>
        );
    };

    return (
        <div className="flex flex-col gap-[12px]">
            {/* Overall Status Banner */}
            <div className={cn('flex items-center justify-between gap-[12px] py-[14px] px-[16px] border border-solid border-sg-line border-l-[3px] rounded-[12px] bg-sg-bg lte-640:flex-col lte-640:items-start', health.overall.all_healthy ? 'border-l-sg-profit' : 'border-l-sg-loss')}>
                <div className={cn('flex items-center gap-[12px]', health.overall.all_healthy ? '[&>svg]:text-sg-profit' : '[&>svg]:text-sg-loss')}>
                    {health.overall.all_healthy ? (
                        <CheckCircle size={24} />
                    ) : (
                        <AlertTriangle size={24} />
                    )}
                    <div>
                        <div className="font-semibold text-[1rem]">
                            {health.overall.all_healthy ? 'All sessions healthy' : 'Some sessions need attention'}
                        </div>
                        <div className="text-sg-muted text-[0.84rem]">
                            {health.overall.healthy_sessions} of {health.overall.total_sessions} sessions healthy
                        </div>
                    </div>
                </div>
                <div className="flex items-center gap-[6px] text-sg-faint text-[0.8rem] whitespace-nowrap">
                    <RefreshCw size={14} />
                    Refreshes every 30s
                </div>
            </div>

            {/* Session Cards */}
            <div className="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-[12px] lte-900:grid-cols-[minmax(0,1fr)]">
                {renderSessionCard(health.sessions.api_gateway)}
                {renderSessionCard(health.sessions.stratengine)}
            </div>

            {/* Footer */}
            <div className={footerNote}>
                <Clock size={14} />
                Last updated: {new Date(health.timestamp).toLocaleString('en-IN', {
                    hour: '2-digit',
                    minute: '2-digit',
                    second: '2-digit',
                    hour12: false,
                    timeZone: 'Asia/Kolkata'
                })} IST
            </div>
        </div>
    );
}
