/**
 * Client-side request pacing for resync: jitter, concurrency caps and
 * SNAPSHOT request retry tracking.
 */

export const RESYNC_JITTER_MAX_MS = 3000;

/** Random delay in [0, 3000) ms so many clients don't resync in lockstep after a hello. */
export function resyncJitterMs(random: () => number = Math.random): number {
    return Math.floor(random() * RESYNC_JITTER_MAX_MS);
}

/** Returns a runner that allows at most `max` tasks in flight; the rest queue FIFO. */
export function createConcurrencyLimiter(max: number) {
    let active = 0;
    const queue: Array<() => void> = [];
    const next = () => {
        if (active >= max) return;
        const start = queue.shift();
        if (start) start();
    };
    return function run<T>(task: () => Promise<T>): Promise<T> {
        return new Promise<T>((resolve, reject) => {
            queue.push(() => {
                active++;
                task().then(resolve, reject).finally(() => { active--; next(); });
            });
            next();
        });
    };
}

interface TrackerOptions {
    timeoutMs: number;
    maxRetries: number;
}

/**
 * Tracks the latest SUBSCRIBE reqId per subscription key ("symbol:tf"). If
 * its SNAPSHOT doesn't arrive within timeoutMs (e.g. dropped by a full server
 * queue) it calls `retry`, at most maxRetries times per request. Only the
 * SNAPSHOT for a key's latest reqId is accepted, so several charts can each
 * hold a subscription without discarding each other's snapshots.
 */
export class SnapshotRequestTracker {
    private slots = new Map<string, { latest: string; attempts: number; timer: ReturnType<typeof setTimeout> | null }>();
    private readonly opts: TrackerOptions;

    constructor(opts: TrackerOptions) {
        this.opts = opts;
    }

    /** Record a sent SUBSCRIBE. `isRetry` keeps the retry budget of the request being retried. */
    track(reqId: string, retry: () => void, isRetry = false, key = '') {
        const prev = this.slots.get(key);
        if (prev?.timer) clearTimeout(prev.timer);
        const slot = { latest: reqId, attempts: isRetry && prev ? prev.attempts : 0, timer: null as ReturnType<typeof setTimeout> | null };
        this.slots.set(key, slot);
        slot.timer = setTimeout(() => {
            slot.timer = null;
            if (slot.attempts >= this.opts.maxRetries) {
                console.warn(`[ws] SNAPSHOT for ${reqId} not received after ${slot.attempts} retries`);
                return;
            }
            slot.attempts++;
            console.warn(`[ws] SNAPSHOT for ${reqId} timed out; retry ${slot.attempts}/${this.opts.maxRetries}`);
            retry();
        }, this.opts.timeoutMs);
    }

    /** Whether a SNAPSHOT with this reqId should be applied; accepting stops retries. */
    accept(reqId: string | undefined, key = ''): boolean {
        if (!reqId) return true;
        const slot = this.slots.get(key);
        if (!slot || reqId !== slot.latest) return false;
        if (slot.timer) {
            clearTimeout(slot.timer);
            slot.timer = null;
        }
        return true;
    }

    /** Cancel any pending retry (e.g. on unmount). */
    clear() {
        for (const slot of this.slots.values()) {
            if (slot.timer) clearTimeout(slot.timer);
            slot.timer = null;
        }
    }
}

/** A connection must stay open this long before the reconnect backoff resets. */
export const RECONNECT_STABLE_MS = 10000;

/**
 * Reconnect attempt counter for backoff. Attempts reset only after a
 * connection stayed open for `stableMs`: a gateway that drops the socket right
 * after open (e.g. slow-client disconnect) keeps backing off instead of
 * reconnecting every 0.5–1s.
 */
export class ReconnectBackoff {
    private attempts = 0;
    private openedAt: number | null = null;
    private readonly stableMs: number;

    constructor(stableMs = RECONNECT_STABLE_MS) {
        this.stableMs = stableMs;
    }

    opened(now: number) {
        this.openedAt = now;
    }

    /** Record a close; returns the attempt number for the next reconnect delay. */
    closed(now: number): number {
        if (this.openedAt !== null && now - this.openedAt >= this.stableMs) this.attempts = 0;
        this.openedAt = null;
        return ++this.attempts;
    }
}
