import { create } from 'zustand';
import type { PushState } from '../services/push';

/** This device's Web Push state, shared by the header bell and the
 *  open-time permission request in App. null until first checked. */
interface PushStore {
    state: PushState | null;
    setState: (s: PushState) => void;
}

export const usePushStore = create<PushStore>(set => ({
    state: null,
    setState: s => set({ state: s }),
}));
