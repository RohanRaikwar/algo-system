import { create } from 'zustand';
import type { RefusedEntry, RefusedView } from '../types/refused';

interface RefusedState {
    entries: RefusedEntry[];
    setView: (v: RefusedView) => void;
}

/** Today's refused entries; each pub:refused frame replaces the whole list. */
export const useRefusedStore = create<RefusedState>((set) => ({
    entries: [],
    setView: (v) => set({ entries: v.entries }),
}));

/** Parses a pub:refused payload (object or JSON string); null if not a refused view. */
export function parseRefusedView(data: unknown): RefusedView | null {
    let v = data;
    if (typeof v === 'string') {
        try { v = JSON.parse(v); } catch { return null; }
    }
    if (!v || typeof v !== 'object' || !Array.isArray((v as RefusedView).entries)) return null;
    return v as RefusedView;
}
