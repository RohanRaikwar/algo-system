import { create } from 'zustand';

export interface StrikeInfo {
    resolved: boolean;
    spot_price: number;
    atm_strike: number;
    call: { token: string; symbol: string };
    put: { token: string; symbol: string };
    resolved_at: string;
    lot_size?: number;
    qty?: number;
    call_ltp?: number;
    put_ltp?: number;
}

interface StrikeStore {
    strike: StrikeInfo | null;
    callLTP: number;
    putLTP: number;
    setStrike: (info: StrikeInfo) => void;
    setCallLTP: (price: number) => void;
    setPutLTP: (price: number) => void;
}

export const useStrikeStore = create<StrikeStore>((set) => ({
    strike: null,
    callLTP: 0,
    putLTP: 0,
    setStrike: (info) => set({ strike: info }),
    setCallLTP: (price) => set({ callLTP: price }),
    setPutLTP: (price) => set({ putLTP: price }),
}));
