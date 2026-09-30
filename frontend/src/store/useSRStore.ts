import { create } from 'zustand';
import type { SRView } from '../types/sr';

interface SRState {
    view: SRView | null;
    setView: (v: SRView) => void;
}

/** Latest NIFTY50_SR view; each pub:sr frame replaces the whole state. */
export const useSRStore = create<SRState>((set) => ({
    view: null,
    setView: (view) => set({ view }),
}));

/** Parses a pub:sr payload (object or JSON string); null if not an SR view. */
export function parseSRView(data: unknown): SRView | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || typeof (v as SRView).regime !== 'string' || typeof (v as SRView).side !== 'string') return null;
    return v as SRView;
}
