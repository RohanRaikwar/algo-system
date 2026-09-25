import { useEffect, useState } from 'react';
import { Activity, CheckCircle, AlertTriangle, XCircle, RefreshCw, Clock, Shield } from 'lucide-react';
import { fetchAllSessionsHealth, type AllSessionsHealth, type SessionHealth } from '../../services/api';
import './signals.css';

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
            <div className="signals-empty">
                <RefreshCw size={48} className="spin" strokeWidth={1.5} />
                <p>Loading session health…</p>
            </div>
        );
    }

    if (error || !health) {
        return (
            <div className="signals-empty">
                <XCircle size={48} strokeWidth={1.5} />
                <p className="error-text">{error || 'No session data returned by the gateway.'}</p>
            </div>
        );
    }

    const renderSessionCard = (session: SessionHealth) => {
        const isHealthy = session.healthy === true;
        const isConfigured = session.status !== 'not_configured' && session.status !== 'unknown';
        const statusClass = isHealthy ? 'healthy' : isConfigured ? 'unhealthy' : 'unknown';

        return (
            <div className={`daily-day-card session-health-card ${statusClass}`}>
                {/* Header */}
                <div className="session-health-header">
                    <div className="session-health-title">
                        <Activity size={18} />
                        <span className="session-service-name">{session.service}</span>
                    </div>
                    <div className={`session-status-badge ${statusClass}`}>
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
                <div className="session-purpose-text">{session.purpose}</div>

                {/* Message (if any) */}
                {session.message && (
                    <div className="session-warning-message">
                        <AlertTriangle size={14} />
                        {session.message}
                    </div>
                )}

                {/* Details Grid */}
                {isConfigured && session.healthy !== undefined && (
                    <div className="daily-summary-grid session-details">
                        <div className="daily-summary-card">
                            <div className="daily-summary-label">Session valid</div>
                            <div className={`daily-summary-value ${session.valid ? 'price-up' : 'price-down'}`}>
                                {session.valid ? 'Yes' : 'No'}
                            </div>
                        </div>

                        <div className="daily-summary-card">
                            <div className="daily-summary-label">
                                <Clock size={12} />
                                Session age
                            </div>
                            <div className="daily-summary-value sm">
                                {session.age_minutes?.toFixed(1)} min
                            </div>
                        </div>

                        <div className="daily-summary-card">
                            <div className="daily-summary-label">
                                <RefreshCw size={12} />
                                Next refresh
                            </div>
                            <div className="daily-summary-value sm">
                                {session.time_until_refresh || 'N/A'}
                            </div>
                        </div>

                        <div className="daily-summary-card">
                            <div className="daily-summary-label">
                                <Shield size={12} />
                                Circuit breaker
                            </div>
                            <div className={`daily-summary-value ${session.circuit_breaker_open ? 'price-down' : 'price-up'}`}>
                                {session.circuit_breaker_open ? 'OPEN' : 'Closed'}
                            </div>
                        </div>

                        <div className="daily-summary-card">
                            <div className="daily-summary-label">Total refreshes</div>
                            <div className="daily-summary-value">
                                {session.total_refreshes || 0}
                            </div>
                        </div>

                        <div className="daily-summary-card">
                            <div className="daily-summary-label">Success rate</div>
                            <div className="daily-summary-value price-up">
                                {session.total_refreshes && session.total_refreshes > 0 ? 
                                    `${((session.successful_refreshes || 0) / session.total_refreshes * 100).toFixed(1)}%` : 
                                    'N/A'}
                            </div>
                        </div>

                        {session.last_refresh && (
                            <div className="daily-summary-card span-2">
                                <div className="daily-summary-label">Last refresh</div>
                                <div className="daily-summary-value sm">
                                    {new Date(session.last_refresh).toLocaleString('en-IN', {
                                        hour: '2-digit',
                                        minute: '2-digit',
                                        second: '2-digit',
                                        hour12: false,
                                        timeZone: 'Asia/Kolkata'
                                    })}
                                </div>
                            </div>
                        )}
                    </div>
                )}
            </div>
        );
    };

    return (
        <div className="session-health-wrap">
            {/* Overall Status Banner */}
            <div className={`session-overall-banner ${health.overall.all_healthy ? 'banner-healthy' : 'banner-unhealthy'}`}>
                <div className="banner-left">
                    {health.overall.all_healthy ? (
                        <CheckCircle size={24} />
                    ) : (
                        <AlertTriangle size={24} />
                    )}
                    <div>
                        <div className="banner-title">
                            {health.overall.all_healthy ? 'All sessions healthy' : 'Some sessions need attention'}
                        </div>
                        <div className="banner-subtitle">
                            {health.overall.healthy_sessions} of {health.overall.total_sessions} sessions healthy
                        </div>
                    </div>
                </div>
                <div className="banner-right">
                    <RefreshCw size={14} />
                    Refreshes every 30s
                </div>
            </div>

            {/* Session Cards */}
            <div className="session-cards-grid">
                {renderSessionCard(health.sessions.api_gateway)}
                {renderSessionCard(health.sessions.stratengine)}
            </div>

            {/* Footer */}
            <div className="session-health-footer">
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
