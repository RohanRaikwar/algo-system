import { create } from 'zustand';

/** `/api/market-status` response. */
export interface MarketStatus {
    isOpen: boolean;
    isHoliday: boolean;
    holidayName: string;
    nextOpenLabel: string;
}

interface MarketStatusState {
    status: MarketStatus | null;
    setStatus: (s: MarketStatus) => void;
}

export const useMarketStatusStore = create<MarketStatusState>((set) => ({
    status: null,
    setStatus: (status) => set({ status }),
}));

const POLL_MS = 5 * 60 * 1000;

/**
 * Polls market status once for the whole app; Header, StatusDot and page
 * bars only read the store. Returns a stop function.
 */
export function startMarketStatusPoll(): () => void {
    const apiUrl = import.meta.env.VITE_API_URL || '';
    const load = () => fetch(`${apiUrl}/api/market-status`)
        .then(r => r.json())
        .then((data: MarketStatus) => useMarketStatusStore.getState().setStatus(data))
        .catch(() => { });
    load();
    const timer = setInterval(load, POLL_MS);
    return () => clearInterval(timer);
}
