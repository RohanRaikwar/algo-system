import { describe, expect, it } from 'vitest';
import { tokenCaption, tokenLabel } from '../helpers';

describe('instrument labels', () => {
    it('maps known keys to friendly names', () => {
        expect(tokenLabel('NSE:99926000')).toBe('NIFTY 50');
        expect(tokenLabel('BSE:99919000')).toBe('SENSEX');
        expect(tokenLabel('NSE:99926009')).toBe('BANK NIFTY');
    });

    it('falls back to the raw key', () => {
        expect(tokenLabel('NFO:12345')).toBe('NFO:12345');
        expect(tokenLabel(null)).toBe('');
    });

    it('splits exchange and token for the caption', () => {
        expect(tokenCaption('NSE:99926000')).toBe('NSE · 99926000');
        expect(tokenCaption('99926000')).toBe('');
    });
});
