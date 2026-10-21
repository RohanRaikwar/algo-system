import { useCallback, useEffect, useState } from 'react';
import { AlertTriangle, CheckCircle, Octagon, RefreshCw } from 'lucide-react';
import { fetchTradingConfig, saveTradingConfig, type StoredTradingConfig } from '../../services/api';
import { Alert } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Empty } from '@/components/ui/empty';
import { Spinner } from '@/components/ui/spinner';
import { Switch } from '@/components/ui/switch';
import { errorText } from './classes';

const cardTitle = 'text-[1rem] font-semibold';
const cardDesc = 'mt-[4px] max-w-[60ch] text-sg-muted text-[0.86rem] leading-[1.5]';
const code = 'font-mono text-[0.8rem] text-sg-text-2 bg-sg-raised py-[1px] px-[6px] rounded-[5px]';
const listItem = 'flex items-center justify-between gap-[16px] py-[11px] px-0 border-b border-b-sg-line-soft text-[0.88rem] last:[border-bottom:none] [&_span]:text-sg-text-2';

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
            <Empty>
                <Spinner size={32} strokeWidth={1.5} />
                <p>Loading settings…</p>
            </Empty>
        );
    }

    if (!config) {
        return (
            <Empty>
                <AlertTriangle size={40} strokeWidth={1.5} />
                <p className={errorText}>{error}</p>
                <Button variant="outline" onClick={() => void load()}>
                    <RefreshCw size={14} /> Retry
                </Button>
            </Empty>
        );
    }

    const blocked = config.killSwitch;

    return (
        <div className="flex flex-col gap-[12px] max-w-[760px]">
            <Card variant="settings" tone={blocked ? 'danger' : undefined} render={<section />} aria-labelledby="kill-switch-title">
                <div className="flex items-start justify-between gap-[16px]">
                    <div>
                        <h3 id="kill-switch-title" className={cardTitle}>Entry kill switch</h3>
                        <p className={cardDesc}>
                            Stops the strategy engine from opening new positions. Exits, stop losses and
                            end-of-day square-off keep running, so open positions can always close.
                        </p>
                    </div>
                    <Switch
                        variant="danger"
                        checked={blocked}
                        onCheckedChange={onToggle}
                        aria-labelledby="kill-switch-title"
                        disabled={saving || confirmResume}
                    />
                </div>

                <Alert variant="soft" tone={blocked ? 'loss' : 'profit'} className="mt-[14px]" role="status">
                    {blocked
                        ? <><Octagon size={16} /> New entries blocked. Open positions still exit normally.</>
                        : <><CheckCircle size={16} /> New entries allowed.</>}
                </Alert>

                {confirmResume && (
                    <div className="flex items-center gap-[10px] flex-wrap mt-[12px] p-[12px] border border-solid border-sg-line rounded-ctrl bg-sg-surface text-[0.86rem]">
                        <p className="flex-[1_1_260px] text-sg-text-2">Allow the strategy engine to open new positions again?</p>
                        <Button variant="secondary" size="md" onClick={() => setConfirmResume(false)} disabled={saving}>
                            Keep blocked
                        </Button>
                        <Button variant="default" size="md" className="border border-solid border-transparent" onClick={() => void setKillSwitch(false)} disabled={saving}>
                            {saving ? 'Allowing…' : 'Allow entries'}
                        </Button>
                    </div>
                )}

                {error && (
                    <Alert variant="inline" tone="loss" className="mt-[12px]">
                        <AlertTriangle size={14} /> {error}
                    </Alert>
                )}
                {!error && savedAt && (
                    <Alert variant="inline" tone="muted" className="mt-[12px]" role={undefined}>
                        <CheckCircle size={14} /> Saved at {savedAt} IST
                    </Alert>
                )}

                <p className="mt-[10px] text-sg-faint text-[0.8rem]">
                    If <code className={code}>STRAT_KILL_SWITCH=true</code> is set on the server, entries stay blocked
                    whatever this switch shows.
                </p>
            </Card>

            <Card variant="settings" render={<section />} aria-labelledby="server-settings-title">
                <h3 id="server-settings-title" className={cardTitle}>Set on the server</h3>
                <p className={cardDesc}>
                    These can't be changed from the dashboard. Edit the stratengine env file and
                    restart the service.
                </p>
                <ul className="mt-[12px] list-none">
                    <li className={listItem}><span>Live or paper orders</span><code className={code}>STRAT_LIVE_ORDERS</code></li>
                    <li className={listItem}><span>Quantity (lots)</span><code className={code}>STRAT_QTY</code></li>
                    <li className={listItem}><span>NIFTY50_SR on/off</span><code className={code}>STRAT_SR_ENABLED</code></li>
                    <li className={listItem}><span>End-of-day exit time</span><code className={code}>STRAT_EOD_EXIT_TIME</code></li>
                    <li className={listItem}><span>Targets and stop losses</span><span>Defined in strategy code</span></li>
                </ul>
            </Card>
        </div>
    );
}
