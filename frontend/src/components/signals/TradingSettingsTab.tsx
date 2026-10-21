import { useCallback, useEffect, useState } from 'react';
import { AlertTriangle, CheckCircle, Octagon, RefreshCw } from 'lucide-react';
import { fetchTradingConfig, saveTradingConfig, type StoredTradingConfig } from '../../services/api';
import { cn } from '@/lib/utils';
import { ctrlBtn, emptyState, errorText, spin } from './classes';

const card = 'p-[18px] bg-sg-bg border border-solid rounded-[12px] lte-640:p-[14px]';
const cardTitle = 'text-[1rem] font-semibold';
const cardDesc = 'mt-[4px] max-w-[60ch] text-sg-muted text-[0.86rem] leading-[1.5]';
const btnBase = 'h-[32px] py-0 px-[12px] rounded-ctrl border border-solid text-[0.84rem] font-medium cursor-pointer disabled:opacity-[0.5] disabled:cursor-default';
const btn = `${btnBase} border-sg-line bg-sg-raised text-sg-text hover:enabled:bg-sg-hover`;
const btnPrimary = `${btnBase} border-transparent bg-sg-accent text-white hover:enabled:bg-sg-accent-hover`;
const code = 'font-mono text-[0.8rem] text-sg-text-2 bg-sg-raised py-[1px] px-[6px] rounded-[5px]';
const listItem = 'flex items-center justify-between gap-[16px] py-[11px] px-0 border-b border-b-sg-line-soft text-[0.88rem] last:[border-bottom:none] [&_span]:text-sg-text-2';
const message = 'flex items-center gap-[8px] mt-[12px] text-[0.84rem]';

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
            <div className={emptyState}>
                <RefreshCw size={32} className={spin} strokeWidth={1.5} />
                <p>Loading settings…</p>
            </div>
        );
    }

    if (!config) {
        return (
            <div className={emptyState}>
                <AlertTriangle size={40} strokeWidth={1.5} />
                <p className={errorText}>{error}</p>
                <button className={ctrlBtn} onClick={() => void load()}>
                    <RefreshCw size={14} /> Retry
                </button>
            </div>
        );
    }

    const blocked = config.killSwitch;

    return (
        <div className="flex flex-col gap-[12px] max-w-[760px]">
            <section className={cn(card, blocked ? 'border-[rgba(240,97,109,0.45)]' : 'border-sg-line')} aria-labelledby="kill-switch-title">
                <div className="flex items-start justify-between gap-[16px]">
                    <div>
                        <h3 id="kill-switch-title" className={cardTitle}>Entry kill switch</h3>
                        <p className={cardDesc}>
                            Stops the strategy engine from opening new positions. Exits, stop losses and
                            end-of-day square-off keep running, so open positions can always close.
                        </p>
                    </div>
                    <button
                        type="button"
                        role="switch"
                        className="relative shrink-0 w-[44px] h-[26px] p-0 [border:none] rounded-pill bg-sg-hover shadow-[inset_0_0_0_1px_var(--sg-line)] cursor-pointer [transition:background_0.18s] after:content-[''] after:absolute after:top-[3px] after:left-[3px] after:w-[20px] after:h-[20px] after:rounded-[50%] after:bg-sg-text-2 after:[transition:transform_0.18s,background_0.18s] aria-checked:bg-sg-loss aria-checked:[box-shadow:none] aria-checked:focus-visible:[box-shadow:none] aria-checked:after:[transform:translateX(18px)] aria-checked:after:bg-white disabled:opacity-[0.5] disabled:cursor-default motion-reduce:after:[transition:none]!"
                        aria-checked={blocked}
                        aria-labelledby="kill-switch-title"
                        onClick={onToggle}
                        disabled={saving || confirmResume}
                    />
                </div>

                <div className={cn('flex items-center gap-[8px] mt-[14px] py-[10px] px-[12px] rounded-ctrl text-[0.88rem] font-medium', blocked ? 'text-sg-loss bg-sg-loss-soft' : 'text-sg-profit bg-sg-profit-soft')} role="status">
                    {blocked
                        ? <><Octagon size={16} /> New entries blocked. Open positions still exit normally.</>
                        : <><CheckCircle size={16} /> New entries allowed.</>}
                </div>

                {confirmResume && (
                    <div className="flex items-center gap-[10px] flex-wrap mt-[12px] p-[12px] border border-solid border-sg-line rounded-ctrl bg-sg-surface text-[0.86rem]">
                        <p className="flex-[1_1_260px] text-sg-text-2">Allow the strategy engine to open new positions again?</p>
                        <button className={btn} onClick={() => setConfirmResume(false)} disabled={saving}>
                            Keep blocked
                        </button>
                        <button className={btnPrimary} onClick={() => void setKillSwitch(false)} disabled={saving}>
                            {saving ? 'Allowing…' : 'Allow entries'}
                        </button>
                    </div>
                )}

                {error && (
                    <div className={cn(message, 'text-sg-loss')} role="alert">
                        <AlertTriangle size={14} /> {error}
                    </div>
                )}
                {!error && savedAt && (
                    <div className={cn(message, 'text-sg-muted')}>
                        <CheckCircle size={14} /> Saved at {savedAt} IST
                    </div>
                )}

                <p className="mt-[10px] text-sg-faint text-[0.8rem]">
                    If <code className={code}>STRAT_KILL_SWITCH=true</code> is set on the server, entries stay blocked
                    whatever this switch shows.
                </p>
            </section>

            <section className={cn(card, 'border-sg-line')} aria-labelledby="server-settings-title">
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
            </section>
        </div>
    );
}
