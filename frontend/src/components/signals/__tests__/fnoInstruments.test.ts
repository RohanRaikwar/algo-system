import { describe, expect, it } from 'vitest';
import { paramRows, rejectSummary, decisionLine } from '../FnoInstrumentsTab';
import { parseStrikeSelView } from '../../../store/useStrikeSelStore';
import { channelKind } from '../../../utils/seqTracker';

describe('FNO strike selection', () => {
    it('summarises only the filters that dropped contracts', () => {
        expect(rejectSummary({ delta: 12, premium: 0, theta: 0, gamma: 0, liquidity: 3, iv: 1, cost: 0 })).toBe('delta 12, liquidity 3, iv 1');
        expect(rejectSummary({ delta: 0, premium: 0, theta: 0, gamma: 0, liquidity: 0, iv: 0, cost: 0 })).toBe('none');
    });

    it('describes the rules, with off for disabled limits', () => {
        const rows = Object.fromEntries(paramRows({ delta_min: 0.45, delta_max: 0.6, max_theta_pct: 0, max_gamma: 0.005, gamma_dte: 1, min_liquidity: 5000 }));
        expect(rows['Delta band']).toBe('0.45 – 0.60 (closest to 0.525 wins)');
        expect(rows['Max theta']).toBe('off');
        expect(rows['Gamma cap']).toBe('0.005 within 1d of expiry');
        expect(rows['Max IV']).toBe('off');
        const full = Object.fromEntries(paramRows({ delta_min: 0.45, delta_max: 0.6, max_theta_pct: 8, max_gamma: 0, gamma_dte: 1, min_liquidity: 0, max_buy_iv: 25, min_dte: 2, cost_multiple: 3 }));
        expect(full['Max IV']).toBe('25.0%');
        expect(full['Cost rule']).toBe('delta × target ≥ 3× round-trip cost');
        expect(full['Expiry']).toBe('nearest 2+ days out');
    });

    it('parses pub:strikesel as a full-state channel', () => {
        expect(channelKind('pub:strikesel')).toBe('full_state');
        expect(parseStrikeSelView('{"strategy":"NIFTY50_SR","params":{}}')?.strategy).toBe('NIFTY50_SR');
        expect(parseStrikeSelView('null')).toBeNull();
    });
});

describe('picker decisions', () => {
    it('formats picked and refused decisions', () => {
        expect(decisionLine({ strategy: 'NIFTY50_SR', mode: 'shadow', result: 'picked', symbol: 'NIFTY06OCT2622700CE', delta: 0.52, bid: 20280, ask: 20320, ts: '' }))
            .toBe('picked NIFTY06OCT2622700CE · Δ 0.52 · ₹202.80 / ₹203.20');
        expect(decisionLine({ strategy: 'NIFTY50_RANGE', mode: 'on', result: 'refused', reason: 'no CE passes (dte 6; rejected spread 3)', ts: '' }))
            .toBe('refused: no CE passes (dte 6; rejected spread 3)');
    });

    it('shows the old path\'s choice and agree/disagree for a single entry', () => {
        const base = { strategy: 'NIFTY50_SR', mode: 'shadow', result: 'picked' as const, symbol: 'SYM', delta: 0.5, bid: 100, ask: 110, ts: '' };
        expect(decisionLine({ ...base, old_symbol: 'SYM', agree: true }))
            .toBe('picked SYM · Δ 0.50 · ₹1.00 / ₹1.10 · old SYM ✓ agree');
        expect(decisionLine({ ...base, old_symbol: 'OTHER', agree: false }))
            .toBe('picked SYM · Δ 0.50 · ₹1.00 / ₹1.10 · old OTHER ✗ disagree');
        // No old choice recorded (e.g. "on" mode, or "off"): unchanged, no suffix.
        expect(decisionLine(base)).toBe('picked SYM · Δ 0.50 · ₹1.00 / ₹1.10');
    });

    it('shows all four legs and the credit for a condor decision', () => {
        const legs = [
            { leg: 'LONG_CE', strike: 23000, symbol: 'LCE' },
            { leg: 'SHORT_CE', strike: 22900, symbol: 'SCE' },
            { leg: 'LONG_PE', strike: 22400, symbol: 'LPE' },
            { leg: 'SHORT_PE', strike: 22500, symbol: 'SPE' },
        ];
        expect(decisionLine({
            strategy: 'NIFTY50_RANGE_IC', mode: 'shadow', result: 'picked', legs, credit: 4600,
            old_legs: ['SCE', 'LCE', 'SPE', 'LPE'], agree: true, ts: '',
        })).toBe('picked LONG_CE LCE, SHORT_CE SCE, LONG_PE LPE, SHORT_PE SPE · credit ₹46.00 · old SCE, LCE, SPE, LPE ✓ agree');

        expect(decisionLine({ strategy: 'NIFTY50_RANGE_IC', mode: 'shadow', result: 'refused', reason: 'no condor legs pass', ts: '' }))
            .toBe('refused: no condor legs pass');
    });
});

describe('expected return score', () => {
    it('shows the expected return on premium when the picker scored the contract', () => {
        expect(decisionLine({ strategy: 'NIFTY50_SR', mode: 'shadow', result: 'picked', symbol: 'NIFTY06OCT2622800CE', delta: 0.38, bid: 12990, ask: 13010, score: 0.254, ts: '' }))
            .toBe('picked NIFTY06OCT2622800CE · Δ 0.38 · ₹129.90 / ₹130.10 · exp +25.4%');
    });
});

import { comparison, scoreText } from '../FnoInstrumentsTab';

describe('decision cards', () => {
    it('classifies the old-vs-new comparison', () => {
        expect(comparison({ strategy: 'S', mode: 'shadow', result: 'picked', old_symbol: 'X', agree: true, ts: '' })).toBe('agree');
        expect(comparison({ strategy: 'S', mode: 'shadow', result: 'picked', old_symbol: 'X', agree: false, ts: '' })).toBe('disagree');
        expect(comparison({ strategy: 'S', mode: 'on', result: 'picked', ts: '' })).toBe('none');
        expect(comparison({ strategy: 'IC', mode: 'shadow', result: 'picked', legs: [{ leg: 'SHORT_CE', strike: 1, symbol: 'A' }], old_legs: ['A'], agree: true, ts: '' })).toBe('agree');
    });

    it('formats the expected return', () => {
        expect(scoreText(0.254)).toBe('+25.4%');
        expect(scoreText(-0.031)).toBe('−3.1%');
        expect(scoreText(undefined)).toBe('');
        expect(scoreText(0)).toBe('');
    });
});

import { shortSymbol } from '../FnoInstrumentsTab';

describe('shortSymbol', () => {
    it('reduces an option symbol to strike and type', () => {
        expect(shortSymbol('NIFTY06OCT2623050CE')).toBe('23050 CE');
        expect(shortSymbol('NIFTY13OCT2622450PE')).toBe('22450 PE');
        expect(shortSymbol('SOMETHING')).toBe('SOMETHING');
    });
});
