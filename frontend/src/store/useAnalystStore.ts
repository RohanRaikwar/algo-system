import { create } from 'zustand';
import type { MarketStateEvent, SRLevel, BreakoutEvent } from '../types/analyst';

const MAX_STATE_HISTORY = 50;
const MAX_BREAKOUT_HISTORY = 30;

interface AnalystState {
    // Current market state
    currentState: MarketStateEvent | null;
    stateHistory: MarketStateEvent[];

    // S/R levels
    levels: SRLevel[];

    // Breakout
    breakout: BreakoutEvent | null;
    breakoutHistory: BreakoutEvent[];

    // Actions
    setMarketState: (event: MarketStateEvent) => void;
    setLevels: (levels: SRLevel[]) => void;
    setBreakout: (event: BreakoutEvent) => void;
}

export const useAnalystStore = create<AnalystState>((set) => ({
    currentState: null,
    stateHistory: [],
    levels: [],
    breakout: null,
    breakoutHistory: [],

    setMarketState: (event) =>
        set((s) => {
            // Only add to history if state actually changed
            const history =
                s.currentState && s.currentState.state !== event.state
                    ? [s.currentState, ...s.stateHistory].slice(0, MAX_STATE_HISTORY)
                    : s.stateHistory;
            return { currentState: event, stateHistory: history };
        }),

    setLevels: (levels) => set({ levels }),

    setBreakout: (event) =>
        set((s) => {
            const history = [event, ...s.breakoutHistory].slice(0, MAX_BREAKOUT_HISTORY);
            return { breakout: event, breakoutHistory: history };
        }),
}));
