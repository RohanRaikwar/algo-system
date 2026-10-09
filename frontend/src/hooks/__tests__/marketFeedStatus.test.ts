import { describe, expect, it } from 'vitest';
import { deriveMarketFeed, type MarketFeedInput } from '../useMarketFeedStatus';

const base: MarketFeedInput = {
    connected: true,
    reconnectAttempts: 0,
    marketOpen: false,
    status: null,
    weekend: false,
};

describe('deriveMarketFeed', () => {
    it('open and live pulses green', () => {
        const s = deriveMarketFeed({ ...base, marketOpen: true });
        expect(s.dot).toBe('live');
        expect(s.compactLabel).toBe('Open · Live');
        expect(s.summary).toBe('Market open · Feed live');
    });

    it('open while the feed is still connecting is static green', () => {
        const s = deriveMarketFeed({ ...base, marketOpen: true, connected: false });
        expect(s.dot).toBe('open');
        expect(s.compactLabel).toBe('Connecting…');
    });

    it('reconnecting wins over market state', () => {
        const s = deriveMarketFeed({ ...base, marketOpen: true, connected: false, reconnectAttempts: 2 });
        expect(s.dot).toBe('reconnecting');
        expect(s.feedTone).toBe('down');
    });

    it('holiday is amber with the holiday name and next open', () => {
        const s = deriveMarketFeed({
            ...base,
            status: { isOpen: false, isHoliday: true, holidayName: 'Diwali', nextOpenLabel: 'Mon 09:15' },
        });
        expect(s.dot).toBe('holiday');
        expect(s.marketLabel).toBe('Holiday: Diwali');
        expect(s.summary).toBe('Holiday: Diwali · Opens Mon 09:15 · Feed live');
    });

    it('weekend is grey', () => {
        const s = deriveMarketFeed({ ...base, weekend: true });
        expect(s.dot).toBe('closed');
        expect(s.compactLabel).toBe('Weekend');
    });

    it('REST status open counts as open even before the WS flag', () => {
        const s = deriveMarketFeed({
            ...base,
            status: { isOpen: true, isHoliday: false, holidayName: '', nextOpenLabel: '' },
        });
        expect(s.open).toBe(true);
        expect(s.dot).toBe('live');
    });
});
