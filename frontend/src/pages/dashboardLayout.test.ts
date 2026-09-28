import { beforeEach, describe, expect, it } from 'vitest';
import { defaultPanes, loadLayout, saveLayout } from './dashboardLayout';

function memoryStorage(): Storage {
    const m = new Map<string, string>();
    return {
        get length() { return m.size; },
        clear: () => m.clear(),
        getItem: (k) => (m.has(k) ? m.get(k)! : null),
        key: (i) => [...m.keys()][i] ?? null,
        removeItem: (k) => { m.delete(k); },
        setItem: (k, v) => { m.set(k, String(v)); },
    };
}

describe('dashboard layout', () => {
    beforeEach(() => { globalThis.localStorage = memoryStorage(); });

    it('defaults panes to 5m, 1h, 3m when available', () => {
        expect(defaultPanes([60, 120, 180, 300, 3600])).toEqual([300, 3600, 180]);
        expect(defaultPanes([60, 300])).toEqual([300, 300, 300]);
    });

    it('round-trips a saved layout and rejects junk', () => {
        saveLayout({ count: 4, panes: [120, 300, 3600] });
        expect(loadLayout([60])).toEqual({ count: 4, panes: [120, 300, 3600] });
        localStorage.setItem('dashboardLayout_v1', '{"count":7,"panes":"x"}');
        expect(loadLayout([60, 300, 3600, 180]).count).toBe(1);
        localStorage.setItem('dashboardLayout_v1', 'not json');
        expect(loadLayout([60]).count).toBe(1);
    });
});
