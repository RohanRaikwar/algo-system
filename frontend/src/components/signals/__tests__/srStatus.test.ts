import { describe, expect, it } from 'vitest';
import { rejectLabel, srStatus, topRejects } from '../SRStatusCard';
import { parseSRView } from '../../../store/useSRStore';
import type { SRView } from '../../../types/sr';

const base: SRView = {
    strategy: 'NIFTY50_SR', key: 'NSE:99926000', ts: '2026-09-30T06:37:00Z',
    regime: 'RANGE', adx: 18, rsi5: 50, levels: [], side: 'NONE', rejects: {},
    trades_today: 0, max_trades: 3, consec_losses: 0, day_pnl_pts: 0, cooldown_left: 0,
    entry_tf: 1, entry_from_min: 570, entry_to_min: 885, min_confirmations: 3,
    fade: true, retest: true, pullback: true,
};

describe('SR status', () => {
    it('labels reject reasons', () => {
        expect(rejectLabel('fade:not_at_level')).toBe('fade: price not at a level');
        expect(rejectLabel('pullback:confirmations_2')).toBe('pullback: only 2 confirmations');
        expect(rejectLabel('retest:reward_risk')).toBe('retest: reward:risk too low');
        expect(rejectLabel('something_new')).toBe('something_new');
    });

    it('prefers position, then block, then pending break, then last reject', () => {
        expect(srStatus({ ...base, last_reject: 'fade:no_pattern' })).toBe('Waiting: fade: no reversal candle');
        expect(srStatus({ ...base, pending_side: 'CALL', pending_level: 2270000, pending_left: 12, last_reject: 'x' }))
            .toBe('Break CALL at 22700.00: waiting for retest (12 bars left)');
        expect(srStatus({ ...base, block: 'trade_cap', pending_side: 'CALL' })).toBe('Blocked: daily trade cap reached');
        expect(srStatus({ ...base, side: 'PUT', kind: 'FADE', entry: 2270000, stop: 2272000, target: 2266000, block: 'trade_cap' }))
            .toBe('In PUT (FADE) entry 22700.00 · stop 22720.00 · target 22660.00');
        expect(srStatus({ ...base, regime: 'WARMING' })).toBe('Warming up: regime not known yet');
    });

    it('sorts rejects by count', () => {
        expect(topRejects({ a: 1, b: 5, c: 3 }, 2)).toEqual([['b', 5], ['c', 3]]);
        expect(topRejects(null)).toEqual([]);
    });

    it('parses pub:sr payloads', () => {
        expect(parseSRView(JSON.stringify(base))?.strategy).toBe('NIFTY50_SR');
        expect(parseSRView({ regime: 'RANGE' })).toBeNull();
        expect(parseSRView('not json')).toBeNull();
    });
});

describe('live premium', () => {
    it('uses the tick when present, else the snapshot', async () => {
        const { livePremium, useOptionLTPStore } = await import('../../../store/useOptionLTPStore');
        expect(livePremium(17725, 176.2)).toEqual({ rupees: 177.25, live: true });
        expect(livePremium(undefined, 176.2)).toEqual({ rupees: 176.2, live: false });
        useOptionLTPStore.getState().setLTP('40712', 17725);
        const before = useOptionLTPStore.getState().ltp;
        useOptionLTPStore.getState().setLTP('40712', 17725);
        expect(useOptionLTPStore.getState().ltp).toBe(before); // same price: no new state
        expect(useOptionLTPStore.getState().ltp['40712']).toBe(17725);
    });
});
