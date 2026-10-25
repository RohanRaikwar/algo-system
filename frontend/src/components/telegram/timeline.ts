import type { TelegramMessage } from '../../services/api';

/** Steps shown in the latency row, with their labels. Later events (stop moves, exits) stay in "Trade:". */
const STAGES: Record<string, string> = {
    ARMED: 'armed',
    REPLACED: 'armed',
    REFUSED: 'refused',
    FIRST_TICK: '1st tick',
    ENTERED: 'entered',
    FILLED: 'filled',
    ENTRY_REFUSED: 'entry refused',
    DROPPED: 'dropped',
};

/** A duration: "450ms", "2.1s", "41s", "3m 05s". */
export function span(ms: number): string {
    if (ms < 1000) return `${Math.max(0, Math.round(ms))}ms`;
    if (ms < 10_000) return `${(ms / 1000).toFixed(1)}s`;
    if (ms < 60_000) return `${Math.round(ms / 1000)}s`;
    const s = Math.round(ms / 1000);
    return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}

/**
 * The call's latency steps: Telegram send → receipt (send time is to the
 * second), the read, then what stratengine did, each as time since receipt.
 * Empty when the message carries no stamps (older build).
 */
export function timelineParts(m: TelegramMessage): string[] {
    const parts: string[] = [];
    const recv = m.received_at ? Date.parse(m.received_at) : NaN;
    const sent = Date.parse(m.sent_at);
    if (!Number.isNaN(recv) && new Date(recv).getFullYear() >= 2000) {
        if (!Number.isNaN(sent)) parts.push(`recv +${span(recv - sent)}`);
        if (m.read_ms) parts.push(`read ${span(m.read_ms)}`);
    }
    const seen = new Set<string>();
    for (const st of m.timeline ?? []) {
        const label = STAGES[st.event];
        if (!label || seen.has(label)) continue;
        seen.add(label);
        parts.push(`${label} +${span(st.ms)}`);
    }
    return parts;
}
