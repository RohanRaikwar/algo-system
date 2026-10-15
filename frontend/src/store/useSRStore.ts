import { create } from 'zustand';
import type { SRView } from '../types/sr';
import { useAppStore } from './useAppStore';

interface SRState {
    /** NIFTY50_SR (pub:sr). */
    view: SRView | null;
    /** SR on other underlyings (SENSEX_SR, …) by index key (pub:srx). */
    extra: Record<string, SRView>;
    setView: (v: SRView) => void;
    setExtra: (views: Record<string, SRView>) => void;
}

/** Latest SR views; each pub:sr / pub:srx frame replaces its whole state. */
export const useSRStore = create<SRState>((set) => ({
    view: null,
    extra: {},
    setView: (view) => set({ view }),
    setExtra: (extra) => set({ extra }),
}));

/** The SR view for an index key ("BSE:99919000"), whichever strategy runs on it. */
export function useSRViewFor(key: string | null): SRView | null {
    return useSRStore(s => {
        if (!key) return null;
        if (s.view && s.view.key === key) return s.view;
        return s.extra[key] ?? null;
    });
}

/** The SR view of the chart's selected index; NIFTY's when that index has none. */
export function useSelectedSRView(): SRView | null {
    const token = useAppStore(s => s.selectedToken);
    return useSRStore(s => (token && s.extra[token]) || s.view);
}

/** Parses a pub:sr payload (object or JSON string); null if not an SR view. */
export function parseSRView(data: unknown): SRView | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || typeof (v as SRView).regime !== 'string' || typeof (v as SRView).side !== 'string') return null;
    return v as SRView;
}

/** Parses a pub:srx payload: {"BSE:99919000": view}; invalid entries are dropped. */
export function parseSRXViews(data: unknown): Record<string, SRView> | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || Array.isArray(v)) return null;
    const out: Record<string, SRView> = {};
    for (const [key, raw] of Object.entries(v as Record<string, unknown>)) {
        const view = parseSRView(raw);
        if (view) out[key] = view;
    }
    return out;
}
