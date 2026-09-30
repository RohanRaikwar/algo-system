import type {
    AutoscaleInfo,
    IChartApi,
    ISeriesApi,
    ISeriesPrimitive,
    ISeriesPrimitivePaneRenderer,
    ISeriesPrimitivePaneView,
    Logical,
    SeriesAttachedParameter,
    Time,
} from 'lightweight-charts';

export type TradeMarkerKind = 'entry' | 'exit' | 'refused';

export interface TradeMarker {
    time: number; // chart time (IST-shifted seconds, TF-aligned)
    kind: TradeMarkerKind;
    label: string; // e.g. "BUY CALL"; empty in compact mode
    side: string; // "C" | "P" | ""
}

export const MARKER_COLORS: Record<TradeMarkerKind, string> = {
    entry: '#3ecf8e', // --sg-profit
    exit: '#f0616d', // --sg-loss
    refused: '#8a94a6', // --sg-text-3
};

const FONT = "600 10px 'Inter', sans-serif";
const PILL_H = 18;
const PAD_X = 6;
const ICON = 10;
const GAP = 4; // icon to text
const STEM = 7; // bar extreme to pill
const STACK = 3; // gap between stacked pills on one bar

interface Placed {
    x: number;
    y: number; // bar extreme (high for above, low for below)
    above: boolean;
    slot: number;
    m: TradeMarker;
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
}

/** Icon centred at (cx, cy), ICON px square, stroked in the current strokeStyle. */
function drawIcon(ctx: CanvasRenderingContext2D, kind: TradeMarkerKind, cx: number, cy: number) {
    const h = ICON / 2;
    ctx.lineWidth = 1.6;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    ctx.beginPath();
    if (kind === 'entry') {
        // Trend-up arrow.
        ctx.moveTo(cx - h, cy + h * 0.6);
        ctx.lineTo(cx - h * 0.2, cy - h * 0.1);
        ctx.lineTo(cx + h * 0.2, cy + h * 0.3);
        ctx.lineTo(cx + h, cy - h * 0.6);
        ctx.moveTo(cx + h * 0.2, cy - h * 0.6);
        ctx.lineTo(cx + h, cy - h * 0.6);
        ctx.lineTo(cx + h, cy + h * 0.2);
    } else if (kind === 'exit') {
        // Box with arrow leaving it.
        ctx.moveTo(cx + h * 0.1, cy - h);
        ctx.lineTo(cx - h, cy - h);
        ctx.lineTo(cx - h, cy + h);
        ctx.lineTo(cx + h * 0.1, cy + h);
        ctx.moveTo(cx - h * 0.3, cy);
        ctx.lineTo(cx + h, cy);
        ctx.moveTo(cx + h * 0.4, cy - h * 0.6);
        ctx.lineTo(cx + h, cy);
        ctx.lineTo(cx + h * 0.4, cy + h * 0.6);
    } else {
        // Ban: circle with slash.
        ctx.arc(cx, cy, h, 0, Math.PI * 2);
        ctx.moveTo(cx - h * 0.7, cy + h * 0.7);
        ctx.lineTo(cx + h * 0.7, cy - h * 0.7);
    }
    ctx.stroke();
}

class Renderer implements ISeriesPrimitivePaneRenderer {
    constructor(private readonly placed: Placed[]) {}

    draw(target: Parameters<ISeriesPrimitivePaneRenderer['draw']>[0]) {
        target.useMediaCoordinateSpace(({ context: ctx }) => {
            ctx.save();
            ctx.font = FONT;
            ctx.textBaseline = 'middle';
            for (const p of this.placed) this.drawOne(ctx, p);
            ctx.restore();
        });
    }

    private drawOne(ctx: CanvasRenderingContext2D, { x, y, above, slot, m }: Placed) {
        const color = MARKER_COLORS[m.kind];
        const text = m.label || m.side;
        const textW = text ? ctx.measureText(text).width : 0;
        const w = PAD_X * 2 + ICON + (text ? GAP + textW : 0);
        const offset = STEM + slot * (PILL_H + STACK);
        const top = above ? y - offset - PILL_H : y + offset;
        const left = Math.round(x - w / 2);
        const refused = m.kind === 'refused';

        // Stem from bar extreme to the first pill.
        if (slot === 0) {
            ctx.strokeStyle = color;
            ctx.globalAlpha = 0.6;
            ctx.lineWidth = 1;
            ctx.setLineDash(refused ? [2, 2] : []);
            ctx.beginPath();
            ctx.moveTo(Math.round(x) + 0.5, above ? y - 1 : y + 1);
            ctx.lineTo(Math.round(x) + 0.5, above ? top + PILL_H : top);
            ctx.stroke();
            ctx.setLineDash([]);
            ctx.globalAlpha = 1;
        }

        roundRect(ctx, left + 0.5, top + 0.5, w - 1, PILL_H - 1, 4);
        if (refused) {
            ctx.fillStyle = '#151b25'; // chart background
            ctx.fill();
            ctx.strokeStyle = color;
            ctx.lineWidth = 1;
            ctx.stroke();
        } else {
            ctx.fillStyle = color;
            ctx.fill();
        }

        const fg = refused ? color : '#0b0e14';
        ctx.strokeStyle = fg;
        drawIcon(ctx, m.kind, left + PAD_X + ICON / 2, top + PILL_H / 2);
        if (text) {
            ctx.fillStyle = fg;
            ctx.fillText(text, left + PAD_X + ICON + GAP, top + PILL_H / 2 + 0.5);
        }
    }
}

class PaneView implements ISeriesPrimitivePaneView {
    placed: Placed[] = [];
    zOrder() { return 'top' as const; }
    renderer() { return new Renderer(this.placed); }
}

/**
 * Trade markers drawn as labelled badges: entries below the bar, exits and
 * refused entries above, stacked when several share a bar.
 */
export class TradeMarkersPrimitive implements ISeriesPrimitive<Time> {
    private chart: IChartApi | null = null;
    private series: ISeriesApi<'Candlestick'> | null = null;
    private requestUpdate: (() => void) | null = null;
    private markers: TradeMarker[] = [];
    private readonly view = new PaneView();
    private readonly views = [this.view];

    attached(p: SeriesAttachedParameter<Time>) {
        this.chart = p.chart as IChartApi;
        this.series = p.series as ISeriesApi<'Candlestick'>;
        this.requestUpdate = p.requestUpdate;
    }

    detached() {
        this.chart = null;
        this.series = null;
        this.requestUpdate = null;
    }

    setMarkers(markers: TradeMarker[]) {
        this.markers = markers;
        this.requestUpdate?.();
    }

    updateAllViews() {
        const placed: Placed[] = [];
        if (this.chart && this.series) {
            const ts = this.chart.timeScale();
            const slots = new Map<string, number>();
            for (const m of this.markers) {
                const x = ts.timeToCoordinate(m.time as Time);
                if (x === null) continue;
                const logical = ts.coordinateToLogical(x);
                if (logical === null) continue;
                const bar = this.series.dataByIndex(Math.round(logical));
                if (!bar || !('high' in bar) || bar.time !== m.time) continue;
                const above = m.kind !== 'entry';
                const y = this.series.priceToCoordinate(above ? bar.high : bar.low);
                if (y === null) continue;
                const key = `${m.time}:${above ? 'a' : 'b'}`;
                const slot = slots.get(key) ?? 0;
                slots.set(key, slot + 1);
                placed.push({ x, y, above, slot, m });
            }
        }
        this.view.placed = placed;
    }

    paneViews() { return this.views; }

    /** Pixel headroom so badges on the highest/lowest bar stay on screen. */
    autoscaleInfo(from: Logical, to: Logical): AutoscaleInfo | null {
        if (this.markers.length === 0 || !this.series) return null;
        let lo = Infinity;
        let hi = -Infinity;
        for (let i = Math.max(0, Math.floor(from)); i <= Math.ceil(to); i++) {
            const bar = this.series.dataByIndex(i);
            if (!bar || !('high' in bar)) continue;
            if (bar.low < lo) lo = bar.low;
            if (bar.high > hi) hi = bar.high;
        }
        if (lo > hi) return null;
        const margin = STEM + 2 * PILL_H + STACK;
        return { priceRange: { minValue: lo, maxValue: hi }, margins: { above: margin, below: margin } };
    }
}
