import { useEffect, useState, type ReactNode } from 'react';
import { AlertTriangle, CheckCircle2, WifiOff } from 'lucide-react';
import { useWSStore } from '../../store/useWSStore';
import { fmtUptime } from '../../utils/helpers';
import type { SystemMetrics } from '../../types/ws';
import { Sparkline } from './Sparkline';
import { SERVICE_LABELS, collectIssues, isStale, isTicking } from './healthStatus';
import { cn } from '@/lib/utils';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Alert } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Card as UICard } from '@/components/ui/card';
import { Empty } from '@/components/ui/empty';
import { Progress } from '@/components/ui/progress';
import { Separator } from '@/components/ui/separator';

type Tone = 'ok' | 'warn' | 'bad' | 'idle';

/** Text colour per tone. */
const TONE_TEXT: Record<Tone, string> = {
    ok: 'text-sg-profit',
    warn: 'text-sg-warn',
    bad: 'text-sg-loss',
    idle: 'text-sg-faint',
};

const DOT_BG: Record<Tone, string> = {
    ok: 'bg-sg-profit',
    warn: 'bg-sg-warn',
    bad: 'bg-sg-loss',
    idle: 'bg-sg-faint',
};

/** Meter fill tone per health tone. */
const FILL_TONE = { ok: 'accent', warn: 'warn', bad: 'loss', idle: 'faint' } as const;

/** Banner left-border tone; idle has none. */
const BANNER_TONE = { ok: 'profit', warn: 'warn', bad: 'loss', idle: undefined } as const;

function dot(tone: Tone, opts: { lg?: boolean; pulse?: boolean } = {}): string {
    return cn(
        'rounded-[50%] shrink-0',
        opts.lg ? 'w-[12px] h-[12px]' : 'w-[8px] h-[8px]',
        DOT_BG[tone],
        opts.pulse && 'animate-[health-pulse_2s_ease-out_infinite] motion-reduce:animate-none',
    );
}

const pageCls = 'flex flex-col gap-[14px]';
const pageTitle = 'text-sg-text text-[1.25rem] font-semibold tracking-[-0.01em]';
const colCls = 'flex flex-col gap-[12px] min-w-0 lte-1100:last:col-span-full lte-1100:last:grid lte-1100:last:grid-cols-[repeat(2,minmax(0,1fr))] lte-1100:last:items-start lte-720:last:grid-cols-[minmax(0,1fr)]';
const serviceCls = 'inline-flex items-center gap-[8px] text-[0.86rem] whitespace-nowrap';
const serviceName = 'text-sg-text-2 font-medium';
const heroCls = 'flex items-center gap-[10px] mb-[10px]';
const heroValue = 'text-[1.9rem] font-semibold tracking-[-0.02em] leading-[1.1] tabular-nums';
const note = 'text-sg-faint text-[0.8rem] leading-[1.5] mt-[2px] mb-[4px] mx-0';

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
        <UICard variant="panel" render={<section />}>
            <header className="flex items-center justify-between gap-[10px] mb-[12px]">
                <h2 className="text-sg-text text-[0.95rem] font-semibold">{title}</h2>
                {meta && <span className="text-sg-faint text-[0.78rem]">{meta}</span>}
            </header>
            {children}
        </UICard>
    );
}

/** A labelled value. Colour only when the value needs attention. */
function Row({ label, value, tone, hint }: { label: string; value: ReactNode; tone?: Tone; hint?: string }) {
    const alert = tone === 'warn' || tone === 'bad' || tone === 'idle' ? TONE_TEXT[tone] : 'text-sg-text';
    return (
        <div className="flex items-baseline justify-between gap-[12px] py-[6px] px-0 text-[0.86rem]">
            {hint ? (
                <Tooltip>
                    <TooltipTrigger render={<span className="text-sg-muted cursor-help underline decoration-dotted decoration-sg-faint underline-offset-[3px]" />}>{label}</TooltipTrigger>
                    {/* Hints are sentences: wrap like the native title did. */}
                    <TooltipContent className="whitespace-normal max-w-[min(280px,calc(100vw_-_32px))]">{hint}</TooltipContent>
                </Tooltip>
            ) : <span className="text-sg-muted">{label}</span>}
            <span className={cn('font-medium tabular-nums whitespace-pre text-right', alert)}>{value}</span>
        </div>
    );
}

function Meter({ pct, tone }: { pct: number; tone: Tone }) {
    return (
        <Progress variant="meter" tone={FILL_TONE[tone]} className="mt-[2px] mb-[8px]" aria-hidden value={Math.max(0, Math.min(100, pct))} />
    );
}

function NotReporting({ service }: { service: string }) {
    return (
        <Empty variant="unavailable" render={<p />}>
            {service} hasn't published stats recently. Check that the service is running.
        </Empty>
    );
}

const PROBLEM_COUNTERS: { key: keyof NonNullable<SystemMetrics['pipeline']>; label: string; hint: string }[] = [
    { key: 'dropped_ticks', label: 'Dropped ticks', hint: 'Ticks lost because a pipeline buffer was full' },
    { key: 'late_ticks', label: 'Late ticks', hint: 'Ticks that arrived after their candle was already closed' },
    { key: 'stale_candles_rejected', label: 'Stale candles rejected', hint: 'Candles refused because a newer one was already stored' },
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
            <div className={pageCls}>
                <h1 className={pageTitle}>System health</h1>
                <Empty variant="panel">Waiting for the first metrics update from the gateway…</Empty>
            </div>
        );
    }

    const age = now - metricsAt;
    const stale = isStale(now, metricsAt, connected);
    const tickRate = history.length ? history[history.length - 1].tickRate : null;
    const ticking = isTicking(history.map(h => h.tickRate));

    const lagHistory = history.map(h => h.candleLag).filter((v): v is number => v !== null);
    const issues = stale ? [] : collectIssues(m, ticking, lagHistory);
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
    const ows = orders?.order_ws;
    const owsState = !ows ? null
        : ows.status === 'up' ? { label: 'Connected', sub: 'Fills and stop hits arrive as soon as the broker sends them.', tone: 'ok' as Tone }
            : ows.status === 'connecting' ? { label: 'Connecting', sub: 'First connection pending. Fills arrive by order-book polling meanwhile.', tone: 'warn' as Tone }
                : ows.status === 'down' ? { label: 'Down', sub: 'Reconnecting. Fills arrive by order-book polling; stop hits are seen within 60s.', tone: 'bad' as Tone }
                    : ows.status === 'paper' ? { label: 'Paper mode', sub: 'No live orders, so the socket does not connect.', tone: 'idle' as Tone }
                        : { label: 'Off', sub: 'Disabled by STRAT_ORDER_WS. Fills arrive by order-book polling.', tone: 'idle' as Tone };
    const owsSince = ows?.since ? Date.parse(ows.since) : NaN;
    const owsLastPush = ows?.last_push_at ? Date.parse(ows.last_push_at) : NaN;
    const owsActive = ows?.status === 'up' || ows?.status === 'down' || ows?.status === 'connecting';
    const rlPct = orders && orders.rl_max > 0 ? (orders.rl_count / orders.rl_max) * 100 : 0;
    const rlTone = levelTone(rlPct, 60, 90);
    const problems = p ? PROBLEM_COUNTERS.filter(c => (p[c.key] as number) > 0) : [];

    const cpuTone = levelTone(m.cpu_percent, 70, 90);
    const memTone = levelTone(m.mem_percent, 75, 90);
    const loadTone = levelTone((m.cpu_load_1 / cores) * 100, 70, 100);

    return (
        <div className={pageCls}>
            <div className="flex items-baseline justify-between gap-[12px]">
                <h1 className={pageTitle}>System health</h1>
                <span className={cn('inline-flex items-center gap-[7px] text-[0.8rem] tabular-nums', stale ? 'text-sg-loss' : 'text-sg-faint')}>
                    <span className={dot(stale ? 'bad' : 'ok', { pulse: !stale })} />
                    {stale ? `Last update ${fmtClock(metricsAt)}` : `Updated ${fmtAge(age)}`}
                </span>
            </div>

            <Alert variant="status" tone={BANNER_TONE[worst]} role="status">
                {stale ? <WifiOff size={20} /> : worst === 'ok' ? <CheckCircle2 size={20} /> : <AlertTriangle size={20} />}
                <div>
                    <div className="text-sg-text font-semibold">
                        {stale
                            ? `No updates from the gateway for ${Math.round(age / 1000)}s`
                            : worst === 'ok' ? 'All systems running normally'
                                : issues.length === 1 ? '1 issue needs attention' : `${issues.length} issues need attention`}
                    </div>
                    {stale && (
                        <p className="mt-[4px] text-sg-text-2 text-[0.86rem] leading-[1.5]">
                            {connected ? 'The connection is open but metrics stopped arriving.' : 'The browser is disconnected and reconnecting.'}
                            {' '}Everything below is from {fmtClock(metricsAt)} and may be out of date.
                        </p>
                    )}
                    {!stale && issues.length > 0 && (
                        <ul className="mt-[6px] pl-[18px] text-sg-text-2 text-[0.86rem] leading-[1.6]">
                            {issues.map(i => <li key={i.key}>{i.text}</li>)}
                        </ul>
                    )}
                </div>
            </Alert>

            <div className={cn('flex flex-col gap-[14px] transition-opacity duration-200 ease-[ease] motion-reduce:transition-none', stale && 'opacity-[0.45] [filter:grayscale(0.6)]')}>
                {/* Services: one compact row */}
                {(m.services?.length ?? 0) > 0 && (
                    <div className="flex flex-wrap items-center gap-[6px_22px] py-[10px] px-[16px] bg-sg-surface border border-solid border-sg-line rounded-[12px]" aria-label="Services">
                        {(m.services ?? []).map(s => (
                            <span key={s.name} className={serviceCls} title={`${s.name}: ${s.status === 'up' ? `last heartbeat ${fmtAge(s.age_ms)}` : 'no heartbeat'}`}>
                                <span className={dot(s.status === 'up' ? 'ok' : 'bad')} />
                                <span className={serviceName}>{SERVICE_LABELS[s.name] ?? s.name}</span>
                                {s.status === 'down' && <span className="text-sg-loss">down</span>}
                            </span>
                        ))}
                        {m.redis_ok !== undefined && (
                            <span className={serviceCls} title="Redis round trip from the API gateway">
                                <span className={dot(m.redis_ok ? 'ok' : 'bad')} />
                                <span className={serviceName}>Redis</span>
                                <span className={m.redis_ok ? 'text-sg-faint text-[0.8rem] tabular-nums' : 'text-sg-loss'}>
                                    {m.redis_ok ? fmtMs(m.redis_ping_ms, 2) : 'unreachable'}
                                </span>
                            </span>
                        )}
                    </div>
                )}

                <div className="grid grid-cols-[repeat(3,minmax(0,1fr))] gap-[12px] items-start lte-1100:grid-cols-[repeat(2,minmax(0,1fr))] lte-720:grid-cols-[minmax(0,1fr)]">
                    <div className={colCls}>
                        {/* Market data pipeline */}
                        <Card
                            title="Market data pipeline"
                            meta={p && <Badge variant="status" tone={p.market_open ? 'profit' : undefined}>{p.market_open ? 'Market open' : 'Market closed'}</Badge>}
                        >
                            {!p ? <NotReporting service="mdengine" /> : (
                                <>
                                    <div className={heroCls}>
                                        <span className={cn(heroValue, 'text-sg-text')}>{tickRate === null ? '—' : fmtRate(tickRate)}</span>
                                        <span className="text-sg-muted text-[0.86rem]">ticks/s</span>
                                    </div>
                                    <Sparkline
                                        label="Ticks per second over the last 5 minutes"
                                        points={history.map(h => ({ at: h.at, value: h.tickRate }))}
                                        format={v => `${fmtRate(v)}/s`}
                                    />
                                    <Separator variant="soft" />
                                    <Row label="Ticks received" value={fmtCount(p.ticks_total)} />
                                    <Row label="1s candles built" value={fmtCount(p.candles_total)} />
                                    {problems.length === 0 ? (
                                        <p className="flex items-start gap-[7px] mt-[6px] mb-[2px] mx-0 text-sg-muted text-[0.84rem] leading-[1.45] [&_svg]:shrink-0 [&_svg]:mt-[2px] [&_svg]:text-sg-profit">
                                            <CheckCircle2 size={14} /> No dropped or late ticks, stale candles, feed gaps or reconnects.
                                        </p>
                                    ) : problems.map(c => (
                                        <Row key={c.key} label={c.label} value={fmtCount(p[c.key] as number)} tone="warn" hint={c.hint} />
                                    ))}
                                    <Separator variant="soft" />
                                    {p.feed_conns_wanted !== undefined && p.feed_conns_wanted > 0 && (
                                        <Row
                                            label="Feed sockets"
                                            value={`${p.feed_conns_up ?? 0} / ${p.feed_conns_wanted}`}
                                            tone={p.market_open && (p.feed_conns_up ?? 0) < p.feed_conns_wanted ? 'warn' : undefined}
                                            hint="Parallel Angel One connections. Each covers stalls on the others' network path; fewer than configured means less protection."
                                        />
                                    )}
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

                    <div className={colCls}>
                        {/* Latency */}
                        <Card title="Latency">
                            <div className="grid grid-cols-[repeat(3,1fr)] gap-[8px] mb-[8px]">
                                {([
                                    ['p50', legacy ? undefined : m.e2e_latency_p50_ms],
                                    ['p95', legacy ? undefined : m.e2e_latency_p95_ms],
                                    ['p99', legacy ? undefined : m.e2e_latency_p99_ms],
                                ] as const).map(([label, v]) => {
                                    const t = levelTone(v, 20, 100);
                                    return (
                                        <div key={label} className="flex flex-col gap-[2px] py-[10px] px-[12px] bg-sg-bg border border-solid border-sg-line-soft rounded-[10px]">
                                            <span className="text-sg-faint text-[0.76rem]">{label}</span>
                                            <span className={cn('whitespace-nowrap text-[1.05rem] font-semibold tabular-nums', t === 'ok' ? 'text-sg-text' : TONE_TEXT[t])}>{fmtMs(v)}</span>
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
                            <p className={note}>
                                Tick pipeline p95: market data received to pushed to your browser
                                {legacy ? '. Shown after the stack restarts; the old gateway measured candle age instead.'
                                    : samples > 0 ? `, last ${fmtCount(samples)} ticks.` : '. No ticks measured yet.'}
                            </p>
                            <Separator variant="soft" />
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
                                    <div className={heroCls}>
                                        <span className={dot(cb.tone, { lg: true })} />
                                        <span className={cn(heroValue, TONE_TEXT[cb.tone])}>{cb.label}</span>
                                    </div>
                                    <p className={note}>Circuit breaker {cb.sub.toLowerCase()}.</p>
                                    <Row
                                        label="Consecutive broker failures"
                                        value={fmtCount(orders.cb_failures)}
                                        tone={orders.cb_failures > 0 ? 'warn' : undefined}
                                        hint="The breaker opens after 3 failed broker calls in a row"
                                    />
                                    <Separator variant="soft" />
                                    <Row label="Real entries in the last 60s" value={`${fmtCount(orders.rl_count)} of ${fmtCount(orders.rl_max)}`} tone={rlTone === 'ok' || rlTone === 'idle' ? undefined : rlTone} />
                                    <Meter pct={rlPct} tone={rlTone} />
                                    <p className={note}>Real entries only. New entries are refused at the limit; exits are never blocked.</p>
                                </>
                            )}
                        </Card>

                        {/* Order-status socket */}
                        <Card title="Order-status socket" meta="stratengine">
                            {!orders ? <NotReporting service="stratengine" /> : !ows || !owsState ? (
                                <Empty variant="unavailable" render={<p />}>The running stratengine does not report the order-status socket. Restart it to load the new build.</Empty>
                            ) : (
                                <>
                                    <div className={heroCls}>
                                        <span className={dot(owsState.tone, { lg: true })} />
                                        <span className={cn(heroValue, TONE_TEXT[owsState.tone])}>{owsState.label}</span>
                                    </div>
                                    <p className={note}>{owsState.sub}</p>
                                    {owsActive && (
                                        <>
                                            <Row
                                                label="Since"
                                                value={Number.isNaN(owsSince) ? '—' : `${fmtClock(owsSince)} (${fmtAge(now - owsSince)})`}
                                            />
                                            <Row
                                                label="Reconnects"
                                                value={fmtCount(ows.reconnects)}
                                                tone={ows.reconnects > 0 ? 'warn' : undefined}
                                                hint="Times the socket reconnected after its first connection"
                                            />
                                            <Row label="Order updates received" value={fmtCount(ows.pushes)} />
                                            <Row
                                                label="Last update"
                                                value={Number.isNaN(owsLastPush) ? 'None yet' : `${fmtClock(owsLastPush)} (${fmtAge(now - owsLastPush)})`}
                                                tone={Number.isNaN(owsLastPush) ? 'idle' : undefined}
                                            />
                                            {ows.last_error && (
                                                <Row label="Last error" value={ows.last_error} tone="warn" hint="Error that ended the last failed or dropped connection" />
                                            )}
                                        </>
                                    )}
                                </>
                            )}
                        </Card>
                    </div>

                    <div className={colCls}>
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
                            <Separator variant="soft" />
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
