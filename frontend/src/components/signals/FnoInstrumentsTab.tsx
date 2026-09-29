import { useEffect } from 'react';
import { useStrikeSelStore } from '../../store/useStrikeSelStore';
import { fetchStrikeSel } from '../../services/api';
import type { SRContract, SRRejects, SRSide, StrikeSelView, PickerDecision, PickerView } from '../../types/strikesel';
import { Target, TrendingUp, TrendingDown, Clock, SlidersHorizontal, History } from 'lucide-react';

/** Rupees (the chain's unit) to a display string. */
function fmtRupees(v: number): string {
    if (!v || v <= 0) return '—';
    return '₹' + v.toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** ISO timestamp to IST HH:MM:SS. */
function fmtTime(iso: string): string {
    if (!iso) return '—';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '—';
    return d.toLocaleTimeString('en-IN', { timeZone: 'Asia/Kolkata', hour12: false });
}

function fmtExpiry(ymd: string, dte: number): string {
    const d = new Date(`${ymd}T00:00:00+05:30`);
    if (Number.isNaN(d.getTime())) return ymd;
    const s = d.toLocaleDateString('en-IN', { day: '2-digit', month: 'short', timeZone: 'Asia/Kolkata' });
    return `${s} (${dte}d)`;
}

const num = (v: number, digits: number) => (Number.isFinite(v) ? v.toFixed(digits) : '—');

/** "delta 12, liquidity 3" — only filters that dropped something. */
export function rejectSummary(r: SRRejects): string {
    const parts = (Object.entries(r) as Array<[keyof SRRejects, number]>)
        .filter(([, n]) => n > 0)
        .map(([k, n]) => `${k} ${n}`);
    return parts.length ? parts.join(', ') : 'none';
}

/** The active selection rules, in words. */
export function paramRows(p: StrikeSelView['params']): Array<[string, string]> {
    return [
        ['Delta band', `${num(p.delta_min, 2)} – ${num(p.delta_max, 2)} (closest to ${num((p.delta_min + p.delta_max) / 2, 3)} wins)`],
        ['Max theta', p.max_theta_pct > 0 ? `${num(p.max_theta_pct, 1)}% of premium per day` : 'off'],
        ['Gamma cap', p.max_gamma > 0 ? `${p.max_gamma} within ${p.gamma_dte}d of expiry` : 'off'],
        ['Min liquidity', p.min_liquidity > 0 ? p.min_liquidity.toLocaleString('en-IN') : 'off'],
        ['Max IV', p.max_buy_iv ? `${num(p.max_buy_iv, 1)}%` : 'off'],
        ['Cost rule', p.cost_multiple ? `delta × target ≥ ${p.cost_multiple}× round-trip cost` : 'off'],
        ['Expiry', (p.min_dte ?? 1) > 1 ? `nearest ${p.min_dte}+ days out` : 'nearest after today'],
    ];
}

function ContractRows({ c }: { c: SRContract }) {
    const rows: Array<[string, string, string?]> = [
        ['Symbol', c.symbol || `${c.strike}${c.option}`, 'mono'],
        ['Token', c.token || '—', 'mono'],
        ['Expiry', fmtExpiry(c.expiry, c.dte)],
        ['Premium', fmtRupees(c.premium), 'ltp live'],
        ['Delta', num(c.delta, 3)],
        ['Gamma', num(c.gamma, 5)],
        ['Theta', `${num(c.theta, 2)} /day`],
        ['Vega', num(c.vega, 2)],
        ['IV', `${num(c.iv, 1)}%`],
        ['Liquidity', c.liquidity > 0 ? Math.round(c.liquidity).toLocaleString('en-IN') : '—'],
    ];
    return (
        <>
            {rows.map(([label, value, cls]) => (
                <div key={label} className="fno-inst-row">
                    <span className="fno-inst-label">{label}</span>
                    <span className={`fno-inst-value${cls ? ` ${cls}` : ''}`}>{value}</span>
                </div>
            ))}
        </>
    );
}

const paise = (v?: number) => (v && v > 0 ? `₹${(v / 100).toFixed(2)}` : '—');

/** " · old <symbol(s)> ✓ agree" / "✗ disagree" — what the old (non-picker)
 * path chose for the same entry, blank when there's nothing to compare yet
 * (e.g. "on" mode or no entry signal). */
function oldChoiceSuffix(d: PickerDecision): string {
    const old = d.legs && d.legs.length ? (d.old_legs ?? []).join(', ') : (d.old_symbol ?? '');
    if (!old) return '';
    return ` · old ${old} ${d.agree ? '✓ agree' : '✗ disagree'}`;
}

export function decisionLine(d: PickerDecision): string {
    if (d.legs && d.legs.length) {
        if (d.result !== 'picked') return `refused: ${d.reason ?? ''}${oldChoiceSuffix(d)}`;
        const legs = d.legs.map(l => `${l.leg} ${l.symbol}`).join(', ');
        return `picked ${legs} · credit ${paise(d.credit)}${oldChoiceSuffix(d)}`;
    }
    if (d.result !== 'picked') return `refused: ${d.reason ?? ''}${oldChoiceSuffix(d)}`;
    const score = d.score ? ` · exp ${d.score > 0 ? '+' : ''}${(d.score * 100).toFixed(1)}%` : '';
    return `picked ${d.symbol ?? d.strike} · Δ ${num(d.delta ?? NaN, 2)} · ${paise(d.bid)} / ${paise(d.ask)}${score}${oldChoiceSuffix(d)}`;
}

function PickerCard({ p }: { p: PickerView }) {
    return (
        <div className="fno-atm-card">
            <div className="fno-atm-header">
                <Target size={16} />
                <span>Global option picker · {p.mode}</span>
                <span className="fno-badge resolved">{p.streamed} streamed</span>
            </div>
            <div className="fno-atm-body">
                <div className="fno-atm-stat"><span className="fno-atm-label">Greeks (chain)</span>
                    <span className="fno-atm-value fno-param">{p.chain_error ? `error: ${p.chain_error}` : fmtTime(p.chain_at ?? '')}</span></div>
                <div className="fno-atm-stat"><span className="fno-atm-label">Max spread</span>
                    <span className="fno-atm-value fno-param">{num(p.rules.max_spread_pct, 1)}% of mid</span></div>
                <div className="fno-atm-stat"><span className="fno-atm-label">Max quote age</span>
                    <span className="fno-atm-value fno-param">{p.rules.max_quote_age_s}s</span></div>
                <div className="fno-atm-stat"><span className="fno-atm-label">Max greeks age</span>
                    <span className="fno-atm-value fno-param">{p.rules.max_chain_age_s}s</span></div>
                <div className="fno-atm-stat"><span className="fno-atm-label">Pick by</span>
                    <span className="fno-atm-value fno-param">{p.rules.rank === 'return' ? 'expected return on premium' : 'delta closest to band middle'}</span></div>
            </div>
            <div className="fno-inst-body">
                {(p.decisions ?? []).length === 0 && <div className="fno-inst-row"><span className="fno-na">No entry signal yet</span></div>}
                {(p.decisions ?? []).map(d => (
                    <div key={d.strategy} className="fno-inst-row">
                        <span className="fno-inst-label">{d.strategy} · {fmtTime(d.ts)}</span>
                        <span className="fno-inst-value">{decisionLine(d)}</span>
                    </div>
                ))}
            </div>
        </div>
    );
}

function SideCard({ title, kind, side, chainError }: { title: string; kind: 'call' | 'put'; side?: SRSide; chainError?: string }) {
    const Icon = kind === 'call' ? TrendingUp : TrendingDown;
    return (
        <div className={`fno-instrument-card ${kind}`}>
            <div className="fno-inst-header">
                <Icon size={16} />
                <span>{title}</span>
                {side?.pick && <span className="fno-atm-head-strike">{side.pick.strike}</span>}
            </div>
            <div className="fno-inst-body">
                {side?.pick ? (
                    <ContractRows c={side.pick} />
                ) : (
                    <div className="fno-inst-row">
                        <span className="fno-na">
                            {chainError || side?.error || 'No contract passes the rules right now'}
                        </span>
                    </div>
                )}
                {side && !side.error && (
                    <div className="fno-inst-row">
                        <span className="fno-inst-label">Rejected</span>
                        <span className="fno-inst-value">{rejectSummary(side.rejects)}</span>
                    </div>
                )}
            </div>
        </div>
    );
}

export function FnoInstrumentsTab() {
    const view = useStrikeSelStore(s => s.view);
    const setView = useStrikeSelStore(s => s.setView);

    useEffect(() => {
        fetchStrikeSel().then(v => {
            if (v && v.params) setView(v);
        }).catch(() => { });
    }, [setView]);

    if (!view) {
        return (
            <div className="fno-instruments-wrap">
                <div className="fno-pending">
                    <Target size={48} strokeWidth={1.5} />
                    <p>Waiting for the greeks strike selection…</p>
                    <span className="fno-pending-sub">Updates every 30s during market hours (NIFTY50_SR strike selection and the global option picker)</span>
                </div>
            </div>
        );
    }

    const last = view.last;

    return (
        <div className="fno-instruments-wrap">
            {view.picker && <PickerCard p={view.picker} />}
            <div className="fno-atm-card">
                <div className="fno-atm-header">
                    <SlidersHorizontal size={16} />
                    <span>{view.strategy || 'NIFTY50_SR'} strike selection (greeks)</span>
                    <span className="fno-badge resolved"><Clock size={11} /> {fmtTime(view.updated_at)}</span>
                </div>
                <div className="fno-atm-body">
                    {paramRows(view.params).map(([label, value]) => (
                        <div key={label} className="fno-atm-stat">
                            <span className="fno-atm-label">{label}</span>
                            <span className="fno-atm-value fno-param">{value}</span>
                        </div>
                    ))}
                </div>
            </div>

            <div className="fno-instruments-grid">
                <SideCard title="CALL pick now" kind="call" side={view.call} chainError={view.error} />
                <SideCard title="PUT pick now" kind="put" side={view.put} chainError={view.error} />
            </div>

            <div className="fno-instrument-card">
                <div className="fno-inst-header">
                    <History size={16} />
                    <span>Last picked for an entry</span>
                    {last && <span className="fno-na">{fmtTime(last.ts)} · {last.side} · asked {last.asked_strike}</span>}
                </div>
                <div className="fno-inst-body">
                    {last ? <ContractRows c={last} /> : (
                        <div className="fno-inst-row"><span className="fno-na">No SR entry signal yet</span></div>
                    )}
                </div>
            </div>
        </div>
    );
}
