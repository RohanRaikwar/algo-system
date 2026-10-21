import { useEffect } from 'react';
import { useSelectedStrikeSel, useStrikeSelStore } from '../../store/useStrikeSelStore';
import { SRStatusCard } from './SRStatusCard';
import { useOptionLTPStore, livePremium } from '../../store/useOptionLTPStore';
import { fetchStrikeSel } from '../../services/api';
import type { SRContract, SRRejects, SRSide, StrikeSelView, PickerDecision, PickerView, PickerLivePick } from '../../types/strikesel';
import { Target, TrendingUp, TrendingDown, Clock, SlidersHorizontal, History } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
    fnoAtmBody, fnoAtmHeader, fnoAtmStat, fnoBadge, fnoCard, fnoInstBody, fnoInstHeader, fnoInstRow, fnoLabel, fnoNa,
    fnoParam, fnoValue, fnoValueLtp, fnoValueLtpLive, fnoValueMono,
} from './classes';

/** Value class per contract row kind (symbol/token mono, premium big). */
const VALUE_CLASS: Record<string, string> = {
    mono: fnoValueMono,
    ltp: fnoValueLtp,
    'ltp live': fnoValueLtpLive,
};

const decBadgeBase = 'py-[2px] px-[9px] rounded-pill text-[0.74rem] font-semibold';
const DEC_BADGE_TONE = {
    agree: 'text-sg-profit bg-sg-profit-soft',
    disagree: 'text-sg-loss bg-sg-loss-soft',
    none: 'text-sg-muted bg-sg-raised',
} as const;
const decSymbol = 'font-mono text-[0.82rem] [overflow-wrap:anywhere]';
const decCol = 'flex flex-col gap-[4px] min-w-0 py-[10px] px-[12px]';

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
        ['Theta rule', p.theta_max_gain_pct > 0
            ? `decay over ${p.theta_hold_min ?? DEFAULT_HOLD_MIN}m ≤ ${num(p.theta_max_gain_pct, 0)}% of delta × target (view: ${p.view_target_pts ?? 30} pts)`
            : 'off'],
        ['Gamma cap', p.max_gamma > 0 ? `${p.max_gamma} within ${p.gamma_dte}d of expiry` : 'off'],
        ['Min liquidity', p.min_liquidity > 0 ? p.min_liquidity.toLocaleString('en-IN') : 'off'],
        ['Max IV', p.max_buy_iv ? `${num(p.max_buy_iv, 1)}%` : 'off'],
        ['Cost rule', p.cost_multiple ? `delta × target ≥ ${p.cost_multiple}× round-trip cost` : 'off'],
        ['Expiry', (p.min_dte ?? 1) > 1 ? `nearest ${p.min_dte}+ days out` : 'nearest after today'],
    ];
}

/** Minutes in one session (09:15–15:30): a day's theta is spread over it,
 *  as the picker does (optionpicker.tradingMinutesPerDay). */
const SESSION_MIN = 375;
/** Trades are held 1–5h: the decay ladder shows each hour. */
export const HOLD_LADDER_MIN = [60, 120, 180, 240, 300];
const DEFAULT_HOLD_MIN = 240;

/** Rupees of premium lost to theta (rupees per day) over holdMin minutes. */
export function decayOver(theta: number, holdMin: number): number {
    return Math.abs(theta) * holdMin / SESSION_MIN;
}

/** "₹2.40 · ₹4.80 · ₹7.20 · [₹9.60] · ₹12.00": decay at 1–5h, the hold the
 *  picker prices in brackets. */
export function decayLadder(theta: number, holdMin: number): string {
    if (!Number.isFinite(theta)) return '—';
    return HOLD_LADDER_MIN
        .map(m => {
            const v = `₹${decayOver(theta, m).toFixed(2)}`;
            return m === holdMin ? `[${v}]` : v;
        })
        .join(' · ');
}

export interface ThetaBudget { decay: number; gain: number; pct: number; ok: boolean }

/** The picker's theta rule (optionpicker thetaGainOK): decay over the hold
 *  ≤ maxPct % of |delta| × target. null when the rule is off. */
export function thetaBudget(theta: number, delta: number, holdMin: number, targetPts: number, maxPct: number): ThetaBudget | null {
    if (maxPct <= 0 || holdMin <= 0 || targetPts <= 0 || !Number.isFinite(theta) || !Number.isFinite(delta)) return null;
    const decay = decayOver(theta, holdMin);
    const gain = Math.abs(delta) * targetPts;
    const pct = gain > 0 ? decay * 100 / gain : Infinity;
    return { decay, gain, pct, ok: decay * 100 <= gain * maxPct };
}

/** "₹9.60 / ₹15.00 = 64% (limit 25%)". */
export function thetaBudgetLine(b: ThetaBudget, maxPct: number): string {
    const pct = Number.isFinite(b.pct) ? `${Math.round(b.pct)}%` : '—';
    return `₹${b.decay.toFixed(2)} / ₹${b.gain.toFixed(2)} = ${pct} (limit ${num(maxPct, 0)}%)`;
}

type Params = StrikeSelView['params'];

/** Decay over 1–5h, and for a live pick the theta rule at the view target. */
function ThetaRows({ theta, delta, params, live }: { theta: number; delta: number; params?: Params; live: boolean }) {
    const hold = params?.theta_hold_min ?? DEFAULT_HOLD_MIN;
    const b = live && params
        ? thetaBudget(theta, delta, hold, params.view_target_pts ?? 30, params.theta_max_gain_pct)
        : null;
    return (
        <>
            <div className={fnoInstRow}>
                <span className={fnoLabel}>Decay 1h–5h</span>
                <span className={fnoValue}>{decayLadder(theta, hold)}</span>
            </div>
            {b && params && (
                <div className={fnoInstRow}>
                    <span className={fnoLabel}>Theta vs gain ({hold}m)</span>
                    <span className={fnoValue}>{thetaBudgetLine(b, params.theta_max_gain_pct)}</span>
                </div>
            )}
        </>
    );
}

/** Contract details. live: premium follows the token's ticks (a "now" pick);
 *  otherwise it is the premium when the contract was picked. */
function ContractRows({ c, live = false, params }: { c: SRContract; live?: boolean; params?: Params }) {
    const tick = useOptionLTPStore(s => (live && c.token ? s.ltp[c.token] : undefined));
    const prem = livePremium(tick, c.premium);
    const rows: Array<[string, string, string?]> = [
        ['Symbol', c.symbol || `${c.strike}${c.option}`, 'mono'],
        ['Token', c.token || '—', 'mono'],
        ['Expiry', fmtExpiry(c.expiry, c.dte)],
        ['Premium', fmtRupees(prem.rupees), prem.live ? 'ltp live' : 'ltp'],
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
                <div key={label} className={fnoInstRow}>
                    <span className={fnoLabel}>{label}</span>
                    <span className={cls ? VALUE_CLASS[cls] : fnoValue}>{value}</span>
                </div>
            ))}
            <ThetaRows theta={c.theta} delta={c.delta} params={params} live={live} />
        </>
    );
}

const paise = (v?: number) => (v && v > 0 ? `₹${(v / 100).toFixed(2)}` : '—');

/** " · old <symbol> ✓ agree" / "✗ disagree" — what the old (non-picker)
 * path chose for the same entry, blank when there's nothing to compare yet
 * (e.g. "on" mode or no entry signal). */
function oldChoiceSuffix(d: PickerDecision): string {
    const old = d.old_symbol ?? '';
    if (!old) return '';
    return ` · old ${old} ${d.agree ? '✓ agree' : '✗ disagree'}`;
}

export function decisionLine(d: PickerDecision): string {
    if (d.result !== 'picked') return `refused: ${d.reason ?? ''}${oldChoiceSuffix(d)}`;
    const score = d.score ? ` · exp ${d.score > 0 ? '+' : ''}${(d.score * 100).toFixed(1)}%` : '';
    return `picked ${d.symbol ?? d.strike} · Δ ${num(d.delta ?? NaN, 2)} · ${paise(d.bid)} / ${paise(d.ask)}${score}${oldChoiceSuffix(d)}`;
}

/** Old-vs-new comparison for a decision: nothing to compare in "on" mode or
 * before the old path chose (no old symbol). */
export function comparison(d: PickerDecision): 'agree' | 'disagree' | 'none' {
    if (!d.old_symbol) return 'none';
    return d.agree ? 'agree' : 'disagree';
}

/** Expected return on premium as "+25.4%" / "−3.1%"; blank without a score. */
export function scoreText(score?: number): string {
    if (!score) return '';
    const pct = (Math.abs(score) * 100).toFixed(1);
    return `${score > 0 ? '+' : '−'}${pct}%`;
}

const COMPARISON_LABEL = { agree: 'Agree', disagree: 'Disagree', none: 'No comparison' } as const;

/** Old vs new live pick for one side: 'none' until both pickers have an answer. */
export function sideComparison(side: SRSide): 'agree' | 'disagree' | 'none' {
    if (side.agree === undefined) return 'none';
    return side.agree ? 'agree' : 'disagree';
}

/** "₹170.10 / ₹171.90 · spread 1.1% · 0.8s old" for a live picker pick. */
export function quoteLine(p: PickerLivePick): string {
    return `₹${p.bid.toFixed(2)} / ₹${p.ask.toFixed(2)} · spread ${p.spread_pct.toFixed(1)}% · ${p.quote_age_s.toFixed(1)}s old`;
}

function NewPickRows({ side, params }: { side: SRSide; params?: Params }) {
    const p = side.new_pick;
    const cmp = sideComparison(side);
    return (
        <>
            <div className={cn(fnoInstRow, 'mt-[6px] [border-top:1px_dashed_var(--sg-line)]')}>
                <span className="text-[0.8rem] font-semibold text-sg-text-2">New picker</span>
                <span className={fnoValue}>
                    {p ? `${p.strike} ${p.option}` : 'refused'}
                    {cmp !== 'none' && <span className={cn(decBadgeBase, 'ml-[8px]', DEC_BADGE_TONE[cmp])}>{COMPARISON_LABEL[cmp]}</span>}
                </span>
            </div>
            {p ? (
                <>
                    <div className={fnoInstRow}><span className={fnoLabel}>Bid / ask</span><span className={fnoValue}>{quoteLine(p)}</span></div>
                    <div className={fnoInstRow}><span className={fnoLabel}>Delta · IV</span>
                        <span className={fnoValue}>{p.delta.toFixed(3)} · {p.iv.toFixed(1)}%</span></div>
                    <div className={fnoInstRow}><span className={fnoLabel}>Theta</span>
                        <span className={fnoValue}>{num(p.theta, 2)} /day</span></div>
                    <ThetaRows theta={p.theta} delta={p.delta} params={params} live />
                    {p.score !== 0 && (
                        <div className={fnoInstRow}><span className={fnoLabel}>Expected return</span>
                            <span className={fnoValue}>{scoreText(p.score)}</span></div>
                    )}
                    {p.waived && p.waived.length > 0 && (
                        <div className={fnoInstRow}><span className={fnoLabel}>Rules waived</span>
                            <span className={fnoValue}>{p.waived.join(', ')}</span></div>
                    )}
                </>
            ) : (
                <div className={fnoInstRow}><span className={fnoNa}>{side.new_error || 'No tradable contract'}</span></div>
            )}
            {side.new_rejects && (
                <div className={fnoInstRow}><span className={fnoLabel}>New rejected</span><span className={fnoValue}>{side.new_rejects}</span></div>
            )}
        </>
    );
}

function NewChoice({ d }: { d: PickerDecision }) {
    if (d.result !== 'picked') {
        return <div className="text-[0.8rem] text-sg-loss [overflow-wrap:anywhere]">Refused: {d.reason ?? '—'}</div>;
    }
    const score = scoreText(d.score);
    return (
        <>
            <div className={decSymbol}>{d.symbol ?? d.strike}</div>
            <div className="flex flex-wrap gap-y-[4px] gap-x-[10px] text-[0.8rem] text-sg-text-2 tabular-nums">
                <span>Δ {num(d.delta ?? NaN, 2)}</span>
                <span>{paise(d.bid)} / {paise(d.ask)}</span>
                {score && <span className={(d.score ?? 0) > 0 ? 'text-sg-profit font-semibold' : 'text-sg-loss font-semibold'}>exp {score}</span>}
                {d.waived && d.waived.length > 0 && <span className="text-sg-loss font-semibold">waived {d.waived.join(', ')}</span>}
            </div>
        </>
    );
}

function OldChoice({ d }: { d: PickerDecision }) {
    if (!d.old_symbol) {
        return <div className={fnoNa}>{d.mode === 'on' ? 'Picker decides (on mode)' : 'Not chosen yet'}</div>;
    }
    return <div className={decSymbol}>{d.old_symbol}</div>;
}

function DecisionCard({ d }: { d: PickerDecision }) {
    const cmp = comparison(d);
    return (
        <div className="border border-solid border-sg-line-soft rounded-[10px] bg-sg-surface overflow-hidden" title={decisionLine(d)}>
            <div className="flex items-center gap-[8px] py-[9px] px-[12px] border-b border-b-sg-line-soft">
                <span className="font-semibold">{d.strategy}</span>
                <span className={fnoNa}>{fmtTime(d.ts)}</span>
                <span className={cn(decBadgeBase, 'ml-auto', DEC_BADGE_TONE[cmp])}>{COMPARISON_LABEL[cmp]}</span>
            </div>
            <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
                <div className={decCol}>
                    <div className={fnoLabel}>New picker</div>
                    <NewChoice d={d} />
                </div>
                <div className={cn(decCol, 'border-l border-l-sg-line-soft')}>
                    <div className={fnoLabel}>Old picker</div>
                    <OldChoice d={d} />
                </div>
            </div>
        </div>
    );
}

function PickerCard({ p }: { p: PickerView }) {
    const decisions = p.decisions ?? [];
    return (
        <div className={fnoCard}>
            <div className={fnoAtmHeader}>
                <Target size={16} />
                <span>Global option picker · {p.mode}</span>
                <span className={fnoBadge}>{p.streamed} streamed</span>
            </div>
            <div className={fnoAtmBody}>
                <div className={fnoAtmStat}><span className={fnoLabel}>Greeks (chain)</span>
                    <span className={fnoParam}>{p.chain_error ? `error: ${p.chain_error}` : fmtTime(p.chain_at ?? '')}</span></div>
                <div className={fnoAtmStat}><span className={fnoLabel}>Max spread</span>
                    <span className={fnoParam}>{num(p.rules.max_spread_pct, 1)}% of mid</span></div>
                <div className={fnoAtmStat}><span className={fnoLabel}>Max quote age</span>
                    <span className={fnoParam}>{p.rules.max_quote_age_s}s</span></div>
                <div className={fnoAtmStat}><span className={fnoLabel}>Max greeks age</span>
                    <span className={fnoParam}>{p.rules.max_chain_age_s}s</span></div>
                <div className={fnoAtmStat}><span className={fnoLabel}>Pick by</span>
                    <span className={fnoParam}>{p.rules.rank === 'return' ? 'expected return on premium' : 'delta closest to band middle'}</span></div>
            </div>
            {decisions.length === 0 ? (
                <div className={fnoInstBody}><div className={fnoInstRow}><span className={fnoNa}>No entry signal yet</span></div></div>
            ) : (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(320px,1fr))] gap-[10px] pt-[12px] px-[14px] pb-[14px] border-t border-t-sg-line-soft lte-640:grid-cols-[minmax(0,1fr)] lte-640:p-[10px]">
                    {decisions.map(d => <DecisionCard key={d.strategy} d={d} />)}
                </div>
            )}
        </div>
    );
}

function SideCard({ title, kind, side, chainError, params }: { title: string; kind: 'call' | 'put'; side?: SRSide; chainError?: string; params?: Params }) {
    const Icon = kind === 'call' ? TrendingUp : TrendingDown;
    return (
        <div className={fnoCard}>
            <div className={cn(fnoInstHeader, kind === 'call' ? 'text-sg-profit' : 'text-sg-loss')}>
                <Icon size={16} />
                <span>{title}</span>
                {side?.pick && <span className="ml-auto tabular-nums">{side.pick.strike}</span>}
            </div>
            <div className={fnoInstBody}>
                {side?.pick ? (
                    <ContractRows c={side.pick} live params={params} />
                ) : (
                    <div className={fnoInstRow}>
                        <span className={fnoNa}>
                            {chainError || side?.error || 'No contract passes the rules right now'}
                        </span>
                    </div>
                )}
                {side && !side.error && !chainError && (
                    <div className={fnoInstRow}>
                        <span className={fnoLabel}>Rejected</span>
                        <span className={fnoValue}>{rejectSummary(side.rejects)}</span>
                    </div>
                )}
                {side && (side.new_pick || side.new_error) && <NewPickRows side={side} params={params} />}
            </div>
        </div>
    );
}

export function FnoInstrumentsTab() {
    const view = useSelectedStrikeSel();
    const setView = useStrikeSelStore(s => s.setView);

    useEffect(() => {
        fetchStrikeSel().then(v => {
            if (v && v.params) setView(v);
        }).catch(() => { });
    }, [setView]);

    if (!view) {
        return (
            <div className="flex flex-col gap-[12px]">
                <SRStatusCard />
                <div className="flex flex-col items-center gap-[10px] py-[56px] px-[20px] text-sg-muted text-center [&_svg]:text-sg-faint">
                    <Target size={48} strokeWidth={1.5} />
                    <p>Waiting for the greeks strike selection…</p>
                    <span className="text-sg-faint text-[0.84rem]">Updates every 30s during market hours (NIFTY50_SR strike selection and the global option picker)</span>
                </div>
            </div>
        );
    }

    const last = view.last;

    return (
        <div className="flex flex-col gap-[12px]">
            <SRStatusCard />
            {view.picker && <PickerCard p={view.picker} />}
            <div className={fnoCard}>
                <div className={fnoAtmHeader}>
                    <SlidersHorizontal size={16} />
                    <span>{view.strategy || 'NIFTY50_SR'} strike selection (greeks)</span>
                    <span className={fnoBadge}><Clock size={11} /> {fmtTime(view.updated_at)}</span>
                </div>
                <div className={fnoAtmBody}>
                    {paramRows(view.params).map(([label, value]) => (
                        <div key={label} className={fnoAtmStat}>
                            <span className={fnoLabel}>{label}</span>
                            <span className={fnoParam}>{value}</span>
                        </div>
                    ))}
                </div>
            </div>

            <div className="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-[12px] lte-900:grid-cols-[minmax(0,1fr)]">
                <SideCard title="CALL pick now" kind="call" side={view.call} chainError={view.error} params={view.params} />
                <SideCard title="PUT pick now" kind="put" side={view.put} chainError={view.error} params={view.params} />
            </div>

            <div className={fnoCard}>
                <div className={fnoInstHeader}>
                    <History size={16} />
                    <span>Last picked for an entry</span>
                    {last && <span className="ml-auto text-sg-faint text-[0.8rem] font-medium">{fmtTime(last.ts)} · {last.side} · asked {last.asked_strike}</span>}
                </div>
                <div className={fnoInstBody}>
                    {last ? <ContractRows c={last} params={view.params} /> : (
                        <div className={fnoInstRow}><span className={fnoNa}>No SR entry signal yet</span></div>
                    )}
                </div>
            </div>
        </div>
    );
}
