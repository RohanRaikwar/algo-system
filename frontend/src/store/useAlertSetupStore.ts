import { create } from 'zustand';
import { showAlertSetupAtStart } from '../services/alertSetup';

/** Whether the alert setup dialog is open: at start on Android until done,
 *  and again from the bell menu. */
interface AlertSetupStore {
    open: boolean;
    setOpen: (open: boolean) => void;
}

export const useAlertSetupStore = create<AlertSetupStore>(set => ({
    open: typeof navigator !== 'undefined' && showAlertSetupAtStart(),
    setOpen: open => set({ open }),
}));
