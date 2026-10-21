import { create } from 'zustand';
import { isStandalone } from '../services/push';
import { keepAwakeWanted, saveKeepAwake } from '../services/wakeLock';

/** Keep-awake choice (saved) and whether the screen lock is held now.
 *  App holds the lock; the header toggle flips the choice. */
interface WakeStore {
    want: boolean;
    held: boolean;
    setWant: (on: boolean) => void;
    setHeld: (held: boolean) => void;
}

export const useWakeStore = create<WakeStore>(set => ({
    want: keepAwakeWanted(isStandalone()),
    held: false,
    setWant: on => { saveKeepAwake(on); set({ want: on }); },
    setHeld: held => set({ held }),
}));
