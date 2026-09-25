import { useCallback, useEffect, useState } from 'react';
import { AlertTriangle, CheckCircle, Octagon, RefreshCw } from 'lucide-react';
import { fetchTradingConfig, saveTradingConfig, type StoredTradingConfig } from '../../services/api';

function nowIST(): string {
    return new Date().toLocaleTimeString('en-IN', { timeZone: 'Asia/Kolkata', hour12: false });
}

/**
 * Dashboard trading controls.
 *
 * stratengine (internal/stratengine/controls.go) applies only the kill switch
 * from POST /api/trading/config. Live orders, sizing, targets and stops are
 * env-only, so they are listed read-only here rather than offered as inputs
 * that would be saved and silently ignored.
 */
export function TradingSettingsTab() {
    const [config, setConfig] = useState<StoredTradingConfig | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [confirmResume, setConfirmResume] = useState(false);
    const [error, setError] = useState('');
    const [savedAt, setSavedAt] = useState('');

    const load = useCallback(async () => {
        setLoading(true);
        setError('');
        try {
            setConfig(await fetchTradingConfig());
        } catch (err) {
            setError(`Could not load the kill switch state: ${(err as Error).message}`);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const setKillSwitch = async (on: boolean) => {
        if (!config) return;
        setSaving(true);
        setError('');
        setConfirmResume(false);
        const next = { ...config, killSwitch: on };
        try {
            await saveTradingConfig(next);
            setConfig(next);
            setSavedAt(nowIST());
        } catch (err) {
            setError(`Kill switch not changed: ${(err as Error).message}`);
        } finally {
            setSaving(false);
        }
    };

    const onToggle = () => {
        if (!config) return;
        if (config.killSwitch) {
            // Re-enabling entries is the risky direction: ask first.
            setConfirmResume(true);
        } else {
            void setKillSwitch(true);
        }
    };

    if (loading) {
        return (
            <div className="signals-empty">
                <RefreshCw size={32} className="spin" strokeWidth={1.5} />
                <p>Loading settings…</p>
            </div>
        );
    }

    if (!config) {
        return (
            <div className="signals-empty">
                <AlertTriangle size={40} strokeWidth={1.5} />
                <p className="error-text">{error}</p>
                <button className="daily-refresh-btn" onClick={() => void load()}>
                    <RefreshCw size={14} /> Retry
                </button>
            </div>
        );
    }

    const blocked = config.killSwitch;

    return (
        <div className="settings-wrap">
            <section className={`settings-card${blocked ? ' blocked' : ''}`} aria-labelledby="kill-switch-title">
                <div className="settings-card-head">
                    <div>
                        <h3 id="kill-switch-title" className="settings-card-title">Entry kill switch</h3>
                        <p className="settings-card-desc">
                            Stops the strategy engine from opening new positions. Exits, stop losses and
                            end-of-day square-off keep running, so open positions can always close.
                        </p>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        className="switch"
                        aria-checked={blocked}
                        aria-labelledby="kill-switch-title"
                        onClick={onToggle}
                        disabled={saving || confirmResume}
                    />
                </div>

                <div className={`settings-state ${blocked ? 'blocked' : 'ok'}`} role="status">
                    {blocked
                        ? <><Octagon size={16} /> New entries blocked. Open positions still exit normally.</>
                        : <><CheckCircle size={16} /> New entries allowed.</>}
                </div>

                {confirmResume && (
                    <div className="settings-confirm">
                        <p>Allow the strategy engine to open new positions again?</p>
                        <button className="settings-btn" onClick={() => setConfirmResume(false)} disabled={saving}>
                            Keep blocked
                        </button>
                        <button className="settings-btn primary" onClick={() => void setKillSwitch(false)} disabled={saving}>
                            {saving ? 'Allowing…' : 'Allow entries'}
                        </button>
                    </div>
                )}

                {error && (
                    <div className="settings-message error" role="alert">
                        <AlertTriangle size={14} /> {error}
                    </div>
                )}
                {!error && savedAt && (
                    <div className="settings-message success">
                        <CheckCircle size={14} /> Saved at {savedAt} IST
                    </div>
                )}

                <p className="settings-note">
                    If <code>STRAT_KILL_SWITCH=true</code> is set on the server, entries stay blocked
                    whatever this switch shows.
                </p>
            </section>

            <section className="settings-card" aria-labelledby="server-settings-title">
                <h3 id="server-settings-title" className="settings-card-title">Set on the server</h3>
                <p className="settings-card-desc">
                    These can't be changed from the dashboard. Edit the stratengine env file and
                    restart the service.
                </p>
                <ul className="settings-list">
                    <li><span>Live or paper orders</span><code>STRAT_LIVE_ORDERS</code></li>
                    <li><span>Quantity (lots)</span><code>STRAT_QTY</code></li>
                    <li><span>Active strategy</span><code>STRAT_ACTIVE_STRATEGY</code></li>
                    <li><span>End-of-day exit time</span><code>STRAT_EOD_EXIT_TIME</code></li>
                    <li><span>Targets and stop losses</span><span>Defined in strategy code</span></li>
                </ul>
            </section>
        </div>
    );
}
