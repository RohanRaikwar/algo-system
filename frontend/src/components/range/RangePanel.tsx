import { useRangeStore } from '../../store/useRangeStore';
import { RANGE_KIND_LABEL, type RangeEntryKind } from '../signals/rangeKind';
import { REGIME_LABEL, asOf, hhmm, pnlPaise, points, rupees, watching } from './rangeFormat';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import styles from './RangePanel.module.css';

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
    return (
        <div className={styles.stat}>
            <span className={styles.statLabel}>{label}</span>
            <span className={styles.statValue}>{value}</span>
            {sub && <span className={styles.statSub}>{sub}</span>}
        </div>
    );
}

export function RangePanel() {
    const v = useRangeStore(s => s.view);
    const last = useLastNiftyPaise(v?.key);

    if (!v) {
        return (
            <section className={styles.panel} aria-label="Range strategy">
                <div className={styles.header}>
                    <h2 className={styles.title}>Range strategy</h2>
                    <span className={`${styles.pill} ${styles.paper}`}>Paper</span>
                </div>
                <p className={styles.empty}>Waiting for the strategy engine…</p>
            </section>
        );
    }

    const watch = watching(v);
    const inPosition = v.side !== 'NONE';
    const kindLabel = v.kind ? RANGE_KIND_LABEL[v.kind as RangeEntryKind] ?? v.kind : '';
    const pnlPts = pnlPaise(v, last);
    const updated = asOf(v.ts);

    return (
        <section className={styles.panel} aria-label="Range strategy">
            <div className={styles.header}>
                <h2 className={styles.title}>Range strategy</h2>
                <span className={`${styles.pill} ${styles[`regime${v.regime}`]}`} title={`15m ADX ${v.adx.toFixed(1)} (range below ${v.max_adx})`}>
                    {REGIME_LABEL[v.regime] ?? v.regime}
                </span>
                <span className={`${styles.pill} ${styles.paper}`} title="The executor only sends real orders for NIFTY50_FNO">Paper</span>
                {updated && <span className={styles.updated}>as of {updated} IST</span>}
            </div>

            <div className={styles.grid}>
                <div className={styles.block}>
                    <h3 className={styles.blockTitle}>Situation</h3>
                    <div className={styles.stats}>
                        <Stat label="15m ADX" value={v.adx.toFixed(1)} sub={`range < ${v.max_adx}`} />
                        <Stat label="Support" value={rupees(v.support)} sub={last && v.support ? points(last - v.support, true) : undefined} />
                        <Stat label="Resistance" value={rupees(v.resistance)} sub={last && v.resistance ? points(v.resistance - last, true) : undefined} />
                        <Stat label="Width" value={v.width ? points(v.width) : '—'} sub={v.class ? v.class.toLowerCase() : undefined} />
                        <Stat label="5m RSI" value={v.rsi5 ? v.rsi5.toFixed(0) : '—'} />
                        <Stat label="Day move" value={v.day_open ? points(v.day_trend, true) : '—'} />
                    </div>
                </div>

                <div className={styles.block}>
                    <h3 className={styles.blockTitle}>Watching</h3>
                    <p className={`${styles.watch} ${watch.kind === 'pending' || (watch.kind === 'flag' && watch.ready) ? styles.watchLive : ''}`}>
                        {watch.text}
                    </p>
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
                <span>Entries on {v.entry_tf}m closes</span>
                <span>Flat by {hhmm(v.time_exit_min)}</span>
                <span className={styles.modes}>
                    <span className={v.breakout ? styles.on : styles.off}>Breakout</span>
                    <span className={v.flag ? styles.on : styles.off}>Flag</span>
                    <span className={v.mean_reversion ? styles.on : styles.off}>Mean rev.</span>
                </span>
            </div>
        </section>
    );
}
