import { describe, expect, it } from 'vitest';
import { rangeEntryKind } from '../rangeKind';
import { parseRangeView } from '../../../store/useRangeStore';
import { channelKind, snapshotLiveUpdates } from '../../../utils/seqTracker';

describe('range strategy frontend helpers', () => {
    it('reads the entry kind from a range signal reason', () => {
        expect(rangeEntryKind('RANGE BREAKOUT CALL edge=1 stop=2 close=3')).toBe('BREAKOUT');
        expect(rangeEntryKind('RANGE FLAG PUT edge=1')).toBe('FLAG');
        expect(rangeEntryKind('RANGE MEAN_REVERSION CALL range=1-2')).toBe('MEAN_REVERSION');
        expect(rangeEntryKind('REVERSE: RANGE BREAKOUT PUT edge=1')).toBe('BREAKOUT');
        expect(rangeEntryKind('ENTRY CALL reason=REVERSE_FROM_PUT close=1')).toBeNull();
        expect(rangeEntryKind(undefined)).toBeNull();
    });

    it('parses pub:range payloads', () => {
        expect(parseRangeView('{"regime":"RANGE","adx":12}')?.regime).toBe('RANGE');
        expect(parseRangeView({ regime: 'TRENDING' })?.regime).toBe('TRENDING');
        expect(parseRangeView('not json')).toBeNull();
        expect(parseRangeView({ foo: 1 })).toBeNull();
    });

    it('treats pub:range as full state and restores it from a snapshot', () => {
        expect(channelKind('pub:range')).toBe('full_state');
        const range = { regime: 'RANGE' };
        const out = snapshotLiveUpdates({ range, liveSeqs: { 'pub:range': 3 } }, {});
        expect(out).toEqual([{ channel: 'pub:range', data: range, seq: 3 }]);
        expect(snapshotLiveUpdates({ range, liveSeqs: { 'pub:range': 3 } }, { 'pub:range': 4 })).toEqual([]);
    });
});
