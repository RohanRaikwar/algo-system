import { describe, expect, it } from 'vitest';
import type { TelegramMessage } from '../../services/api';
import { span, timelineParts } from './timeline';

const base: TelegramMessage = {
    chat_id: 1, msg_id: 2, sent_at: '2026-10-27T04:30:00Z', text: 'BUY NIFTY 24500 CE', verdict: 'SENT',
};

describe('span', () => {
    it('formats durations', () => {
        expect(span(450)).toBe('450ms');
        expect(span(2140)).toBe('2.1s');
        expect(span(41_300)).toBe('41s');
        expect(span(185_000)).toBe('3m 05s');
    });
});

describe('timelineParts', () => {
    it('is empty without stamps', () => {
        expect(timelineParts(base)).toEqual([]);
    });

    it('lists receipt, read and the entry steps once each', () => {
        const m: TelegramMessage = {
            ...base,
            received_at: '2026-10-27T04:30:00.700Z',
            read_ms: 1800,
            timeline: [
                { event: 'ARMED', ms: 1900 },
                { event: 'FIRST_TICK', ms: 2600 },
                { event: 'SL_RAISED', ms: 30_000 },
                { event: 'ENTERED', ms: 41_000 },
                { event: 'FILLED', ms: 41_300 },
                { event: 'REPLACED', ms: 50_000 },
            ],
        };
        expect(timelineParts(m)).toEqual([
            'recv +700ms', 'read 1.8s', 'armed +1.9s', '1st tick +2.6s', 'entered +41s', 'filled +41s',
        ]);
    });
});
