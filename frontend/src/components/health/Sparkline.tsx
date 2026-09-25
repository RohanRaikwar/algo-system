import { useMemo, useState, type KeyboardEvent, type PointerEvent } from 'react';
import styles from './Health.module.css';

interface Point {
    at: number;
    value: number | null;
}

interface SparklineProps {
    points: Point[];
    /** Accessible name, e.g. "Ticks per second, last 5 minutes". */
    label: string;
    format: (v: number) => string;
    height?: number;
}

const W = 300; // viewBox width; the SVG stretches to its container

function fmtClock(ms: number): string {
    return new Date(ms).toLocaleTimeString('en-IN', {
        hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, timeZone: 'Asia/Kolkata',
    });
}

/**
 * Single-series trend line with a crosshair tooltip. Null values break the
 * line (no data) rather than drawing a false zero.
 */
export function Sparkline({ points, label, format, height = 40 }: SparklineProps) {
    const [hover, setHover] = useState<number | null>(null);
    const pad = 3;

    const geo = useMemo(() => {
        const vals = points.map(p => p.value).filter((v): v is number => v !== null);
        if (points.length < 2 || vals.length === 0) return null;
        const t0 = points[0].at;
        const t1 = points[points.length - 1].at;
        const span = Math.max(1, t1 - t0);
        const max = Math.max(...vals);
        const min = Math.min(0, ...vals);
        const range = max - min || 1;
        const x = (at: number) => ((at - t0) / span) * W;
        const y = (v: number) => pad + (1 - (v - min) / range) * (height - pad * 2);

        const segments: string[] = [];
        let cur = '';
        for (const p of points) {
            if (p.value === null) {
                if (cur) segments.push(cur);
                cur = '';
                continue;
            }
            cur += `${cur ? 'L' : 'M'}${x(p.at).toFixed(1)},${y(p.value).toFixed(1)}`;
        }
        if (cur) segments.push(cur);
        const spanLabel = span < 60_000 ? `${Math.round(span / 1000)}s ago` : `${Math.round(span / 60_000)} min ago`;
        return { d: segments.join(' '), x, y, max, spanLabel, base: y(0) };
    }, [points, height]);

    if (!geo) {
        return <div className={styles.sparkEmpty} style={{ height }}>Collecting data…</div>;
    }

    const nearest = (clientX: number, rect: DOMRect) => {
        const rel = ((clientX - rect.left) / rect.width) * W;
        let best = 0;
        let bestDist = Infinity;
        points.forEach((p, i) => {
            const d = Math.abs(geo.x(p.at) - rel);
            if (d < bestDist) { bestDist = d; best = i; }
        });
        return best;
    };

    const onMove = (e: PointerEvent<SVGSVGElement>) => setHover(nearest(e.clientX, e.currentTarget.getBoundingClientRect()));
    const onKey = (e: KeyboardEvent<SVGSVGElement>) => {
        if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
        e.preventDefault();
        setHover(h => {
            const i = h ?? points.length - 1;
            return Math.max(0, Math.min(points.length - 1, i + (e.key === 'ArrowRight' ? 1 : -1)));
        });
    };

    const hp = hover !== null ? points[hover] : null;
    const hx = hp ? geo.x(hp.at) : 0;
    const leftPct = hp ? (hx / W) * 100 : 0;

    return (
        <div className={styles.spark}>
            <svg
                viewBox={`0 0 ${W} ${height}`}
                preserveAspectRatio="none"
                width="100%"
                height={height}
                role="img"
                aria-label={label}
                tabIndex={0}
                onPointerMove={onMove}
                onPointerLeave={() => setHover(null)}
                onKeyDown={onKey}
                onBlur={() => setHover(null)}
            >
                <line x1={0} x2={W} y1={geo.base} y2={geo.base} className={styles.sparkBase} vectorEffect="non-scaling-stroke" />
                <path d={geo.d} className={styles.sparkLine} vectorEffect="non-scaling-stroke" />
                {hp && (
                    <line x1={hx} x2={hx} y1={0} y2={height} className={styles.sparkCross} vectorEffect="non-scaling-stroke" />
                )}
            </svg>
            {hp && hp.value !== null && (
                <span className={styles.sparkDot} style={{ left: `${leftPct}%`, top: geo.y(hp.value) }} />
            )}
            {hp && (
                <div
                    className={styles.sparkTip}
                    style={{ left: `${Math.min(80, Math.max(20, leftPct))}%` }}
                >
                    <b>{hp.value === null ? 'No data' : format(hp.value)}</b> {fmtClock(hp.at)}
                </div>
            )}
            <div className={styles.sparkAxis}>
                <span>{geo.spanLabel}</span>
                <span>peak {format(geo.max)}</span>
                <span>now</span>
            </div>
        </div>
    );
}
