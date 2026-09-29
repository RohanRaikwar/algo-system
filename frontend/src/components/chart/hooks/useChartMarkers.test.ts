import { describe, expect, it } from 'vitest';
import { buildChartMarkers, REFUSED_COLOR } from './useChartMarkers';
import { parseRefusedView } from '../../../store/useRefusedStore';
import { channelKind, snapshotLiveUpdates } from '../../../utils/seqTracker';
import { IST_OFFSET } from '../../../utils/helpers';
import type { SignalRecord } from '../../../types/signal';
import type { RefusedEntry } from '../../../types/refused';

const refused: RefusedEntry = {
    strategy: 'NIFTY50_SR', side: 'PUT', token: '99926000', exchange: 'NSE', strike: 22600,
    reason: 'SR strike: no greeks', strategy_reason: 'SR PULLBACK PUT', ts: '2026-09-29T05:16:01Z',
};
const entry = {
    id: 1, strategy: 'NIFTY50_SR', action: 'BUY', side: 'CALL', token: '99926000', exchange: 'NSE',
    reason: '', ema_values: '', candle_ts: '2026-09-29T05:10:00Z', created_at: '2026-09-29T05:10:00Z',
} as SignalRecord;

describe('buildChartMarkers', () => {
    it('marks refused entries as grey circles on their TF bucket', () => {
        const m = buildChartMarkers([entry], [refused], 'NSE:99926000', 60, false);
        expect(m).toHaveLength(2);
        const r = m[1];
        expect(r.shape).toBe('circle');
        expect(r.color).toBe(REFUSED_COLOR);
        expect(r.text).toBe('REFUSED P');
        const sec = Date.parse(refused.ts) / 1000 + IST_OFFSET;
        expect(r.time).toBe(Math.floor(sec / 60) * 60);
        expect(m[0].shape).toBe('arrowUp'); // sorted by time: entry first
    });

    it('skips other instruments and uses compact text on phones', () => {
        expect(buildChartMarkers([], [refused], 'NFO:40712', 60, false)).toHaveLength(0);
        expect(buildChartMarkers([], [refused], '99926000', 60, true)[0].text).toBe('✕P');
    });
});

describe('pub:refused', () => {
    it('parses object and string payloads', () => {
        const view = { date: '2026-09-29', entries: [refused] };
        expect(parseRefusedView(view)?.entries).toHaveLength(1);
        expect(parseRefusedView(JSON.stringify(view))?.entries[0].reason).toBe('SR strike: no greeks');
        expect(parseRefusedView({ date: 'x' })).toBeNull();
        expect(parseRefusedView('not json')).toBeNull();
    });

    it('is a full-state channel carried by SNAPSHOT', () => {
        expect(channelKind('pub:refused')).toBe('full_state');
        const ups = snapshotLiveUpdates({ refused: { date: 'd', entries: [] }, liveSeqs: { 'pub:refused': 3 } }, {});
        expect(ups).toEqual([{ channel: 'pub:refused', data: { date: 'd', entries: [] }, seq: 3 }]);
    });
});
