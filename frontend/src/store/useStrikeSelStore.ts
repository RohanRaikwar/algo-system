import { create } from 'zustand';
import type { StrikeSelView } from '../types/strikesel';
import { useAppStore } from './useAppStore';

interface StrikeSelState {
    /** NIFTY50_SR (pub:strikesel). */
    view: StrikeSelView | null;
    /** Other underlyings (SENSEX_SR, …) by index key (pub:strikeselx). */
    extra: Record<string, StrikeSelView>;
    setView: (v: StrikeSelView) => void;
    setExtra: (views: Record<string, StrikeSelView>) => void;
}

/** Latest SR strike selections; each frame replaces its whole state. */
export const useStrikeSelStore = create<StrikeSelState>((set) => ({
    view: null,
    extra: {},
    setView: (view) => set({ view }),
    setExtra: (extra) => set({ extra }),
}));

/** The strike selection of the chart's selected index; NIFTY's otherwise. */
export function useSelectedStrikeSel(): StrikeSelView | null {
    const token = useAppStore(s => s.selectedToken);
    return useStrikeSelStore(s => (token && s.extra[token]) || s.view);
}

/** Parses a pub:strikesel payload (object or JSON string); null if not a view. */
export function parseStrikeSelView(data: unknown): StrikeSelView | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || typeof (v as StrikeSelView).params !== 'object') return null;
    return v as StrikeSelView;
}

/** Parses a pub:strikeselx payload: {"BSE:99919000": view}. */
export function parseStrikeSelXViews(data: unknown): Record<string, StrikeSelView> | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || Array.isArray(v)) return null;
    const out: Record<string, StrikeSelView> = {};
    for (const [key, raw] of Object.entries(v as Record<string, unknown>)) {
        const view = parseStrikeSelView(raw);
        if (view) out[key] = view;
    }
    return out;
}
