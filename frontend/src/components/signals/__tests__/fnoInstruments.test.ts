import { describe, expect, it } from 'vitest';
import { paramRows, rejectSummary } from '../FnoInstrumentsTab';
import { parseStrikeSelView } from '../../../store/useStrikeSelStore';
import { channelKind } from '../../../utils/seqTracker';

describe('FNO strike selection', () => {
    it('summarises only the filters that dropped contracts', () => {
        expect(rejectSummary({ delta: 12, premium: 0, theta: 0, gamma: 0, liquidity: 3 })).toBe('delta 12, liquidity 3');
        expect(rejectSummary({ delta: 0, premium: 0, theta: 0, gamma: 0, liquidity: 0 })).toBe('none');
    });

    it('describes the rules, with off for disabled limits', () => {
        const rows = Object.fromEntries(paramRows({ delta_min: 0.45, delta_max: 0.6, max_theta_pct: 0, max_gamma: 0.005, gamma_dte: 1, min_liquidity: 5000 }));
        expect(rows['Delta band']).toBe('0.45 – 0.60 (closest to 0.525 wins)');
        expect(rows['Max theta']).toBe('off');
        expect(rows['Gamma cap']).toBe('0.005 within 1d of expiry');
    });

    it('parses pub:strikesel as a full-state channel', () => {
        expect(channelKind('pub:strikesel')).toBe('full_state');
        expect(parseStrikeSelView('{"strategy":"NIFTY50_SR","params":{}}')?.strategy).toBe('NIFTY50_SR');
        expect(parseStrikeSelView('null')).toBeNull();
    });
});
