/**
 * Pure helpers for per-channel sequence tracking against the gateway's
 * channel_seq + epoch contract.
 *
 * The gateway starts a new epoch on process restart and whenever its Redis
 * subscription is re-established. Seqs are only comparable within one epoch.
 */

export type SeqVerdict =
    | 'ok'            // expected next seq (or first seen on this channel)
    | 'gap'           // seq skipped ahead — backfill via /api/missed
    | 'duplicate'     // same seq again — ignore
    | 'regression'    // seq went backwards within the same epoch — resync
    | 'epoch_change'; // new sequence space — reset all seqs and resync

export interface SeqInput {
    storedEpoch: string | null;
    storedSeq: number;          // 0 = nothing stored for this channel
    epoch: string | undefined;  // undefined = legacy gateway without epoch
    seq: number;
}

export function classifySeq({ storedEpoch, storedSeq, epoch, seq }: SeqInput): SeqVerdict {
    if (epoch && storedEpoch && epoch !== storedEpoch) return 'epoch_change';
    if (storedSeq <= 0) return 'ok';
    if (seq === storedSeq + 1) return 'ok';
    // Seq 1 only means "channel (re)created": the gateway evicts idle channels and
    // restarts them at 1 in the same epoch, with nothing lost in between.
    if (seq === 1 && storedSeq > 1) return 'ok';
    if (seq > storedSeq + 1) return 'gap';
    if (seq === storedSeq) return 'duplicate';
    return 'regression';
}

/**
 * Decide whether an /api/missed response covers [fromSeq, toSeq] fully.
 * Uses the gateway's explicit `complete` flag; older gateways lack it, so
 * fall back to counting messages (per-channel seqs are contiguous).
 */
export function isMissedComplete(resp: unknown, fromSeq: number, toSeq: number): boolean {
    if (!resp || typeof resp !== 'object') return false;
    const r = resp as { complete?: unknown; messages?: unknown };
    if (!Array.isArray(r.messages)) return false;
    if (typeof r.complete === 'boolean') return r.complete;
    return r.messages.length === toSeq - fromSeq + 1;
}

/**
 * Start time (ms) encoded in an epoch's hex prefix ("<ms hex>-<random>"),
 * or null if it can't be parsed. The gateway makes it strictly increasing.
 */
export function epochStartMs(epoch: string | null | undefined): number | null {
    if (!epoch) return null;
    const prefix = epoch.split('-', 1)[0];
    if (!/^[0-9a-f]+$/i.test(prefix)) return null;
    const ms = parseInt(prefix, 16);
    return Number.isFinite(ms) ? ms : null;
}

/**
 * True when `incoming` is an epoch older than the adopted `stored` one: such
 * frames were queued before a bump and must be ignored, not adopted (which
 * would flap between epochs). Unparsable epochs are never considered stale.
 */
export function isStaleEpoch(stored: string | null, incoming: string | undefined): boolean {
    if (!stored || !incoming || stored === incoming) return false;
    const s = epochStartMs(stored);
    const i = epochStartMs(incoming);
    return s !== null && i !== null && i < s;
}

/**
 * Per-connection epoch staleness. Epoch start-ms only orders epochs of one
 * gateway process; a restarted gateway (other host, NTP step) may start with a
 * lower prefix. So the first epoch seen on each new socket (its hello) is
 * adopted unconditionally, and stale checks apply only once one is pinned.
 */
export class ConnectionEpochGate {
    private pinned = false;

    /** New socket: the next epoch seen is adopted whatever its ordering. */
    reset() {
        this.pinned = false;
    }

    /** Record that this connection has adopted (or confirmed) an epoch. */
    pin() {
        this.pinned = true;
    }

    isStale(stored: string | null, incoming: string | undefined): boolean {
        return this.pinned && isStaleEpoch(stored, incoming);
    }
}

/**
 * How a channel's messages relate to each other:
 * - full_state: each message replaces the whole state (latest wins; no backfill needed)
 * - latest_only: live value where intermediate values don't matter (ticks; no backfill)
 * - stream: every message matters (candles, indicators, signals; backfill gaps)
 */
export type ChannelKind = 'full_state' | 'latest_only' | 'stream';

export function channelKind(channel: string): ChannelKind {
    if (channel === 'pub:orders' || channel === 'pub:pnl' || channel === 'pub:strike' || channel.startsWith('pub:analyst:')) {
        return 'full_state';
    }
    if (channel.startsWith('pub:tick:')) return 'latest_only';
    return 'stream';
}

/**
 * Whether a backfilled envelope should be applied: full-state channels skip
 * anything not newer than the latest applied seq so old state can't overwrite new.
 */
export function shouldApplyBackfill(channel: string, seq: number, latestSeq: number): boolean {
    return channelKind(channel) !== 'full_state' || seq > latestSeq;
}

export interface SnapshotLiveState {
    orders?: unknown;
    pnl?: unknown;
    liveSeqs?: Record<string, number>;
}

/**
 * Full-state updates carried by a SNAPSHOT that should be applied, given the
 * latest live seqs already applied. Seq 0 means the gateway read the state from
 * Redis without a seq; it is only applied if nothing live was seen yet.
 */
export function snapshotLiveUpdates(
    snap: SnapshotLiveState,
    channelSeqs: Record<string, number>,
): Array<{ channel: string; data: unknown; seq: number }> {
    const out: Array<{ channel: string; data: unknown; seq: number }> = [];
    const fields: Array<[string, unknown]> = [['pub:orders', snap.orders], ['pub:pnl', snap.pnl]];
    for (const [channel, data] of fields) {
        if (data === undefined || data === null) continue;
        const seq = snap.liveSeqs?.[channel] ?? 0;
        const latest = channelSeqs[channel] || 0;
        if (seq === 0 ? latest > 0 : seq < latest) continue;
        out.push({ channel, data, seq });
    }
    return out;
}

export interface SignalEnvelope {
    channel?: string;
    channel_seq: number;
    epoch?: string;
    data: unknown;
}

interface SignalIdentity {
    strategy?: string;
    action?: string;
    token?: string;
    candle_ts?: string;
}

/**
 * Recent pub:signal envelopes carried by a SNAPSHOT that have not been seen:
 * seq above the latest applied one and not already in the store (seqs reset
 * on an epoch change, so identity guards against re-adding). Seq order.
 */
export function snapshotSignalUpdates(
    signals: unknown[] | undefined,
    latestSeq: number,
    existing: SignalIdentity[],
): SignalEnvelope[] {
    if (!Array.isArray(signals)) return [];
    const seen = new Set(existing.map(s => `${s.strategy}|${s.action}|${s.token}|${s.candle_ts}`));
    const out: SignalEnvelope[] = [];
    for (const raw of signals) {
        if (!raw || typeof raw !== 'object') continue;
        const env = raw as Partial<SignalEnvelope>;
        if (typeof env.channel_seq !== 'number' || !env.data || typeof env.data !== 'object') continue;
        if (env.channel_seq <= latestSeq) continue;
        const d = env.data as { strategy_name?: string; action?: string; token?: string; ts?: string };
        if (seen.has(`${d.strategy_name}|${d.action}|${d.token}|${d.ts}`)) continue;
        out.push(env as SignalEnvelope);
    }
    return out.sort((a, b) => a.channel_seq - b.channel_seq);
}
