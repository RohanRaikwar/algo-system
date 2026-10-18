import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { canEdit, exitCommand, levelCommand, levelOnRightSide, rupeesToPaise, snapToTick } from '../positionCommands';
import { PositionControls } from '../PositionControls';
import { buildOpenOrders, type OpenOrder } from '../signalAnalytics';
import type { SignalRecord } from '../../../types/signal';

const order: OpenOrder = {
    strategy: 'SENSEX_SR', side: 'CALL', instrument: 'BSE:99919000', buyPrice: 100, currentPrice: 110,
    entryTime: '', stoplossPrice: 80, stoplossKind: 'HARD', fnoToken: '845123',
};

describe('position commands', () => {
    it('converts rupees to integer paise and snaps index prices to 0.05', () => {
        expect(rupeesToPaise(81.15)).toBe(8115);
        expect(rupeesToPaise(0.1 + 0.2)).toBe(30);
        expect(snapToTick(24851.27)).toBe(24851.25);
        expect(snapToTick(24851.28)).toBe(24851.3);
    });

    it('puts a stop on the losing side and a target on the winning side', () => {
        expect(levelOnRightSide('stop', 90, 100, 'CALL', false)).toBe(true);
        expect(levelOnRightSide('stop', 110, 100, 'PUT', false)).toBe(false); // premium is long for a PUT too
        expect(levelOnRightSide('target', 110, 100, 'PUT', false)).toBe(true);
        expect(levelOnRightSide('stop', 110, 100, 'PUT', true)).toBe(true); // PUT index stop is above
        expect(levelOnRightSide('target', 90, 100, 'PUT', true)).toBe(true);
        expect(levelOnRightSide('target', 100, 100, 'CALL', true)).toBe(false);
        expect(levelOnRightSide('stop', 90, 0, 'CALL', true)).toBe(false);
    });

    it('builds set_levels and exit commands in paise', () => {
        expect(levelCommand(order, 'stop', { premium: 85.5, index: 81500.02 })).toEqual({
            strategy: 'SENSEX_SR', side: 'CALL', fno_token: '845123', action: 'set_levels', fno_sl: 8550, index_stop: 8150000,
        });
        expect(levelCommand(order, 'target', { premium: 150, index: null })).toMatchObject({ fno_target: 15000 });
        expect(levelCommand(order, 'stop', { premium: null, index: null })).toBeNull();
        expect(exitCommand(order)).toEqual({ strategy: 'SENSEX_SR', side: 'CALL', fno_token: '845123', action: 'exit' });
    });

    it('keeps real and multi-leg positions read-only', () => {
        expect(canEdit(order)).toBe(true);
        expect(canEdit({ ...order, real: true })).toBe(false);
        expect(canEdit({ ...order, leg: 'LONG_CE' })).toBe(false);
        expect(renderToStaticMarkup(<PositionControls order={{ ...order, real: true }} currentPrice={110} />)).toContain('read-only');
        const html = renderToStaticMarkup(<PositionControls order={order} currentPrice={110} />);
        expect(html).toContain('>SL<');
        expect(html).toContain('>Exit<');
    });

    it('maps live levels onto open orders', () => {
        const buy = {
            id: 1, strategy: 'NIFTY50_SR', action: 'BUY', side: 'CALL', token: '99926000', exchange: 'NSE',
            reason: '', price: 0, created_at: '2026-10-19T04:00:00Z',
        } as unknown as SignalRecord;
        const [o] = buildOpenOrders([buy], {
            'NIFTY50_SR|CALL': {
                strategy_name: 'NIFTY50_SR', side: 'CALL', fno_token: '45001', entry_fno_price: 10000,
                target_price: 14000, index_stop: 2480000, index_target: 2500000, manual: true,
            },
        });
        expect(o).toMatchObject({ fnoToken: '45001', targetPrice: 140, indexStop: 24800, indexTarget: 25000, manual: true, real: false });
    });
});
