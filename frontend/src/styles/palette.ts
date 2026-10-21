// Canvas copies of the design tokens in global.css (:root --sg-*).
// lightweight-charts paints to canvas and needs literal colours, so these
// cannot be CSS variables. Keep the values in step with global.css.
export const SG = {
    surface: '#151b25',
    line: '#263041',
    text2: '#b8c0cd',
    muted: '#8a94a6',
    profit: '#3ecf8e',
    loss: '#f0616d',
    warn: '#e5a23a',
    info: '#4fb8d6',
} as const;

// Chart-only colours with no CSS token.
export const CHART = {
    grid: 'rgba(138, 148, 166, 0.07)', // muted @ 7%
    crosshair: 'rgba(184, 192, 205, 0.35)', // text-2 @ 35%
    crosshairLabel: '#2b3546',
    prevDay: '#6b7385',
    markerDetail: '#c9d1dc',
    markerInk: '#0b0e14', // text on filled marker labels
    boxFill: { boxed: 'rgba(229, 162, 58, 0.14)', moving: 'rgba(138, 148, 166, 0.06)' }, // warn / muted
    boxEdge: { boxed: 'rgba(229, 162, 58, 0.7)', moving: 'rgba(138, 148, 166, 0.35)' },
} as const;
