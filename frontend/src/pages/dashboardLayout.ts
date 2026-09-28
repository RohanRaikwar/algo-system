export const LAYOUTS = [1, 2, 4] as const;
export type LayoutCount = (typeof LAYOUTS)[number];

export interface ChartLayout {
    count: LayoutCount;
    panes: number[]; // timeframes (seconds) of the extra panes, in order
}

const STORAGE_KEY = 'dashboardLayout_v1';
// Extra panes default to 5m, 1h, 3m: the entry, trend and in-between views.
const DEFAULT_PANES = [300, 3600, 180];

/** Pane timeframes, falling back to available ones so a pane never shows an unknown TF. */
export function defaultPanes(tfs: number[]): number[] {
    const avail = tfs.length ? tfs : [60];
    return DEFAULT_PANES.map((tf, i) => (avail.includes(tf) ? tf : avail[Math.min(i, avail.length - 1)]));
}

/** The viewer's saved layout (per browser); one chart when none or unreadable. */
export function loadLayout(tfs: number[]): ChartLayout {
    const fallback: ChartLayout = { count: 1, panes: defaultPanes(tfs) };
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) return fallback;
        const v = JSON.parse(raw) as Partial<ChartLayout>;
        const count = LAYOUTS.includes(v.count as LayoutCount) ? (v.count as LayoutCount) : 1;
        const panes = Array.isArray(v.panes) && v.panes.length === 3 && v.panes.every(n => typeof n === 'number' && n > 0)
            ? v.panes
            : fallback.panes;
        return { count, panes };
    } catch {
        return fallback;
    }
}

export function saveLayout(l: ChartLayout) {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(l));
    } catch {
        // private mode / blocked storage: layout just isn't remembered
    }
}
