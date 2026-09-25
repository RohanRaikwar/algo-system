import { useEffect, useState, type ReactNode } from 'react';
import { AlertTriangle, CheckCircle2, WifiOff } from 'lucide-react';
import { useWSStore } from '../../store/useWSStore';
import { fmtUptime } from '../../utils/helpers';
import type { SystemMetrics } from '../../types/ws';
import { Sparkline } from './Sparkline';
import { SERVICE_LABELS, collectIssues, isStale } from './healthStatus';
import styles from './Health.module.css';

type Tone = 'ok' | 'warn' | 'bad' | 'idle';

function fmtCount(n: number | undefined): string {
    if (n === undefined || n === null) return '—';
    return Math.round(n).toLocaleString('en-IN');
}

function fmtMs(ms: number | undefined | null, digits = 1): string {
    if (ms === undefined || ms === null || ms <= 0) return '—';
    if (ms >= 60_000) return `${(ms / 60_000).toFixed(1)} min`;
    if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`;
    return `${ms.toFixed(digits)} ms`;
}

function fmtRate(r: number): string {
    return r.toFixed(r < 10 ? 1 : 0);
}

function fmtAge(ms: number): string {
    if (ms < 1500) return 'just now';
    const s = Math.round(ms / 1000);
    if (s < 60) return `${s}s ago`;
    return `${Math.round(s / 60)}m ago`;
}

function fmtClock(ms: number): string {
    return new Date(ms).toLocaleTimeString('en-IN', { hour12: false, timeZone: 'Asia/Kolkata' });
}

/** Tone for a "lower is better" value; ok means within the normal range. */
function levelTone(v: number | undefined | null, warn: number, bad: number): Tone {
    if (v === undefined || v === null || v <= 0) return 'idle';
    if (v >= bad) return 'bad';
    if (v >= warn) return 'warn';
    return 'ok';
}

/** Re-render every second so ages and staleness stay current. */
function useNow(): number {
    const [now, setNow] = useState(Date.now());
    useEffect(() => {
        const t = setInterval(() => setNow(Date.now()), 1000);
        return () => clearInterval(t);
    }, []);
    return now;
}

function Card({ title, meta, children }: { title: string; meta?: ReactNode; children: ReactNode }) {
    return (
        <section className={styles.card}>
            <header className={styles.cardHead}>
                <h2 className={styles.cardTitle}>{title}</h2>
                {meta && <span className={styles.cardMeta}>{meta}</span>}
            </header>
            {children}
        </section>
    );
}

/** A labelled value. Colour only when the value needs attention. */
function Row({ label, value, tone, hint }: { label: string; value: ReactNode; tone?: Tone; hint?: string }) {
    const alert = tone === 'warn' || tone === 'bad' || tone === 'idle' ? styles[tone] : '';
    return (
        <div className={styles.row}>
            <span className={`${styles.rowLabel}${hint ? ` ${styles.hasHint}` : ''}`} title={hint}>{label}</span>
            <span className={`${styles.rowValue} ${alert}`}>{value}</span>
        </div>
    );
}

function Meter({ pct, tone }: { pct: number; tone: Tone }) {
    return (
        <div className={styles.meter} role="presentation">
            <span className={`${styles.meterFill} ${styles[`fill_${tone}`]}`} style={{ width: `${Math.max(0, Math.min(100, pct))}%` }} />
        </div>
    );
}

function NotReporting({ service }: { service: string }) {
    return (
        <p className={styles.unavailable}>
            {service} hasn't published stats recently. Check that the service is running.
        </p>
    );
}

const PROBLEM_COUNTERS: { key: keyof NonNullable<SystemMetrics['pipeline']>; label: string; hint: string }[] = [
    { key: 'dropped_ticks', label: 'Dropped ticks', hint: 'Ticks lost because a pipeline buffer was full' },
    { key: 'late_ticks', label: 'Late ticks', hint: 'Ticks that arrived after their candle was already closed' },
    { key: 'stale_candles_rejected', label: 'Stale candles rejected', hint: 'Candles refused because a newer one was already stored' },
    { key: 'feed_seq_gaps', label: 'Feed sequence gaps', hint: 'Times the feed skipped sequence numbers (possible missed ticks)' },
    { key: 'ws_reconnects', label: 'Feed reconnects', hint: 'Times the market data connection had to reconnect' },
];

export function SystemHealth() {
    const m = useWSStore(s => s.metrics);
    const metricsAt = useWSStore(s => s.metricsAt);
    const history = useWSStore(s => s.metricsHistory);
    const connected = useWSStore(s => s.connected);
    const wsDelay = useWSStore(s => s.wsDelay);
    const now = useNow();

    if (!m || metricsAt === null) {
        return (
            <div className={styles.page}>
                <h1 className={styles.pageTitle}>System health</h1>
                <div className={styles.waiting}>Waiting for the first metrics update from the gateway…</div>
            </div>
        );
    }

    const age = now - metricsAt;
    const stale = isStale(now, metricsAt, connected);
    const tickRate = history.length ? history[history.length - 1].tickRate : null;
    const ticking = (tickRate ?? 0) > 0;

    const issues = stale ? [] : collectIssues(m, ticking);
    const worst: Tone = stale || issues.some(i => i.tone === 'bad') ? 'bad' : issues.length ? 'warn' : 'ok';
    const p = m.pipeline;
    const orders = m.orders;
    const cores = m.cpu_cores || 1;
    const samples = m.e2e_latency_samples ?? 0;
    // Older gateway builds send no service stats and mis-measure latency.
    const legacy = m.services === undefined;

    const cb = !orders ? null
        : orders.cb_state === 1 ? { label: 'Tripped', sub: 'Open: new real entries rejected', tone: 'bad' as Tone }
            : orders.cb_state === 2 ? { label: 'Testing', sub: 'Half-open: testing the broker with one entry', tone: 'warn' as Tone }
                : { label: 'Normal', sub: 'Closed: entries reach the broker', tone: 'ok' as Tone };
    const rlPct = orders && orders.rl_max > 0 ? (orders.rl_count / orders.rl_max) * 100 : 0;
    const rlTone = levelTone(rlPct, 60, 90);
    const problems = p ? PROBLEM_COUNTERS.filter(c => (p[c.key] as number) > 0) : [];

    const cpuTone = levelTone(m.cpu_percent, 70, 90);
    const memTone = levelTone(m.mem_percent, 75, 90);
    const loadTone = levelTone((m.cpu_load_1 / cores) * 100, 70, 100);

    return (
        <div className={styles.page}>
            <div className={styles.titleRow}>
                <h1 className={styles.pageTitle}>System health</h1>
                <span className={`${styles.updated}${stale ? ` ${styles.bad}` : ''}`}>
                    <span className={`${styles.dot} ${styles[stale ? 'dot_bad' : 'dot_ok']}${stale ? '' : ` ${styles.dotPulse}`}`} />
                    {stale ? `Last update ${fmtClock(metricsAt)}` : `Updated ${fmtAge(age)}`}
                </span>
            </div>

            <div className={`${styles.banner} ${styles[`banner_${worst}`]}`} role="status">
                {stale ? <WifiOff size={20} /> : worst === 'ok' ? <CheckCircle2 size={20} /> : <AlertTriangle size={20} />}
                <div>
                    <div className={styles.bannerTitle}>
                        {stale
                            ? `No updates from the gateway for ${Math.round(age / 1000)}s`
                            : worst === 'ok' ? 'All systems running normally'
                                : issues.length === 1 ? '1 issue needs attention' : `${issues.length} issues need attention`}
                    </div>
                    {stale && (
                        <p className={styles.bannerText}>
                            {connected ? 'The connection is open but metrics stopped arriving.' : 'The browser is disconnected and reconnecting.'}
                            {' '}Everything below is from {fmtClock(metricsAt)} and may be out of date.
                        </p>
                    )}
                    {!stale && issues.length > 0 && (
                        <ul className={styles.issueList}>
                            {issues.map(i => <li key={i.key}>{i.text}</li>)}
                        </ul>
                    )}
                </div>
            </div>

            <div className={stale ? styles.staleBody : styles.body}>
                {/* Services: one compact row */}
                {(m.services?.length ?? 0) > 0 && (
                    <div className={styles.services} aria-label="Services">
                        {(m.services ?? []).map(s => (
                            <span key={s.name} className={styles.service} title={`${s.name}: ${s.status === 'up' ? `last heartbeat ${fmtAge(s.age_ms)}` : 'no heartbeat'}`}>
                                <span className={`${styles.dot} ${styles[s.status === 'up' ? 'dot_ok' : 'dot_bad']}`} />
                                <span className={styles.serviceName}>{SERVICE_LABELS[s.name] ?? s.name}</span>
                                {s.status === 'down' && <span className={styles.bad}>down</span>}
                            </span>
                        ))}
                        {m.redis_ok !== undefined && (
                            <span className={styles.service} title="Redis round trip from the API gateway">
                                <span className={`${styles.dot} ${styles[m.redis_ok ? 'dot_ok' : 'dot_bad']}`} />
                                <span className={styles.serviceName}>Redis</span>
                                <span className={m.redis_ok ? styles.serviceSub : styles.bad}>
                                    {m.redis_ok ? fmtMs(m.redis_ping_ms, 2) : 'unreachable'}
                                </span>
                            </span>
                        )}
                    </div>
                )}

                <div className={styles.grid}>
                    <div className={styles.col}>
                        {/* Market data pipeline */}
                        <Card
                            title="Market data pipeline"
                            meta={p && <span className={`${styles.chip} ${p.market_open ? styles.chip_ok : ''}`}>{p.market_open ? 'Market open' : 'Market closed'}</span>}
                        >
                            {!p ? <NotReporting service="mdengine" /> : (
                                <>
                                    <div className={styles.hero}>
                                        <span className={styles.heroValue}>{tickRate === null ? '—' : fmtRate(tickRate)}</span>
                                        <span className={styles.heroUnit}>ticks/s</span>
                                    </div>
                                    <Sparkline
                                        label="Ticks per second over the last 5 minutes"
                                        points={history.map(h => ({ at: h.at, value: h.tickRate }))}
                                        format={v => `${fmtRate(v)}/s`}
                                    />
                                    <div className={styles.divider} />
                                    <Row label="Ticks received" value={fmtCount(p.ticks_total)} />
                                    <Row label="1s candles built" value={fmtCount(p.candles_total)} />
                                    {problems.length === 0 ? (
                                        <p className={styles.allClear}>
                                            <CheckCircle2 size={14} /> No dropped or late ticks, stale candles, feed gaps or reconnects.
                                        </p>
                                    ) : problems.map(c => (
                                        <Row key={c.key} label={c.label} value={fmtCount(p[c.key] as number)} tone="warn" hint={c.hint} />
                                    ))}
                                    <div className={styles.divider} />
                                    <Row
                                        label="Candle lag"
                                        value={`${p.candle_lag_sec.toFixed(2)} s`}
                                        tone={ticking ? levelTone(p.candle_lag_sec, 2, 5) : undefined}
                                        hint="Time from a candle's start until it is finished and sent. A 1s candle can't close before its second ends, so about 1s is normal; over 2s means the pipeline is falling behind."
                                    />
                                    <Row
                                        label="Watermark delay"
                                        value={`${p.watermark_delay_sec.toFixed(2)} s`}
                                        tone={ticking ? levelTone(p.watermark_delay_sec, 3, 10) : undefined}
                                        hint="How far the latest tick time trails the clock. Ticks older than this cut-off are dropped as late. 1 to 2s is normal; steadily growing means ticks stopped arriving."
                                    />
                                </>
                            )}
                        </Card>
                    </div>

                    <div className={styles.col}>
                        {/* Latency */}
                        <Card title="Latency">
                            <div className={styles.latGrid}>
                                {([
                                    ['p50', legacy ? undefined : m.e2e_latency_p50_ms],
                                    ['p95', legacy ? undefined : m.e2e_latency_p95_ms],
                                    ['p99', legacy ? undefined : m.e2e_latency_p99_ms],
                                ] as const).map(([label, v]) => {
                                    const t = levelTone(v, 20, 100);
                                    return (
                                        <div key={label} className={styles.latCell}>
                                            <span className={styles.latLabel}>{label}</span>
                                            <span className={`${styles.latValue} ${t === 'ok' ? '' : styles[t]}`}>{fmtMs(v)}</span>
                                        </div>
                                    );
                                })}
                            </div>
                            {!legacy && (
                                <Sparkline
                                    label="Tick pipeline p95 latency over the last 5 minutes"
                                    points={history.map(h => ({ at: h.at, value: h.p95 }))}
                                    format={v => fmtMs(v)}
                                />
                            )}
                            <p className={styles.note}>
                                Tick pipeline p95: market data received to pushed to your browser
                                {legacy ? '. Shown after the stack restarts; the old gateway measured candle age instead.'
                                    : samples > 0 ? `, last ${fmtCount(samples)} ticks.` : '. No ticks measured yet.'}
                            </p>
                            <div className={styles.divider} />
                            <Row label="Browser round trip" value={fmtMs(wsDelay, 0)} tone={levelTone(wsDelay, 200, 1000)} hint="Ping from this browser to the gateway and back" />
                            <Row
                                label="Indicator compute"
                                value={fmtMs(m.indicator_compute_ms, 3)}
                                tone={levelTone(m.indicator_compute_ms, 3, 10)}
                                hint="Average time to update indicators for one candle"
                            />
                            <Row label="Indicator updates" value={m.indicators ? fmtCount(m.indicators.indicators_total) : 'Not reporting'} tone={m.indicators ? undefined : 'idle'} />
                        </Card>

                        {/* Order safety */}
                        <Card title="Order safety" meta="stratengine">
                            {!orders || !cb ? <NotReporting service="stratengine" /> : (
                                <>
                                    <div className={styles.hero}>
                                        <span className={`${styles.dot} ${styles[`dot_${cb.tone}`]} ${styles.dotLg}`} />
                                        <span className={`${styles.heroValue} ${styles[cb.tone]}`}>{cb.label}</span>
                                    </div>
                                    <p className={styles.note}>Circuit breaker {cb.sub.toLowerCase()}.</p>
                                    <Row
                                        label="Consecutive broker failures"
                                        value={fmtCount(orders.cb_failures)}
                                        tone={orders.cb_failures > 0 ? 'warn' : undefined}
                                        hint="The breaker opens after 3 failed broker calls in a row"
                                    />
                                    <div className={styles.divider} />
                                    <Row label="Real entries in the last 60s" value={`${fmtCount(orders.rl_count)} of ${fmtCount(orders.rl_max)}`} tone={rlTone === 'ok' || rlTone === 'idle' ? undefined : rlTone} />
                                    <Meter pct={rlPct} tone={rlTone} />
                                    <p className={styles.note}>Real entries only. New entries are refused at the limit; exits are never blocked.</p>
                                </>
                            )}
                        </Card>
                    </div>

                    <div className={styles.col}>
                        {/* Host */}
                        <Card title="Host machine">
                            <Row label="CPU" value={`${m.cpu_percent.toFixed(1)}%`} tone={cpuTone} />
                            <Meter pct={m.cpu_percent} tone={cpuTone} />
                            <Row
                                label="Load average"
                                value={`${m.cpu_load_1.toFixed(2)}  ${m.cpu_load_5.toFixed(2)}  ${m.cpu_load_15.toFixed(2)}`}
                                tone={loadTone}
                                hint={`1, 5 and 15 minute load averages. Above ${cores} (the core count) means work is queueing.`}
                            />
                            <Row label="Cores" value={cores} />
                            <div className={styles.divider} />
                            <Row label="Memory" value={`${(m.mem_used_mb / 1024).toFixed(1)} of ${(m.mem_total_mb / 1024).toFixed(1)} GB`} tone={memTone} />
                            <Meter pct={m.mem_percent} tone={memTone} />
                        </Card>

                        {/* Gateway process */}
                        <Card title="API gateway process" meta={`up ${fmtUptime(m.uptime_sec)}`}>
                            <Row label="Goroutines" value={fmtCount(m.goroutines)} />
                            <Row label="Heap in use" value={`${m.heap_alloc_mb.toFixed(1)} MB`} />
                            <Row label="Memory from OS" value={`${m.sys_mb.toFixed(1)} MB`} />
                            <Row label="GC cycles" value={fmtCount(m.gc_runs)} />
                        </Card>
                    </div>
                </div>
            </div>
        </div>
    );
}
