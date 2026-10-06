import type {
    ISeriesApi,
    ISeriesPrimitive,
    ISeriesPrimitivePaneRenderer,
    ISeriesPrimitivePaneView,
    SeriesAttachedParameter,
    Time,
} from 'lightweight-charts';
import { IST_OFFSET } from '../../utils/helpers';
import type { SRView } from '../../types/sr';

/** The sideways box in chart units: IST-shifted seconds and rupees. */
export interface SRBox {
    from: number;
    to: number;
    high: number;
    low: number;
    boxed: boolean;
}

/** Box geometry from the SR view; null without a complete box (pure, for testing). */
export function srBox(v: SRView | null): SRBox | null {
    if (!v?.box_from || !v.box_to || !v.box_high || !v.box_low) return null;
    const from = Date.parse(v.box_from), to = Date.parse(v.box_to);
    if (Number.isNaN(from) || Number.isNaN(to) || to <= from) return null;
    return {
        from: Math.floor(from / 1000) + IST_OFFSET,
        to: Math.floor(to / 1000) + IST_OFFSET,
        high: v.box_high / 100,
        low: v.box_low / 100,
        boxed: Boolean(v.boxed),
    };
}

const FILL = { boxed: 'rgba(229, 162, 58, 0.14)', moving: 'rgba(138, 148, 166, 0.06)' };
const EDGE = { boxed: 'rgba(229, 162, 58, 0.7)', moving: 'rgba(138, 148, 166, 0.35)' };

interface Rect { x: number; y: number; w: number; h: number; boxed: boolean }

class Renderer implements ISeriesPrimitivePaneRenderer {
    constructor(private rect: Rect | null) {}
    draw(target: Parameters<ISeriesPrimitivePaneRenderer['draw']>[0]) {
        const r = this.rect;
        if (!r) return;
        target.useMediaCoordinateSpace(({ context: ctx }) => {
            ctx.fillStyle = r.boxed ? FILL.boxed : FILL.moving;
            ctx.fillRect(r.x, r.y, r.w, r.h);
            ctx.strokeStyle = r.boxed ? EDGE.boxed : EDGE.moving;
            ctx.setLineDash([4, 3]);
            ctx.lineWidth = 1;
            ctx.strokeRect(r.x + 0.5, r.y + 0.5, r.w, r.h);
            ctx.setLineDash([]);
            if (r.boxed && r.w > 50) {
                ctx.fillStyle = EDGE.boxed;
                ctx.font = "600 10px 'Inter', sans-serif";
                ctx.fillText('SIDEWAYS', r.x + 4, r.y - 4);
            }
        });
    }
}

class PaneView implements ISeriesPrimitivePaneView {
    rect: Rect | null = null;
    zOrder() { return 'bottom' as const; }
    renderer() { return new Renderer(this.rect); }
}

/** Shaded rectangle over SR's sideways-box window (amber while boxed). */
export class SRBoxPrimitive implements ISeriesPrimitive<Time> {
    private box: SRBox | null = null;
    private params: SeriesAttachedParameter<Time> | null = null;
    private view = new PaneView();
    private views = [this.view];

    attached(p: SeriesAttachedParameter<Time>) { this.params = p; }
    detached() { this.params = null; }

    setBox(box: SRBox | null) {
        this.box = box;
        this.params?.requestUpdate();
    }

    updateAllViews() {
        const p = this.params, b = this.box;
        this.view.rect = null;
        if (!p || !b) return;
        const series = p.series as ISeriesApi<'Candlestick'>;
        const ts = p.chart.timeScale();
        // Both edges are bar times on 1m–5m charts. The end is the next bar's
        // start, which may not exist yet: then the box runs to the right edge.
        // On coarser charts the start is not a bar time and nothing is drawn.
        const x1 = ts.timeToCoordinate(b.from as Time);
        const x2 = ts.timeToCoordinate(b.to as Time) ?? ts.width();
        const y1 = series.priceToCoordinate(b.high), y2 = series.priceToCoordinate(b.low);
        if (x1 === null || x2 === null || y1 === null || y2 === null || x2 <= x1) return;
        this.view.rect = { x: x1, y: Math.min(y1, y2), w: x2 - x1, h: Math.abs(y2 - y1), boxed: b.boxed };
    }

    paneViews() { return this.views; }
}
