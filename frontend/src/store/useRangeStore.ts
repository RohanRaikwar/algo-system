import { create } from 'zustand';
import type { RangeView } from '../types/range';

interface RangeState {
    view: RangeView | null;
    setView: (v: RangeView) => void;
}

/** Latest NIFTY50_RANGE view; each pub:range frame replaces the whole state. */
export const useRangeStore = create<RangeState>((set) => ({
    view: null,
    setView: (view) => set({ view }),
}));

/** Parses a pub:range payload (object or JSON string); null if not a range view. */
export function parseRangeView(data: unknown): RangeView | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || typeof (v as RangeView).regime !== 'string') return null;
    return v as RangeView;
}
