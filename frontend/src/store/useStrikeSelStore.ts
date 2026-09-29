import { create } from 'zustand';
import type { StrikeSelView } from '../types/strikesel';

interface StrikeSelState {
    view: StrikeSelView | null;
    setView: (v: StrikeSelView) => void;
}

/** Latest NIFTY50_SR strike selection; each pub:strikesel frame replaces it. */
export const useStrikeSelStore = create<StrikeSelState>((set) => ({
    view: null,
    setView: (view) => set({ view }),
}));

/** Parses a pub:strikesel payload (object or JSON string); null if not a view. */
export function parseStrikeSelView(data: unknown): StrikeSelView | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || typeof (v as StrikeSelView).params !== 'object') return null;
    return v as StrikeSelView;
}
