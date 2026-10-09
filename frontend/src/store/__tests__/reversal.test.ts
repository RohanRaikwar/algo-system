import { describe, expect, it } from 'vitest';
import { findReversal, useAnalystStore } from '../useAnalystStore';
import type { ReversalPayload, ReversalView } from '../../types/analyst';

const view = (strategy: string, side: string): ReversalView => ({
    strategy, side, fno_token: '1', index_entry: 0, target_level: 0, stop_level: 0,
    fno_entry_price: 0, p: 0.8, decision: 'EXIT', latched: true, reasons: ['giveback'],
    index_ltp: 0, premium_ltp: 0, progress: 0.4, peak_progress: 0.8, features: {}, ts: '',
});

describe('exit watch reversal store', () => {
    it('matches a live order by strategy and side', () => {
        const p: ReversalPayload = { shadow: true, ts: '', positions: [view('NIFTY50_SR', 'CALL'), view('PAPER_OTHER', 'PUT')] };
        expect(findReversal(p, 'PAPER_OTHER', 'put')?.side).toBe('PUT');
        expect(findReversal(p, 'NIFTY50_SR', 'PUT')).toBeNull();
        expect(findReversal(null, 'NIFTY50_SR', 'CALL')).toBeNull();
        expect(findReversal({ shadow: true, ts: '', positions: null as unknown as ReversalView[] }, 'X', 'CALL')).toBeNull();
    });

    it('stores the latest payload', () => {
        const p: ReversalPayload = { shadow: false, ts: 't', positions: [] };
        useAnalystStore.getState().setReversal(p);
        expect(useAnalystStore.getState().reversal).toBe(p);
    });
});
