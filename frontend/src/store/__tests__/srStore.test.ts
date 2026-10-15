import { describe, expect, it } from 'vitest';
import { parseSRXViews, useSRStore } from '../useSRStore';
import { channelKind, snapshotLiveUpdates } from '../../utils/seqTracker';

const sensex = { strategy: 'SENSEX_SR', key: 'BSE:99919000', regime: 'RANGE', side: 'NONE' };

describe('SR views per underlying', () => {
    it('parses pub:srx maps and drops invalid entries', () => {
        const views = parseSRXViews(JSON.stringify({ 'BSE:99919000': sensex, bad: { x: 1 } }));
        expect(Object.keys(views ?? {})).toEqual(['BSE:99919000']);
        expect(views?.['BSE:99919000'].strategy).toBe('SENSEX_SR');
        expect(parseSRXViews('nope')).toBeNull();
        expect(parseSRXViews([sensex])).toBeNull();
    });

    it('keeps NIFTY and extra views apart', () => {
        const nifty = { ...sensex, strategy: 'NIFTY50_SR', key: 'NSE:99926000' };
        useSRStore.getState().setView(nifty as never);
        useSRStore.getState().setExtra(parseSRXViews({ 'BSE:99919000': sensex }) ?? {});
        const s = useSRStore.getState();
        expect(s.view?.key).toBe('NSE:99926000');
        expect(s.extra['BSE:99919000'].strategy).toBe('SENSEX_SR');
    });

    it('treats pub:srx as full state carried by SNAPSHOT', () => {
        expect(channelKind('pub:srx')).toBe('full_state');
        const srx = { 'BSE:99919000': sensex };
        expect(snapshotLiveUpdates({ srx, liveSeqs: { 'pub:srx': 3 } }, {})).toEqual([{ channel: 'pub:srx', data: srx, seq: 3 }]);
    });
});
