import { useSelectedSRView } from '../../store/useSRStore';
import { rejectLabel } from '../signals/SRStatusCard';
import { KIND_LABEL, REGIME_LABEL, asOf, clock, hhmm, nearestLevels, pnlPaise, points, recentSweep, rupees, sourceLabel, sweepLabel, warnLabel, watching } from './srFormat';
import { DayPosition } from './DayPosition';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { REGIME_TONE } from './srClasses';

const panel = 'shrink-0 border-t border-t-sg-line bg-sg-surface pt-[10px] px-[14px] pb-[8px] text-[13px] text-sg-text-2';
const header = 'flex items-center gap-[8px] flex-wrap mb-[8px]';
const title = 'mt-0 mr-[4px] mb-0 ml-0 text-[13px] font-semibold text-sg-text';
const block = 'min-w-0 py-[8px] px-[10px] rounded-ctrl bg-sg-raised';
const blockTitle = 'mt-0 mx-0 mb-[6px] text-[11px] font-semibold uppercase tracking-[0.06em] text-sg-muted';
const stats = 'flex flex-wrap gap-x-[16px] gap-y-[6px]';
const up = 'text-sg-profit';
const down = 'text-sg-loss';
const on = 'text-sg-profit';
const off = 'text-sg-faint line-through';

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
    return (
        <div className="flex flex-col min-w-[64px]">
            <span className="text-[11px] text-sg-muted">{label}</span>
            <span className="tabular-nums text-sg-text font-medium">{value}</span>
            {sub && <span className="text-[11px] text-sg-faint tabular-nums">{sub}</span>}
        </div>
    );
}

export function SRPanel() {
    const v = useSelectedSRView();
    const last = useLastNiftyPaise(v?.key);

    if (!v) {
        return (
            <section className={panel} aria-label="S/R strategy">
                <div className={header}>
                    <h2 className={title}>S/R strategy</h2>
                    <Badge variant="mobile" className="shrink">Paper</Badge>
                </div>
                <p className="m-0 text-sg-muted">Waiting for the strategy engine…</p>
            </section>
        );
    }

    const price = last ?? v.close ?? null;
    const watch = watching(v, price);
    const inPosition = v.side !== 'NONE';
    const kindLabel = v.kind ? KIND_LABEL[v.kind] ?? v.kind : '';
    const pnlPts = pnlPaise(v, last);
    const updated = asOf(v.ts);
    const { support, resistance } = nearestLevels(v.levels, price);
    const rejectAt = clock(v.last_reject_ts);
    const sweep = recentSweep(v);

    return (
        <section className={panel} aria-label="S/R strategy">
            <div className={header}>
                <h2 className={title}>S/R strategy</h2>
                <Badge variant="mobile" tone={REGIME_TONE[v.regime]} className="shrink" title={`15m ADX ${v.adx.toFixed(1)}`}>
                    {REGIME_LABEL[v.regime] ?? v.regime}
                </Badge>
                <Badge variant="mobile" className="shrink" title="The executor only sends real orders for NIFTY50_FNO">Paper</Badge>
                {sweep && (
                    <Badge variant="mobile" className="shrink text-sg-warn"
                        title={`Liquidity sweep: wick through the level, closed back inside. ${sweep.side === 'HIGH' ? 'CALL' : 'PUT'} entries in the next ${v.sweep_warn_min ?? 5} min are flagged.`}>
                        ⚠ {sweepLabel(sweep)} · {clock(sweep.ts)}
                    </Badge>
                )}
                {updated && <span className="ml-auto text-[11px] text-sg-faint lte-900:ml-0">as of {updated} IST</span>}
            </div>

            <div className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1.1fr)] gap-[10px] lte-900:grid-cols-[1fr]">
                <div className={block}>
                    <h3 className={blockTitle}>Situation</h3>
                    <div className={stats}>
                        <Stat label="15m ADX" value={v.adx.toFixed(1)} />
                        <Stat label="5m RSI" value={v.rsi5 ? v.rsi5.toFixed(0) : '—'} />
                        <Stat label="15m ATR" value={v.atr15 ? points(v.atr15) : '—'} />
                        <Stat label="VWAP" value={rupees(v.vwap)} sub={price && v.vwap ? points(price - v.vwap, true) : undefined} />
                        <Stat label="EMA 9 / 21" value={`${rupees(v.ema_fast)} / ${rupees(v.ema_slow)}`} />
                        <Stat label="Support" value={rupees(support?.price)}
                            sub={support && price ? `${points(price - support.price, true)} · ${sourceLabel(support.source)} ×${support.touches}` : undefined} />
                        <Stat label="Resistance" value={rupees(resistance?.price)}
                            sub={resistance && price ? `${points(resistance.price - price, true)} · ${sourceLabel(resistance.source)} ×${resistance.touches}` : undefined} />
                    </div>
                    <DayPosition v={v} price={price} />
                </div>

                <div className={block}>
                    <h3 className={blockTitle}>Watching</h3>
                    <p className={cn('m-0 leading-[1.4]', watch.kind === 'pending' && 'text-sg-warn')}>{watch.text}</p>
                    {!inPosition && v.last_reject && (
                        <p className="mt-[6px] mx-0 mb-0 text-[11px] text-sg-faint">
                            Last refused{rejectAt ? ` ${rejectAt}` : ''}: {rejectLabel(v.last_reject)}
                        </p>
                    )}
                    {v.sweeps && v.sweeps.length > 0 && (
                        <ul className="mt-[6px] mx-0 mb-0 p-0 list-none text-[11px] text-sg-warn [&_li+li]:mt-[2px]" aria-label="Liquidity sweeps today">
                            {[...v.sweeps].reverse().map(sw => (
                                <li key={`${sw.ts}-${sw.level}`}>⚠ {clock(sw.ts)} {sweepLabel(sw)}</li>
                            ))}
                        </ul>
                    )}
                </div>

                <div className={block}>
                    <h3 className={blockTitle}>Position</h3>
                    {inPosition ? (
                        <>
                            <div className="flex items-center gap-[8px] mb-[6px] flex-wrap">
                                <Badge variant="side" tone={v.side === 'CALL' ? 'profit' : 'loss'} className="px-[7px]">{v.side}</Badge>
                                <span className="text-sg-text font-medium">{kindLabel}</span>
                                {v.strike ? <span className="text-sg-muted tabular-nums">{v.strike} {v.side === 'CALL' ? 'CE' : 'PE'}</span> : null}
                                {pnlPts !== null && (
                                    <span className={cn('ml-auto font-semibold tabular-nums', pnlPts >= 0 ? up : down)}>{points(pnlPts, true)}</span>
                                )}
                            </div>
                            <div className={stats}>
                                <Stat label="Entry" value={rupees(v.entry)} />
                                <Stat label="Stop" value={rupees(v.stop)} sub={last && v.stop ? points(Math.abs(last - v.stop)) : undefined} />
                                <Stat label="Target" value={rupees(v.target)} sub={last && v.target ? points(Math.abs(v.target - last)) : undefined} />
                            </div>
                        </>
                    ) : (
                        <p className="m-0 text-sg-muted">Flat</p>
                    )}
                </div>
            </div>

            <div className="flex flex-wrap gap-x-[16px] gap-y-[4px] mt-[8px] text-[11px] text-sg-faint">
                <span>Trades today {v.trades_today}/{v.max_trades}</span>
                <span className={v.day_pnl_pts > 0 ? up : v.day_pnl_pts < 0 ? down : undefined}>
                    Day {points(v.day_pnl_pts, true)}
                </span>
                {v.consec_losses > 0 && <span>Losses in a row {v.consec_losses}</span>}
                {Object.entries(v.warns ?? {}).map(([w, n]) => (
                    <span key={w} className="text-sg-warn">⚠ {warnLabel(w)} ×{n}</span>
                ))}
                {v.cooldown_left > 0 && <span>Cooldown {v.cooldown_left}m</span>}
                <span>Entries {hhmm(v.entry_from_min)}–{hhmm(v.entry_to_min)} on {v.entry_tf}m closes</span>
                <span>{v.min_confirmations}/4 confirms</span>
                <span className="inline-flex gap-[8px] ml-auto lte-900:ml-0">
                    <span className={v.fade ? on : off}>Fade</span>
                    <span className={v.retest ? on : off}>Retest</span>
                    <span className={v.pullback ? on : off}>Pullback</span>
                </span>
            </div>
        </section>
    );
}
