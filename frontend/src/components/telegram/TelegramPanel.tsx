import { useEffect, useMemo, useState } from 'react';
import { Bot, CheckCircle, MessageSquare, RefreshCw, Send, XCircle } from 'lucide-react';
import { fetchTelegram, type TelegramMessage, type TelegramView } from '../../services/api';
import { cn } from '@/lib/utils';
import { Badge, type BadgeTone } from '@/components/ui/badge';
import { Card } from '@/components/ui/card';
import { Empty } from '@/components/ui/empty';
import { Spinner } from '@/components/ui/spinner';
import { Switch } from '@/components/ui/switch';
import { errorText, footerNote, summaryGrid, summaryLabel, summaryValue } from '../signals/classes';
import { timelineParts } from './timeline';

const POLL_MS = 10_000;

/** Verdict → badge tone and a plain-words label. */
const VERDICT: Record<string, { tone: BadgeTone; label: string }> = {
    SENT: { tone: 'profit', label: 'Call taken' },
    FOLLOW_UP: { tone: 'accent', label: 'Follow-up' },
    DISAGREE: { tone: 'loss', label: 'Readers disagree' },
    REFUSED: { tone: 'warn', label: 'Refused' },
    ERROR: { tone: 'loss', label: 'Error' },
    NOISE: { tone: 'muted', label: 'Noise' },
    SKIPPED: { tone: 'muted', label: 'Skipped' },
    CONTEXT: { tone: 'muted', label: 'In trade context' },
    LLM_STALE: { tone: 'warn', label: 'LLM too slow' },
    AUDIT_DISAGREE: { tone: 'warn', label: 'Fast exit: LLM disagrees' },
    OLD: { tone: 'muted', label: 'Too old' },
};

function queueSize(q?: Record<string, number>): number {
    return q ? Object.values(q).reduce((a, b) => a + b, 0) : 0;
}

/** "2 waiting (exit 1, noise 1) · 1 running" */
function queueText(q?: Record<string, number>, running?: number): string {
    const n = queueSize(q);
    const parts = q ? Object.entries(q).map(([k, v]) => `${k} ${v}`).join(', ') : '';
    const run = running ? ` · ${running} running` : '';
    return n ? `${n} waiting (${parts})${run}` : `empty${run}`;
}

/** Verdicts hidden by "Only trade messages". */
const QUIET = new Set(['NOISE', 'SKIPPED', 'OLD', 'CONTEXT']);

/** A real timestamp (Go sends year 1 for "never"). */
function has(ts?: string): boolean {
    if (!ts) return false;
    const d = new Date(ts);
    return !Number.isNaN(d.getTime()) && d.getFullYear() >= 2000;
}

function time(ts?: string): string {
    if (!ts) return '—';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return '—';
    return d.toLocaleTimeString('en-IN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, timeZone: 'Asia/Kolkata' });
}

function ago(ts?: string): string {
    if (!ts) return 'never';
    const s = Math.round((Date.now() - new Date(ts).getTime()) / 1000);
    if (Number.isNaN(s) || s < 0 || s > 86400 * 30) return 'never';
    if (s < 60) return `${s}s ago`;
    if (s < 3600) return `${Math.floor(s / 60)}m ago`;
    return `${Math.floor(s / 3600)}h ago`;
}

function StatePill({ ok, okText, badText }: { ok: boolean; okText: string; badText: string }) {
    return (
        <Badge tone={ok ? 'profit' : 'loss'}>
            {ok ? <CheckCircle size={13} /> : <XCircle size={13} />} {ok ? okText : badText}
        </Badge>
    );
}

function Field({ label, value, tone }: { label: string; value: string; tone?: string }) {
    return (
        <Card variant="summary-surface">
            <div className={summaryLabel}>{label}</div>
            <div className={summaryValue(tone, true)} title={value}>{value}</div>
        </Card>
    );
}

function MessageRow({ m }: { m: TelegramMessage }) {
    const v = VERDICT[m.verdict] ?? { tone: 'muted' as BadgeTone, label: m.verdict };
    const steps = timelineParts(m);
    return (
        <Card className="p-[12px] flex flex-col gap-[6px]">
            <div className="flex items-center gap-[8px] flex-wrap text-[0.8rem] text-sg-muted">
                <span className="tabular-nums">{time(m.sent_at)}</span>
                <span className="text-sg-faint">#{m.msg_id}</span>
                {m.reply_to ? <span className="text-sg-faint">↩ reply to #{m.reply_to}</span> : null}
                {m.edited ? <Badge variant="kind">edited</Badge> : null}
                <Badge tone={v.tone} className="ml-auto">{v.label}</Badge>
            </div>
            <div className="whitespace-pre-line text-[0.86rem] line-clamp-6">{m.text}</div>
            {m.english && (
                <div className="text-[0.84rem] text-sg-muted">
                    <span className="font-semibold text-sg-text">English: </span>{m.english}
                </div>
            )}
            {m.summary && <div className="font-mono text-[0.82rem] text-sg-accent">{m.summary}</div>}
            {m.reason && <div className="text-[0.78rem] text-sg-faint">{m.reason}</div>}
            {steps.length > 0 && (
                <div className="text-[0.78rem] text-sg-muted tabular-nums" title="Time since the message was received">
                    <span className="font-semibold text-sg-text">Latency: </span>{steps.join(' · ')}
                </div>
            )}
            {m.outcome && (
                <div className="text-[0.8rem] py-[6px] px-[10px] rounded-ctrl bg-sg-raised">
                    <span className="font-semibold">Trade: </span>{m.outcome}
                </div>
            )}
        </Card>
    );
}

export function TelegramPanel() {
    const [view, setView] = useState<TelegramView | null>(null);
    const [loading, setLoading] = useState(true);
    const [failed, setFailed] = useState(false);
    const [onlyTrade, setOnlyTrade] = useState(true);

    useEffect(() => {
        let alive = true;
        const load = async () => {
            const v = await fetchTelegram();
            if (!alive) return;
            setFailed(v === null);
            if (v) setView(v);
            setLoading(false);
        };
        load();
        const id = setInterval(load, POLL_MS);
        return () => {
            alive = false;
            clearInterval(id);
        };
    }, []);

    const messages = useMemo(
        () => (view?.messages ?? []).filter(m => !onlyTrade || !QUIET.has(m.verdict)),
        [view, onlyTrade],
    );

    if (loading) {
        return (
            <Empty>
                <Spinner size={48} strokeWidth={1.5} />
                <p>Loading Telegram status…</p>
            </Empty>
        );
    }
    if (failed && !view) {
        return (
            <Empty>
                <XCircle size={48} strokeWidth={1.5} />
                <p className={errorText}>Could not reach the API gateway. Retrying every 10 seconds.</p>
            </Empty>
        );
    }

    const st = view?.status;
    const tg = st?.telegram;
    const llm = st?.llm;
    const running = !!view?.running;

    return (
        <div className="flex flex-col gap-[12px]">
            <div className="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-[12px] lte-900:grid-cols-[minmax(0,1fr)]">
                {/* Telegram session */}
                <Card className="p-[14px] flex flex-col gap-[10px]">
                    <div className="flex items-center justify-between gap-[10px]">
                        <div className="flex items-center gap-[8px] font-semibold"><Send size={18} className="text-sg-muted" /> Telegram session</div>
                        <StatePill ok={running && !!tg?.connected} okText="Connected" badText={running ? 'Disconnected' : 'Not running'} />
                    </div>
                    {!running && <div className="text-sg-muted text-[0.84rem]">The tgsignal service is not running (no status in the last minute).</div>}
                    {tg?.last_error && <div className="text-sg-loss text-[0.82rem]">{tg.last_error}</div>}
                    <div className={summaryGrid}>
                        <Field label="Logged in as" value={tg?.user || '—'} />
                        <Field label="Groups followed" value={tg?.chats?.join(', ') || '—'} />
                        <Field label="Connected since" value={time(tg?.since)} />
                        <Field label="Last message" value={ago(tg?.last_message_at)} />
                    </div>
                </Card>

                {/* LLM reader */}
                <Card className="p-[14px] flex flex-col gap-[10px]">
                    <div className="flex items-center justify-between gap-[10px]">
                        <div className="flex items-center gap-[8px] font-semibold"><Bot size={18} className="text-sg-muted" /> LLM reader</div>
                        {llm?.enabled
                            ? <StatePill ok={llm.ok || !has(llm.last_read_at)} okText={has(llm.last_read_at) ? 'Working' : 'Ready'} badText="Failing" />
                            : <Badge>Off (regex only)</Badge>}
                    </div>
                    {llm?.last_error && <div className="text-sg-loss text-[0.82rem] break-words">{llm.last_error}</div>}
                    <div className={summaryGrid}>
                        <Field label="Model" value={llm?.model || '—'} />
                        <Field label="Provider" value={`${llm?.provider || '—'}${llm?.providers && llm.providers > 1 ? ` (+${llm.providers - 1} more)` : ''}`} />
                        <Field label="Reads today" value={`${llm?.reads_today ?? 0}${llm?.errors_today ? ` (${llm.errors_today} failed)` : ''}`} tone={llm?.errors_today ? 'text-sg-warn' : undefined} />
                        <Field label="Tokens today" value={`${(llm?.tokens_today ?? 0).toLocaleString('en-IN')} · avg ${llm?.avg_tokens ?? 0}`} />
                        <Field label="Last read" value={ago(llm?.last_read_at)} />
                        <Field label="Queue" value={queueText(llm?.queue, llm?.running)} tone={queueSize(llm?.queue) > 5 ? 'text-sg-warn' : undefined} />
                        <Field label="Budget left" value={llm?.budget_tokens === undefined || llm.budget_tokens < 0 ? 'no limit' : `${llm.budget_tokens.toLocaleString('en-IN')} tokens`} />
                        {llm?.provider_in_use && llm.provider_in_use !== llm.provider && <Field label="Using" value={llm.provider_in_use} tone="text-sg-warn" />}
                    </div>
                </Card>
            </div>

            {/* Today's verdict counts */}
            {st && Object.keys(st.verdicts).length > 0 && (
                <div className="flex flex-wrap gap-[6px]">
                    {Object.entries(st.verdicts).sort((a, b) => b[1] - a[1]).map(([k, n]) => (
                        <Badge key={k} tone={(VERDICT[k]?.tone ?? 'muted')}>{VERDICT[k]?.label ?? k}: {n}</Badge>
                    ))}
                </div>
            )}

            {/* Messages */}
            <div className="flex items-center justify-between gap-[10px]">
                <div className="flex items-center gap-[8px] font-semibold"><MessageSquare size={16} className="text-sg-muted" /> Messages read</div>
                <label className="flex items-center gap-[8px] text-[0.84rem] text-sg-muted cursor-pointer">
                    <Switch checked={onlyTrade} onCheckedChange={setOnlyTrade} aria-label="Only trade messages" />
                    Only trade messages
                </label>
            </div>
            {messages.length === 0 ? (
                <Empty>
                    <MessageSquare size={40} strokeWidth={1.5} />
                    <p>{onlyTrade ? 'No calls or follow-ups yet.' : 'No messages read yet.'}</p>
                </Empty>
            ) : (
                <div className={cn('flex flex-col gap-[8px]')}>
                    {messages.map(m => <MessageRow key={`${m.msg_id}-${m.sent_at}-${m.edited ? 'e' : ''}`} m={m} />)}
                </div>
            )}
            <div className={footerNote}><RefreshCw size={14} /> Refreshes every 10s · status {ago(st?.updated_at)}</div>
        </div>
    );
}
