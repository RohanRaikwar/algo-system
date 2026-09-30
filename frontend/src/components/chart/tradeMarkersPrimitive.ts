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
    lines: string[]; // card detail rows; empty in compact mode
    pnl?: number; // paise, exits paired with an entry; tints the P&L row
}

export const MARKER_COLORS: Record<TradeMarkerKind, string> = {
    entry: '#3ecf8e', // --sg-profit
    exit: '#f0616d', // --sg-loss
    refused: '#8a94a6', // --sg-text-3
};

const FONT = "600 10px 'Inter', sans-serif";
const DETAIL_FONT = "500 10px 'Inter', sans-serif";
const BG = '#151b25'; // chart background
const DETAIL_COLOR = '#c9d1dc';
const HEADER_H = 18;
const LINE_H = 14;
const PAD_X = 6;
const PAD_Y = 3; // below the last detail row
const ICON = 10;
const GAP = 4; // icon to text
const GAP_Y = 14; // clearance between candles and a card
const STACK = 4; // clearance between cards
const EDGE = 2; // pane edge inset
const SIDE_PENALTY = 60; // px cost of flipping a card to its non-preferred side

export interface Rect { left: number; top: number; w: number; h: number }

/** One card to place: its anchor on the bar extreme and its size. */
export interface CardRequest { x: number; y: number; above: boolean; w: number; h: number }

/** Candle column in pixels: x centre and wick top/bottom. */
export interface BarBox { x: number; top: number; bottom: number }

function overlaps(a: Rect, b: Rect, pad: number): boolean {
    return a.left < b.left + b.w + pad && b.left < a.left + a.w + pad
        && a.top < b.top + b.h + pad && b.top < a.top + a.h + pad;
}

/**
 * Places callout cards in time order so each clears the candles under its
 * horizontal span and every card placed before it. A card tries its preferred
 * side centred on the anchor, then shifted left/right, then the other side,
 * and takes the position closest to its anchor that fits in the pane.
 */
export function layoutCards(reqs: CardRequest[], bars: BarBox[], paneW: number, paneH: number): { rect: Rect; above: boolean }[] {
    const placed: Rect[] = [];
    const out: { rect: Rect; above: boolean }[] = [];

    const tryAt = (r: CardRequest, left: number, above: boolean): Rect => {
        // Span covers the card and the anchor bar, since the leader runs back to it.
        const x0 = Math.min(left - STACK, r.x);
        const x1 = Math.max(left + r.w + STACK, r.x);
        let top: number;
        if (above) {
            let hi = r.y;
            for (const b of bars) if (b.x >= x0 && b.x <= x1 && b.top < hi) hi = b.top;
            top = hi - GAP_Y - r.h;
        } else {
            let lo = r.y;
            for (const b of bars) if (b.x >= x0 && b.x <= x1 && b.bottom > lo) lo = b.bottom;
            top = lo + GAP_Y;
        }
        const rect = { left, top, w: r.w, h: r.h };
        // Push outward past earlier cards until clear; each push strictly moves away.
        for (let moved = true; moved;) {
            moved = false;
            for (const p of placed) {
                if (!overlaps(rect, p, STACK)) continue;
                rect.top = above ? p.top - STACK - rect.h : p.top + p.h + STACK;
                moved = true;
            }
        }
        return rect;
    };

    for (const r of reqs) {
        const clampLeft = (l: number) => Math.min(Math.max(l, EDGE), Math.max(EDGE, paneW - r.w - EDGE));
        const lefts = [...new Set([r.x - r.w / 2, r.x - r.w - GAP_Y, r.x + GAP_Y].map(l => Math.round(clampLeft(l))))];
        let best: { rect: Rect; above: boolean; score: number } | null = null;
        for (const above of [r.above, !r.above]) {
            for (const left of lefts) {
                const rect = tryAt(r, left, above);
                const fits = rect.top >= EDGE && rect.top + rect.h <= paneH - EDGE;
                const dy = above ? r.y - (rect.top + rect.h) : rect.top - r.y;
                const cx = rect.left + rect.w / 2;
                const score = dy + 0.5 * Math.abs(cx - r.x) + (above === r.above ? 0 : SIDE_PENALTY) + (fits ? 0 : 1e6);
                if (!best || score < best.score) best = { rect, above, score };
            }
        }
        const rect = best!.rect;
        rect.top = Math.min(Math.max(rect.top, EDGE), Math.max(EDGE, paneH - rect.h - EDGE));
        placed.push(rect);
        out.push({ rect, above: best!.above });
    }
    return out;
}

interface Placed {
    x: number; // anchor: bar centre
    y: number; // anchor: bar high (card above) or low (card below)
    above: boolean;
    rect: Rect;
    m: TradeMarker;
}

let measureCtx: CanvasRenderingContext2D | null | undefined;

/** Text width in px for a font; falls back to an estimate without a canvas (tests). */
function textWidth(text: string, font: string): number {
    if (measureCtx === undefined) {
        measureCtx = typeof document !== 'undefined' ? document.createElement('canvas').getContext('2d') : null;
    }
    if (!measureCtx) return text.length * 6;
    measureCtx.font = font;
    return measureCtx.measureText(text).width;
}

export function cardSize(m: TradeMarker): { w: number; h: number } {
    const text = m.label || m.side;
    let w = PAD_X * 2 + ICON + (text ? GAP + textWidth(text, FONT) : 0);
    for (const line of m.lines) w = Math.max(w, PAD_X * 2 + textWidth(line, DETAIL_FONT));
    const h = HEADER_H + (m.lines.length ? m.lines.length * LINE_H + PAD_Y : 0);
    return { w: Math.ceil(w), h };
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
            ctx.textBaseline = 'middle';
            // Leaders first so no card is crossed by another card's line.
            for (const p of this.placed) this.drawLeader(ctx, p);
            for (const p of this.placed) this.drawCard(ctx, p);
            ctx.restore();
        });
    }

    private drawLeader(ctx: CanvasRenderingContext2D, { x, y, above, rect, m }: Placed) {
        const color = MARKER_COLORS[m.kind];
        const ax = Math.round(x) + 0.5;
        const ay = above ? y - 2 : y + 2;
        // Attach on the card edge facing the bar, as close to the bar's x as the card allows.
        const cx = Math.round(Math.min(Math.max(x, rect.left + 6), rect.left + rect.w - 6)) + 0.5;
        const cy = above ? rect.top + rect.h : rect.top;
        const midY = Math.round((ay + cy) / 2) + 0.5;

        ctx.strokeStyle = color;
        ctx.globalAlpha = 0.7;
        ctx.lineWidth = 1;
        ctx.setLineDash(m.kind === 'refused' ? [2, 2] : []);
        ctx.beginPath();
        ctx.moveTo(cx, cy);
        if (Math.abs(cx - ax) < 1) {
            ctx.lineTo(ax, ay);
        } else {
            ctx.lineTo(cx, midY);
            ctx.lineTo(ax, midY);
            ctx.lineTo(ax, ay);
        }
        ctx.stroke();
        ctx.setLineDash([]);
        ctx.globalAlpha = 1;
        ctx.fillStyle = color;
        ctx.beginPath();
        ctx.arc(ax, ay, 2, 0, Math.PI * 2);
        ctx.fill();
    }

    private drawCard(ctx: CanvasRenderingContext2D, { rect, m }: Placed) {
        const color = MARKER_COLORS[m.kind];
        const refused = m.kind === 'refused';
        const { left, top, w, h } = rect;
        const text = m.label || m.side;

        roundRect(ctx, left + 0.5, top + 0.5, w - 1, h - 1, 4);
        ctx.fillStyle = BG;
        ctx.fill();
        if (!refused) {
            // Solid header band, clipped to the card's rounded outline.
            ctx.save();
            ctx.clip();
            ctx.fillStyle = color;
            ctx.fillRect(left, top, w, HEADER_H);
            ctx.restore();
        }
        roundRect(ctx, left + 0.5, top + 0.5, w - 1, h - 1, 4);
        ctx.strokeStyle = color;
        ctx.lineWidth = 1;
        ctx.stroke();

        const fg = refused ? color : '#0b0e14';
        ctx.strokeStyle = fg;
        drawIcon(ctx, m.kind, left + PAD_X + ICON / 2, top + HEADER_H / 2);
        ctx.font = FONT;
        if (text) {
            ctx.fillStyle = fg;
            ctx.fillText(text, left + PAD_X + ICON + GAP, top + HEADER_H / 2 + 0.5);
        }

        ctx.font = DETAIL_FONT;
        m.lines.forEach((line, i) => {
            const isPnL = m.pnl !== undefined && line.startsWith('P&L');
            ctx.fillStyle = isPnL
                ? (m.pnl! > 0 ? MARKER_COLORS.entry : m.pnl! < 0 ? MARKER_COLORS.exit : DETAIL_COLOR)
                : DETAIL_COLOR;
            ctx.fillText(line, left + PAD_X, top + HEADER_H + i * LINE_H + LINE_H / 2 + 1);
        });
    }
}

class PaneView implements ISeriesPrimitivePaneView {
    placed: Placed[] = [];
    zOrder() { return 'top' as const; }
    renderer() { return new Renderer(this.placed); }
}

/**
 * Trade markers drawn as callout cards joined to their bar by a leader line:
 * entries below price, exits and refused entries above, each placed clear of
 * nearby candles and earlier cards (see layoutCards).
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
        const chart = this.chart;
        const series = this.series;
        if (chart && series && this.markers.length) {
            const ts = chart.timeScale();
            const range = ts.getVisibleLogicalRange();
            const bars: BarBox[] = [];
            if (range) {
                for (let i = Math.max(0, Math.floor(range.from)); i <= Math.ceil(range.to); i++) {
                    const bar = series.dataByIndex(i);
                    if (!bar || !('high' in bar)) continue;
                    const x = ts.logicalToCoordinate(i as Logical);
                    const top = series.priceToCoordinate(bar.high);
                    const bottom = series.priceToCoordinate(bar.low);
                    if (x === null || top === null || bottom === null) continue;
                    bars.push({ x, top, bottom });
                }
            }

            const reqs: CardRequest[] = [];
            const anchors: TradeMarker[] = [];
            for (const m of this.markers) {
                const x = ts.timeToCoordinate(m.time as Time);
                if (x === null) continue;
                const logical = ts.coordinateToLogical(x);
                if (logical === null) continue;
                const bar = series.dataByIndex(Math.round(logical));
                if (!bar || !('high' in bar) || bar.time !== m.time) continue;
                const above = m.kind !== 'entry';
                const y = series.priceToCoordinate(above ? bar.high : bar.low);
                if (y === null) continue;
                reqs.push({ x, y, above, ...cardSize(m) });
                anchors.push(m);
            }

            const pane = chart.paneSize();
            layoutCards(reqs, bars, ts.width() || pane.width, pane.height).forEach(({ rect, above }, i) => {
                const r = reqs[i];
                // Anchor follows the side the card landed on.
                const bar = series.dataByIndex(Math.round(ts.coordinateToLogical(r.x) ?? 0));
                let y = r.y;
                if (above !== r.above && bar && 'high' in bar) {
                    y = series.priceToCoordinate(above ? bar.high : bar.low) ?? r.y;
                }
                placed.push({ x: r.x, y, above, rect, m: anchors[i] });
            });
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
        let tallest = 0;
        for (const m of this.markers) tallest = Math.max(tallest, cardSize(m).h);
        const margin = GAP_Y + tallest + STACK;
        return { priceRange: { minValue: lo, maxValue: hi }, margins: { above: margin, below: margin } };
    }
}
