import { describe, expect, it } from 'vitest';
import type { SignalRecord } from '../../../types/signal';
import { formatSignedPaise, instrumentLabel, orderMode, signalPnL } from '../signalFormat';

const base: SignalRecord = {
    id: 1,
    strategy: 'NIFTY50_SR',
    action: 'BUY',
    token: '99926000',
    exchange: 'NSE',
    reason: '',
    ema_values: {},
    candle_ts: '2026-10-09T06:30:00Z',
    created_at: '2026-10-09T06:30:00Z',
} as SignalRecord;

describe('orderMode', () => {
    it('watch advice is shadow even when live', () => {
        expect(orderMode({ ...base, action: 'WATCH_EXIT', live_mode: true }).mode).toBe('shadow');
    });
    it('profit cap wins over live', () => {
        expect(orderMode({ ...base, profit_cap: true, live_mode: true }).mode).toBe('capped');
    });
    it('live is real, otherwise paper', () => {
        expect(orderMode({ ...base, live_mode: true }).mode).toBe('real');
        expect(orderMode(base).mode).toBe('paper');
    });
});

describe('signalPnL', () => {
    it('EXIT against its entry, in paise', () => {
        expect(signalPnL({ ...base, action: 'EXIT', price: 11840 }, 10950)).toBe(890);
    });
    it('null for entries and missing prices', () => {
        expect(signalPnL({ ...base, price: 11840 }, 10950)).toBeNull();
        expect(signalPnL({ ...base, action: 'EXIT' }, 10950)).toBeNull();
        expect(signalPnL({ ...base, action: 'EXIT', price: 100 }, undefined)).toBeNull();
    });
    it('WATCH_EXIT only when priced at premium', () => {
        expect(signalPnL({ ...base, action: 'WATCH_EXIT', price: 11620, reason: 'x idx=25661' }, 10950)).toBe(670);
        expect(signalPnL({ ...base, action: 'WATCH_EXIT', price: 11620, reason: 'x' }, 10950)).toBeNull();
    });
});

describe('format helpers', () => {
    it('signs paise with a real minus', () => {
        expect(formatSignedPaise(890)).toBe('+₹8.90');
        expect(formatSignedPaise(-590)).toBe('−₹5.90');
        expect(formatSignedPaise(0)).toBe('₹0.00');
    });
    it('prefers the traded option symbol', () => {
        expect(instrumentLabel({ ...base, fno_symbol: 'NIFTY14OCT2625650CE' })).toBe('NIFTY14OCT2625650CE');
        expect(instrumentLabel(base)).toBe('NSE:99926000');
    });
});
