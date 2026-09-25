import { create } from 'zustand';
import type { SystemMetrics } from '../types/ws';

/** One Health-page history point, derived from a metrics update. */
export interface MetricsSample {
    at: number;                 // local receive time (ms)
    tickRate: number | null;    // ticks/s since the previous sample
    p95: number | null;         // tick pipeline p95 latency (ms)
}

/** 5 minutes at the gateway's 2s cadence. */
export const METRICS_HISTORY_LEN = 150;

/** Append a sample for `next`, deriving tick rate from the previous update. */
export function appendMetricsSample(
    history: MetricsSample[],
    prev: SystemMetrics | null,
    prevAt: number | null,
    next: SystemMetrics,
    at: number,
): MetricsSample[] {
    const total = next.pipeline?.ticks_total;
    const prevTotal = prev?.pipeline?.ticks_total;
    let tickRate: number | null = null;
    if (total !== undefined && prevTotal !== undefined && prevAt !== null && at > prevAt && total >= prevTotal) {
        tickRate = (total - prevTotal) / ((at - prevAt) / 1000);
    }
    const p95 = next.e2e_latency_p95_ms && next.e2e_latency_p95_ms > 0 ? next.e2e_latency_p95_ms : null;
    const out = history.length >= METRICS_HISTORY_LEN ? history.slice(history.length - METRICS_HISTORY_LEN + 1) : history.slice();
    out.push({ at, tickRate, p95 });
    return out;
}

interface WSState {
    connected: boolean;
    msgCount: number;
    startTime: number;
    lastMsgTS: string | null;
    reconnectAttempts: number;
    wsDelay: number | null;
    marketOpen: boolean;
    marketStatus: string;
    metrics: SystemMetrics | null;
    /** Local time the latest metrics update arrived (ms), for staleness. */
    metricsAt: number | null;
    metricsHistory: MetricsSample[];
    lastUpdateTime: string | null;
    latency: number | null;

    // Per-channel sequence tracking for gap detection
    channelSeqs: Record<string, number>;
    // Gateway seq epoch the stored channelSeqs belong to (null = none seen yet)
    epoch: string | null;

    // WebSocket instance managed by the store (replaces module-level global)
    wsRef: WebSocket | null;

    setConnected: (c: boolean) => void;
    incrementMsg: () => void;
    setLastMsgTS: (ts: string) => void;
    setReconnectAttempts: (n: number) => void;
    setWsDelay: (d: number) => void;
    setMarket: (open: boolean, status: string) => void;
    setMetrics: (m: SystemMetrics) => void;
    setLastUpdateTime: (t: string) => void;
    setLatency: (l: number) => void;
    setWsRef: (ws: WebSocket | null) => void;
    setChannelSeq: (channel: string, seq: number) => void;
    /** Switch to a new seq epoch, discarding all stored per-channel seqs. */
    resetEpoch: (epoch: string | null) => void;
    /** Send a message over the current WebSocket connection */
    send: (data: string) => void;
}

export const useWSStore = create<WSState>((set, get) => ({
    connected: false,
    msgCount: 0,
    startTime: Date.now(),
    lastMsgTS: null,
    reconnectAttempts: 0,
    wsDelay: null,
    marketOpen: false,
    marketStatus: '',
    metrics: null,
    metricsAt: null,
    metricsHistory: [],
    lastUpdateTime: null,
    latency: null,
    wsRef: null,
    channelSeqs: {},
    epoch: null,

    setConnected: (connected) => set({ connected }),
    incrementMsg: () => set((s) => ({ msgCount: s.msgCount + 1 })),
    setLastMsgTS: (lastMsgTS) => set({ lastMsgTS }),
    setReconnectAttempts: (reconnectAttempts) => set({ reconnectAttempts }),
    setWsDelay: (wsDelay) => set({ wsDelay }),
    setMarket: (marketOpen, marketStatus) => set({ marketOpen, marketStatus }),
    setMetrics: (metrics) => set((s) => {
        const at = Date.now();
        return {
            metrics,
            metricsAt: at,
            metricsHistory: appendMetricsSample(s.metricsHistory, s.metrics, s.metricsAt, metrics, at),
        };
    }),
    setLastUpdateTime: (lastUpdateTime) => set({ lastUpdateTime }),
    setLatency: (latency) => set({ latency }),
    setWsRef: (wsRef) => set({ wsRef }),
    setChannelSeq: (channel, seq) => set((s) => ({
        channelSeqs: { ...s.channelSeqs, [channel]: seq },
    })),
    resetEpoch: (epoch) => set({ epoch, channelSeqs: {} }),
    send: (data) => {
        const ws = get().wsRef;
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(data);
        } else {
            console.warn('[ws] cannot send: not connected');
        }
    },
}));

