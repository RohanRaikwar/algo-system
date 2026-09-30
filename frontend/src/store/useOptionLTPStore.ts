import { create } from 'zustand';

interface OptionLTPState {
    /** Last tick price per token, paise. */
    ltp: Record<string, number>;
    setLTP: (token: string, paise: number) => void;
}

/** Live LTP of every token the gateway streams ticks for (pub:tick:*). */
export const useOptionLTPStore = create<OptionLTPState>((set, get) => ({
    ltp: {},
    setLTP: (token, paise) => {
        if (get().ltp[token] === paise) return;
        set(s => ({ ltp: { ...s.ltp, [token]: paise } }));
    },
}));

/** Premium in rupees: the live tick (paise) when one arrived, else fallback rupees. */
export function livePremium(tickPaise: number | undefined, fallback: number): { rupees: number; live: boolean } {
    if (tickPaise && tickPaise > 0) return { rupees: tickPaise / 100, live: true };
    return { rupees: fallback, live: false };
}
