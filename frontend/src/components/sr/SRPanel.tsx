import { useSRStore } from '../../store/useSRStore';
import { rejectLabel } from '../signals/SRStatusCard';
import { KIND_LABEL, REGIME_LABEL, asOf, clock, hhmm, nearestLevels, pnlPaise, points, rupees, sourceLabel, warnLabel, watching } from './srFormat';
import { DayPosition } from './DayPosition';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import styles from './SRPanel.module.css';

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
    return (
        <div className={styles.stat}>
            <span className={styles.statLabel}>{label}</span>
            <span className={styles.statValue}>{value}</span>
            {sub && <span className={styles.statSub}>{sub}</span>}
        </div>
    );
}

export function SRPanel() {
    const v = useSRStore(s => s.view);
    const last = useLastNiftyPaise(v?.key);

    if (!v) {
        return (
            <section className={styles.panel} aria-label="S/R strategy">
                <div className={styles.header}>
                    <h2 className={styles.title}>S/R strategy</h2>
                    <span className={`${styles.pill} ${styles.paper}`}>Paper</span>
                </div>
                <p className={styles.empty}>Waiting for the strategy engine…</p>
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

    return (
        <section className={styles.panel} aria-label="S/R strategy">
            <div className={styles.header}>
                <h2 className={styles.title}>S/R strategy</h2>
                <span className={`${styles.pill} ${styles[`regime${v.regime}`] ?? ''}`} title={`15m ADX ${v.adx.toFixed(1)}`}>
                    {REGIME_LABEL[v.regime] ?? v.regime}
                </span>
                <span className={`${styles.pill} ${styles.paper}`} title="The executor only sends real orders for NIFTY50_FNO">Paper</span>
                {updated && <span className={styles.updated}>as of {updated} IST</span>}
            </div>

            <div className={styles.grid}>
                <div className={styles.block}>
                    <h3 className={styles.blockTitle}>Situation</h3>
                    <div className={styles.stats}>
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

                <div className={styles.block}>
                    <h3 className={styles.blockTitle}>Watching</h3>
                    <p className={`${styles.watch} ${watch.kind === 'pending' ? styles.watchLive : ''}`}>{watch.text}</p>
                    {!inPosition && v.last_reject && (
                        <p className={styles.reject}>
                            Last refused{rejectAt ? ` ${rejectAt}` : ''}: {rejectLabel(v.last_reject)}
                        </p>
                    )}
                </div>

                <div className={styles.block}>
                    <h3 className={styles.blockTitle}>Position</h3>
                    {inPosition ? (
                        <>
                            <div className={styles.posHead}>
                                <span className={`${styles.side} ${v.side === 'CALL' ? styles.call : styles.put}`}>{v.side}</span>
                                <span className={styles.kind}>{kindLabel}</span>
                                {v.strike ? <span className={styles.strike}>{v.strike} {v.side === 'CALL' ? 'CE' : 'PE'}</span> : null}
                                {pnlPts !== null && (
                                    <span className={`${styles.pnl} ${pnlPts >= 0 ? styles.up : styles.down}`}>{points(pnlPts, true)}</span>
                                )}
                            </div>
                            <div className={styles.stats}>
                                <Stat label="Entry" value={rupees(v.entry)} />
                                <Stat label="Stop" value={rupees(v.stop)} sub={last && v.stop ? points(Math.abs(last - v.stop)) : undefined} />
                                <Stat label="Target" value={rupees(v.target)} sub={last && v.target ? points(Math.abs(v.target - last)) : undefined} />
                            </div>
                        </>
                    ) : (
                        <p className={styles.flat}>Flat</p>
                    )}
                </div>
            </div>

            <div className={styles.footer}>
                <span>Trades today {v.trades_today}/{v.max_trades}</span>
                <span className={v.day_pnl_pts > 0 ? styles.up : v.day_pnl_pts < 0 ? styles.down : undefined}>
                    Day {points(v.day_pnl_pts, true)}
                </span>
                {v.consec_losses > 0 && <span>Losses in a row {v.consec_losses}</span>}
                {Object.entries(v.warns ?? {}).map(([w, n]) => (
                    <span key={w} className={styles.warnCount}>⚠ {warnLabel(w)} ×{n}</span>
                ))}
                {v.cooldown_left > 0 && <span>Cooldown {v.cooldown_left}m</span>}
                <span>Entries {hhmm(v.entry_from_min)}–{hhmm(v.entry_to_min)} on {v.entry_tf}m closes</span>
                <span>{v.min_confirmations}/4 confirms</span>
                <span className={styles.modes}>
                    <span className={v.fade ? styles.on : styles.off}>Fade</span>
                    <span className={v.retest ? styles.on : styles.off}>Retest</span>
                    <span className={v.pullback ? styles.on : styles.off}>Pullback</span>
                </span>
            </div>
        </section>
    );
}
