import { afterEach, describe, expect, it } from 'vitest';
import { acquirePaneSub, isPaneSub, listPaneSubs, releasePaneSub, resetPaneSubs } from '../paneSubscriptions';

describe('pane subscriptions', () => {
    afterEach(resetPaneSubs);

    it('subscribes once per symbol:tf and unsubscribes on the last release', () => {
        expect(acquirePaneSub('NSE:1', 300)).toBe(true);
        expect(acquirePaneSub('NSE:1', 300)).toBe(false);
        expect(acquirePaneSub('NSE:1', 3600)).toBe(true);
        expect(listPaneSubs()).toHaveLength(2);
        expect(releasePaneSub('NSE:1', 300)).toBe(false);
        expect(isPaneSub('NSE:1', 300)).toBe(true);
        expect(releasePaneSub('NSE:1', 300)).toBe(true);
        expect(isPaneSub('NSE:1', 300)).toBe(false);
        expect(releasePaneSub('NSE:1', 300)).toBe(false);
    });
});
