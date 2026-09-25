import { useEffect, useRef } from 'react';
import { useWSStore } from '../store/useWSStore';
import { useAppStore } from '../store/useAppStore';
import { useCandleStore } from '../store/useCandleStore';
import { useLiveOrderStore } from '../store/useLiveOrderStore';
import { useSignalStore } from '../store/useSignalStore';
import { useStrikeStore } from '../store/useStrikeStore';
import { useAnalystStore } from '../store/useAnalystStore';
import { parseChannel, reconnectDelay, fmtTime, IST_OFFSET } from '../utils/helpers';
import {
    classifySeq, isMissedComplete, channelKind, shouldApplyBackfill, snapshotLiveUpdates,
    snapshotSignalUpdates, ConnectionEpochGate,
} from '../utils/seqTracker';
import {
    createConcurrencyLimiter, resyncJitterMs, SnapshotRequestTracker, ReconnectBackoff,
} from '../utils/requestControl';
import type {
    WSEnvelope, CandlePayload, IndicatorPayload,
    SubscribeMsg, IndicatorSpecMsg,
} from '../types/ws';
import type { LiveOrdersPayload, SignalPayload } from '../types/signal';

/**
 * Convert an IndicatorEntry to an IndicatorSpec for SUBSCRIBE.
 * E.g. {name:"SMA_9", tf:300} → { id: "sma", source: "close", params: { length: 9 }, tf: 300 }
 */
function entryToSpec(entry: { name: string; tf: number }): IndicatorSpecMsg {
    const parts = entry.name.split('_');
    const id = (parts[0] || 'sma').toLowerCase();
    const length = parseInt(parts[1] || '14', 10) || 14;
    return { id, source: 'close', params: { length }, tf: entry.tf };
}

let subIdCounter = 0;

// Minimum spacing between resync SUBSCRIBEs; extra requests inside the window
// collapse into one trailing resync.
const RESYNC_MIN_INTERVAL_MS = 2000;

// Max concurrent /api/missed backfill requests.
const MISSED_MAX_CONCURRENT = 4;

// SNAPSHOT retry: the gateway may wait up to 8s for new indicators before
// building, so the timeout sits above that.
const snapshotTracker = new SnapshotRequestTracker({ timeoutMs: 10000, maxRetries: 3 });

/**
 * Normalize channel-only envelopes into typed envelopes expected by existing
 * frontend consumers. This keeps backward compatibility when gateway sends
 * signal/pnl via channel fanout.
 */
export function normalizeRealtimeEnvelope(envelope: WSEnvelope): WSEnvelope {
    if (!envelope.type && envelope.channel === 'pub:signal') {
        return { ...envelope, type: 'signal' };
    }
    if (!envelope.type && envelope.channel === 'pub:pnl') {
        return { ...envelope, type: 'pnl' };
    }
    return envelope;
}

/**
 * Send a SUBSCRIBE message over the store-managed WebSocket.
 */
export function sendSubscribe(
    symbol: string,
    tf: number,
    entries: Array<{ name: string; tf: number }>,
    candleCount = 500,
    isRetry = false,
) {
    const { send } = useWSStore.getState();

    const indicators = entries.map(e => entryToSpec(e));
    const msg: SubscribeMsg = {
        type: 'SUBSCRIBE',
        reqId: `r${++subIdCounter}`,
        symbol,
        tf,
        history: { candles: candleCount },
        indicators,
    };

    send(JSON.stringify(msg));
    snapshotTracker.track(msg.reqId, () => sendSubscribe(symbol, tf, entries, candleCount, true), isRetry);
    console.log('[ws] SUBSCRIBE sent', msg);
}

/**
 * Send an UNSUBSCRIBE message to drop a prior subscription.
 */
export function sendUnsubscribe(symbol: string, tf: number) {
    const { send } = useWSStore.getState();
    const msg = {
        type: 'UNSUBSCRIBE',
        reqId: `r${++subIdCounter}`,
        symbol,
        tf,
    };
    send(JSON.stringify(msg));
    console.log('[ws] UNSUBSCRIBE sent', msg);
}

export function useWebSocket() {
    const localWsRef = useRef<WebSocket | null>(null);
    const pingRef = useRef<ReturnType<typeof setInterval> | null>(null);
    const subscribedRef = useRef(false);

    // Use atomic selectors — each grabs only its own setter function,
    // preventing unnecessary re-renders from unrelated state changes.
    const setConnected = useWSStore(s => s.setConnected);
    const incrementMsg = useWSStore(s => s.incrementMsg);
    const setLastMsgTS = useWSStore(s => s.setLastMsgTS);
    const setReconnectAttempts = useWSStore(s => s.setReconnectAttempts);
    const setWsDelay = useWSStore(s => s.setWsDelay);
    const setMarket = useWSStore(s => s.setMarket);
    const setMetrics = useWSStore(s => s.setMetrics);
    const setLastUpdateTime = useWSStore(s => s.setLastUpdateTime);
    const setLatency = useWSStore(s => s.setLatency);
    const setWsRef = useWSStore(s => s.setWsRef);

    const upsertCandle = useCandleStore(s => s.upsertCandle);
    const aggregateToTF = useCandleStore(s => s.aggregateToTF);
    const updateIndicator = useCandleStore(s => s.updateIndicator);
    const setSnapshot = useCandleStore(s => s.setSnapshot);

    useEffect(() => {
        let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
        const backoff = new ReconnectBackoff();
        const epochGate = new ConnectionEpochGate();
        const apiBase = import.meta.env.VITE_API_URL || '';
        let lastResyncAt = 0;
        let resyncTimer: ReturnType<typeof setTimeout> | null = null;
        let jitterTimer: ReturnType<typeof setTimeout> | null = null;
        let subscribeTimer: ReturnType<typeof setTimeout> | null = null;
        // Set on unmount; every deferred callback checks it before touching state.
        let disposed = false;
        const runMissed = createConcurrencyLimiter(MISSED_MAX_CONCURRENT);

        /**
         * Full resync: re-SUBSCRIBE (as on fresh connect) so the gateway sends a
         * new SNAPSHOT. Throttled; requests inside the window become one trailing resync.
         */
        function requestResync(reason: string) {
            if (disposed) return;
            const wait = lastResyncAt + RESYNC_MIN_INTERVAL_MS - Date.now();
            if (wait > 0) {
                if (!resyncTimer) {
                    resyncTimer = setTimeout(() => {
                        resyncTimer = null;
                        if (!disposed) requestResync(reason);
                    }, wait);
                }
                return;
            }
            lastResyncAt = Date.now();
            const state = useAppStore.getState();
            if (!state.selectedToken) return;
            console.warn(`[ws] resync (${reason}): requesting fresh SNAPSHOT`);
            sendSubscribe(state.selectedToken, state.selectedTF || 60, state.activeIndicators || []);
        }

        /**
         * Epoch-change resyncs hit every client at once; spread them over 0–3s.
         * One pending jittered resync at a time.
         */
        function requestJitteredResync(reason: string) {
            if (jitterTimer || disposed) return;
            jitterTimer = setTimeout(() => {
                jitterTimer = null;
                if (!disposed) requestResync(reason);
            }, resyncJitterMs());
        }

        /**
         * Adopt a gateway seq epoch. A change from a known epoch means stored seqs
         * are meaningless (gateway restart or lost upstream messages): reset them
         * and, if `resync`, request a full SNAPSHOT (jittered).
         * Within one connection, epochs older than the adopted one are ignored
         * (frames queued before a bump), so late frames can't flap the client
         * back. The first epoch on a new socket is always adopted.
         */
        function adoptEpoch(epoch: string | undefined, resync: boolean, reason: string) {
            if (!epoch) return;
            const stored = useWSStore.getState().epoch;
            if (epochGate.isStale(stored, epoch)) return;
            epochGate.pin();
            if (stored === epoch) return;
            useWSStore.getState().resetEpoch(epoch);
            if (stored !== null) {
                console.warn(`[ws] seq epoch changed ${stored} -> ${epoch} (${reason})`);
                if (resync) requestJitteredResync(`epoch change: ${reason}`);
            }
        }

        function backfillGap(channel: string, fromSeq: number, toSeq: number) {
            const epoch = useWSStore.getState().epoch;
            const epochParam = epoch ? `&epoch=${encodeURIComponent(epoch)}` : '';
            runMissed(() => fetch(`${apiBase}/api/missed?channel=${encodeURIComponent(channel)}&from_seq=${fromSeq}&to_seq=${toSeq}${epochParam}`)
                .then(r => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`)))))
                .then(data => {
                    if (disposed) return;
                    if (!isMissedComplete(data, fromSeq, toSeq)) {
                        // Replay buffer no longer holds the range (or epoch moved on).
                        requestResync(`backfill incomplete on ${channel} [${fromSeq},${toSeq}]`);
                        return;
                    }
                    const messages = data.messages as unknown[];
                    for (const raw of messages) {
                        try {
                            const missed = typeof raw === 'string' ? JSON.parse(raw) : raw;
                            if (missed && typeof missed === 'object') {
                                processEnvelope(missed as WSEnvelope, true);
                            }
                        } catch (err) {
                            console.warn('[ws] failed to parse missed envelope:', err);
                        }
                    }
                    if (messages.length > 0) {
                        console.log(`[ws] backfilled ${messages.length} missed messages for ${channel}`);
                    }
                })
                .catch(err => {
                    if (disposed) return;
                    console.warn('[ws] gap backfill failed:', err);
                    requestResync(`backfill failed on ${channel}`);
                });
        }

        function processEnvelope(rawEnvelope: WSEnvelope, fromBackfill = false) {
            const envelope = normalizeRealtimeEnvelope(rawEnvelope);

            // Frames from an epoch older than the adopted one were queued before a
            // bump; the bump's resync supersedes them. SNAPSHOTs still apply chart
            // data (handled below) but not their epoch or live state.
            const staleEpoch = epochGate.isStale(useWSStore.getState().epoch, envelope.epoch);
            if (staleEpoch && envelope.type !== 'SNAPSHOT') return;

            // Only the SNAPSHOT for the latest SUBSCRIBE is applied.
            if (envelope.type === 'SNAPSHOT' && !snapshotTracker.accept(envelope.reqId)) {
                console.log(`[ws] ignoring SNAPSHOT for superseded reqId ${envelope.reqId}`);
                return;
            }

            if (envelope.ts && !fromBackfill) {
                setLastMsgTS(envelope.ts);
                setLastUpdateTime(fmtTime(envelope.ts));
                const serverMs = new Date(envelope.ts).getTime();
                const lat = Date.now() - serverMs;
                if (!isNaN(lat) && lat >= 0 && lat < 30000) {
                    setLatency(lat);
                }
            }

            // Broadcast normalized envelopes for tabs/components.
            window.dispatchEvent(new CustomEvent('ws:message', { detail: envelope }));

            // ── SNAPSHOT response ──
            if (envelope.type === 'SNAPSHOT') {
                // The snapshot is itself the resync, so adopt its epoch without another one.
                adoptEpoch(envelope.epoch, false, 'snapshot');
                handleSnapshot(envelope, !staleEpoch);
                return;
            }

            // ── hello: gateway announces its seq epoch (on connect and after upstream resubscribe) ──
            if (envelope.type === 'hello') {
                // On connect, onopen already re-SUBSCRIBEs; other reasons need an explicit resync.
                adoptEpoch(envelope.epoch, envelope.reason !== 'connect', envelope.reason || 'hello');
                return;
            }

            // Sequenced envelope from a different epoch (e.g. missed hello): reset + resync.
            if (!fromBackfill && typeof envelope.channel_seq === 'number') {
                adoptEpoch(envelope.epoch, true, 'envelope');
            }

            // ── ERROR response ──
            if (envelope.type === 'ERROR') {
                console.error('[ws] server error:', envelope.error, 'reqId:', envelope.reqId);
                return;
            }

            // Ignore global config_update broadcasts so each tab keeps its own indicators.
            if (envelope.type === 'config_update') {
                return;
            }

            // ── Gap detection via channel_seq (epoch already reconciled above) ──
            // Runs for every sequenced channel. Only stream channels backfill gaps:
            // full-state channels are superseded by the message just received, and
            // ticks are latest-value only.
            if (envelope.channel && typeof envelope.channel_seq === 'number') {
                const { channelSeqs, epoch, setChannelSeq } = useWSStore.getState();
                const storedSeq = channelSeqs[envelope.channel] || 0;
                const receivedSeq = envelope.channel_seq;
                if (fromBackfill) {
                    // Don't let backfilled full state overwrite newer applied state.
                    if (!shouldApplyBackfill(envelope.channel, receivedSeq, storedSeq)) return;
                } else {
                    const verdict = classifySeq({ storedEpoch: epoch, storedSeq, epoch: envelope.epoch, seq: receivedSeq });

                    if (verdict === 'gap') {
                        if (channelKind(envelope.channel) === 'stream') {
                            console.warn(`[ws] gap detected on ${envelope.channel}: expected=${storedSeq + 1} got=${receivedSeq}`);
                            backfillGap(envelope.channel, storedSeq + 1, receivedSeq - 1);
                        }
                    } else if (verdict === 'regression' || verdict === 'epoch_change') {
                        console.warn(`[ws] seq ${verdict} on ${envelope.channel}: stored=${storedSeq} got=${receivedSeq}`);
                        requestResync(`seq ${verdict} on ${envelope.channel}`);
                    }
                    if (verdict !== 'duplicate') {
                        setChannelSeq(envelope.channel, receivedSeq);
                    }
                }
            }

            // ── Strike Info (pub:strike channel) ──
            if (envelope.channel === 'pub:strike' && envelope.data) {
                let strikeData = envelope.data;
                if (typeof strikeData === 'string') {
                    try { strikeData = JSON.parse(strikeData); } catch { /* ignore */ }
                }
                if (strikeData && (strikeData as Record<string, unknown>).resolved) {
                    useStrikeStore.getState().setStrike(strikeData as import('../store/useStrikeStore').StrikeInfo);
                }
                return;
            }

            // ── Market Analyst Events (pub:analyst:*) ──
            if (envelope.channel?.startsWith('pub:analyst:') && envelope.data) {
                let analystData = envelope.data;
                if (typeof analystData === 'string') {
                    try { analystData = JSON.parse(analystData); } catch { /* ignore */ }
                }
                const store = useAnalystStore.getState();
                if (envelope.channel === 'pub:analyst:state') {
                    store.setMarketState(analystData as import('../types/analyst').MarketStateEvent);
                } else if (envelope.channel === 'pub:analyst:levels') {
                    const evt = analystData as import('../types/analyst').LevelUpdateEvent;
                    const enrichedLevels = (evt.levels || []).map(l => ({ ...l, token: evt.token, exchange: evt.exchange }));
                    store.setLevels(enrichedLevels);
                } else if (envelope.channel === 'pub:analyst:breakout') {
                    store.setBreakout(analystData as import('../types/analyst').BreakoutEvent);
                }
                return;
            }

            // ── Live Order State (pub:orders channel) ──
            if (envelope.channel === 'pub:orders' && envelope.data) {
                let orderData = envelope.data;
                if (typeof orderData === 'string') {
                    try { orderData = JSON.parse(orderData); } catch { /* ignore */ }
                }
                const orders = (orderData as LiveOrdersPayload)?.orders;
                useLiveOrderStore.getState().setOrders(Array.isArray(orders) ? orders : []);
                return;
            }

            // ── Strategy Signals ──
            if (envelope.type === 'signal' && envelope.data) {
                const sig = envelope.data as SignalPayload;
                useSignalStore.getState().addLiveSignal(sig);
                return;
            }

            // ── Metrics ──
            if (envelope.type === 'metrics' && envelope.metrics) {
                setMetrics(envelope.metrics);
                setMarket(!!envelope.marketOpen, envelope.marketStatus || '');
                return;
            }

            // ── Pong ──
            if (envelope.type === 'pong' && envelope.ping) {
                setWsDelay(Date.now() - envelope.ping);
                return;
            }

            // PnL messages are consumed by ws:message listeners.
            if (envelope.type === 'pnl') {
                return;
            }

            // ── Data messages (legacy channel-based + new protocol) ──
            if (!envelope.channel) return;
            const parsed = parseChannel(envelope.channel);
            if (!parsed) return;

            let payload: Record<string, unknown>;
            if (typeof envelope.data === 'string') {
                try { payload = JSON.parse(envelope.data as string); } catch { payload = envelope.data as unknown as Record<string, unknown>; }
            } else {
                payload = envelope.data as Record<string, unknown>;
            }

            // ── Update FNO LTP from tick data ──
            if (parsed.type === 'tick' || parsed.type === 'candle') {
                const tickPayload = payload as Record<string, unknown>;
                const tickToken = (tickPayload.token as string) || '';
                const tickPrice = (tickPayload.price as number) || (tickPayload.close as number) || 0;
                if (tickToken && tickPrice > 0) {
                    const strikeState = useStrikeStore.getState();
                    if (strikeState.strike?.call?.token === tickToken) {
                        strikeState.setCallLTP(tickPrice);
                    }
                    if (strikeState.strike?.put?.token === tickToken) {
                        strikeState.setPutLTP(tickPrice);
                    }
                }
            }

            if (parsed.type === 'candle') {
                const d = payload as unknown as CandlePayload;
                const tf = d.tf || parsed.tf || 0;
                upsertCandle(tf, {
                    ts: d.ts, open: d.open, high: d.high, low: d.low,
                    close: d.close, volume: d.volume, count: d.count,
                    forming: d.forming, exchange: d.exchange, token: d.token,
                });
                // Aggregate 1s candles into other TFs
                if (tf === 1 || parsed.tf === 1) {
                    const activeTFs = useAppStore.getState().config.tfs || [60];
                    aggregateToTF(activeTFs, {
                        ts: d.ts, open: d.open, high: d.high, low: d.low,
                        close: d.close, volume: d.volume, count: d.count,
                        forming: true, exchange: d.exchange, token: d.token,
                    });
                }
            }

            if (parsed.type === 'indicator') {
                const d = payload as unknown as IndicatorPayload;
                const key = (d.name || parsed.name || '') + ':' + (d.tf || parsed.tf || 0);
                updateIndicator(
                    key, d.name || parsed.name || '', d.tf || parsed.tf || 0,
                    d.value, d.ts, d.ready, !!d.live, d.exchange, d.token
                );
            }
        }

        function connect() {
            if (disposed) return;
            const apiUrl = import.meta.env.VITE_API_URL || '';
            let url: string;
            if (apiUrl) {
                // Convert http(s)://host:port to ws(s)://host:port/ws
                url = apiUrl.replace(/^http/, 'ws') + '/ws';
            } else {
                // Same-origin (dev mode with Vite proxy)
                const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
                url = `${proto}//${location.host}/ws`;
            }
            const currentLastTS = useWSStore.getState().lastMsgTS;
            if (currentLastTS) {
                url += `?last_ts=${encodeURIComponent(currentLastTS)}`;
            }

            const ws = new WebSocket(url);
            localWsRef.current = ws;
            setWsRef(ws); // Store-managed WS ref

            ws.onopen = () => {
                setConnected(true);
                // Backoff resets only once this socket has stayed open a while.
                backoff.opened(Date.now());
                epochGate.reset();
                setReconnectAttempts(0);
                subscribedRef.current = false;

                // Auto-subscribe with current state (global indicators)
                const state = useAppStore.getState();
                const token = state.selectedToken;
                const tf = state.selectedTF || 60;
                const entries = state.activeIndicators || [];

                if (token) {
                    lastResyncAt = Date.now(); // this SUBSCRIBE is the connect-time resync
                    subscribeTimer = setTimeout(() => {
                        subscribeTimer = null;
                        if (!disposed) sendSubscribe(token, tf, entries);
                    }, 100);
                    subscribedRef.current = true;
                }
            };

            ws.onclose = () => {
                if (disposed) return;
                setConnected(false);
                setWsRef(null);
                subscribedRef.current = false;
                const attempts = backoff.closed(Date.now());
                setReconnectAttempts(attempts);
                const delay = reconnectDelay(attempts);
                reconnectTimer = setTimeout(connect, delay);
            };

            ws.onerror = () => {
                // onclose will handle reconnection
            };

            ws.onmessage = (evt) => {
                // Server may batch multiple JSON objects with newline separators
                const rawData: string = evt.data;
                const lines = rawData.indexOf('\n') >= 0 ? rawData.split('\n') : [rawData];

                for (const line of lines) {
                    if (!line) continue;

                    incrementMsg();
                    try {
                        const envelope: WSEnvelope = JSON.parse(line);
                        processEnvelope(envelope);
                    } catch (e) {
                        console.warn('[ws] parse error', e);
                    }
                }
            };
        }

        /**
         * Handle SNAPSHOT response: bulk-set candles + indicator histories.
         */
        function handleSnapshot(envelope: WSEnvelope, applyLiveState = true) {
            const tf = envelope.tf || 60;
            const symbol = envelope.symbol || '';
            const parts = symbol.split(':');
            const exchange = parts[0] || '';
            const token = parts[1] || '';

            // Convert snapshot candles to CandleRaw format
            const candles = (envelope.candles || []).map(c => ({
                ts: c.ts,
                open: c.open,
                high: c.high,
                low: c.low,
                close: c.close,
                volume: c.volume,
                count: c.count || 0,
                forming: false,
                exchange,
                token,
            }));

            // Convert snapshot indicator points to { time, value } arrays
            const indHistories: Record<string, Array<{ time: number; value: number }>> = {};
            if (envelope.indicators) {
                for (const [name, points] of Object.entries(envelope.indicators)) {
                    indHistories[name] = (points || [])
                        .filter(p => p.ready && p.ts)
                        .map(p => ({
                            time: Math.floor(new Date(p.ts).getTime() / 1000) + IST_OFFSET,
                            value: p.value,
                        }))
                        .sort((a, b) => a.time - b.time);
                }
            }

            setSnapshot(tf, candles, indHistories, token, exchange);
            console.log(
                `[ws] SNAPSHOT applied: tf=${tf} candles=${candles.length} indicators=${Object.keys(indHistories).length}`,
            );

            // Fetch initial Analyst states appended to snapshot
            const analystStore = useAnalystStore.getState();
            if (envelope.analystState) {
                analystStore.setMarketState(envelope.analystState);
            }
            if (envelope.analystLevels) {
                const evt = envelope.analystLevels;
                const enrichedLevels = (evt.levels || []).map(l => ({ ...l, token: evt.token, exchange: evt.exchange }));
                analystStore.setLevels(enrichedLevels);
            }
            if (envelope.analystBreakout) {
                analystStore.setBreakout(envelope.analystBreakout);
            }

            // Orders + P&L so a resync recovers them even if live frames were lost.
            // Skipped for stale-epoch snapshots (their seqs don't compare) and for
            // state older than what live frames already applied.
            if (applyLiveState) {
                const { channelSeqs, setChannelSeq } = useWSStore.getState();
                for (const u of snapshotLiveUpdates(envelope, channelSeqs)) {
                    if (u.channel === 'pub:orders') {
                        const orders = (u.data as LiveOrdersPayload)?.orders;
                        useLiveOrderStore.getState().setOrders(Array.isArray(orders) ? orders : []);
                    } else if (u.channel === 'pub:pnl') {
                        // PnL is consumed by ws:message listeners.
                        window.dispatchEvent(new CustomEvent('ws:message', {
                            detail: { type: 'pnl', channel: 'pub:pnl', data: u.data },
                        }));
                    }
                    if (u.seq > 0) setChannelSeq(u.channel, u.seq);
                }

                // Recent signals, so one lost to a slow-client disconnect is recovered.
                const signalSeq = useWSStore.getState().channelSeqs['pub:signal'] || 0;
                const recovered = snapshotSignalUpdates(envelope.signals, signalSeq, useSignalStore.getState().signals);
                for (const sig of recovered) {
                    // The (non-stale) snapshot vouches for these; a signal pushed
                    // before an epoch bump must not be dropped as stale.
                    processEnvelope({ ...sig, epoch: undefined } as WSEnvelope, true);
                }
                if (recovered.length > 0) {
                    setChannelSeq('pub:signal', recovered[recovered.length - 1].channel_seq);
                    console.log(`[ws] recovered ${recovered.length} signals from SNAPSHOT`);
                }
            }
        }

        connect();

        // Ping every 5s
        pingRef.current = setInterval(() => {
            const ws = useWSStore.getState().wsRef;
            if (ws?.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({ ping: Date.now() }));
            }
        }, 5000);

        return () => {
            disposed = true;
            if (reconnectTimer) clearTimeout(reconnectTimer);
            if (resyncTimer) clearTimeout(resyncTimer);
            if (jitterTimer) clearTimeout(jitterTimer);
            if (subscribeTimer) clearTimeout(subscribeTimer);
            snapshotTracker.clear();
            if (pingRef.current) clearInterval(pingRef.current);
            if (localWsRef.current) {
                localWsRef.current.onclose = null; // prevent reconnect on unmount
                localWsRef.current.onmessage = null;
                localWsRef.current.close();
            }
            setWsRef(null);
        };
    }, []); // connect once on mount
}
