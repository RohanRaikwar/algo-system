import { useRef, useState, type PointerEvent as ReactPointerEvent, type RefObject } from 'react';

/** Upward speed in px/ms; negative is a downward flick. */
export const FLICK_SPEED = 0.5;

export interface SheetRelease {
    /** Sheet height at release, px. */
    height: number;
    /** Height when the drag started, px. */
    startHeight: number;
    /** Upward velocity at release (px/ms); negative = moving down. */
    velocity: number;
    /** False for a tap (moved under 8px). */
    moved: boolean;
}

/** Ignore pointer-downs on controls inside the drag area. */
const INTERACTIVE = 'button, a, input, select, textarea, label';

/**
 * Pointer drag for bottom sheets: the sheet follows the finger, and the
 * caller decides the snap from height and flick velocity on release.
 */
export function useSheetDrag(
    sheetRef: RefObject<HTMLElement | null>,
    maxHeight: () => number,
    onRelease: (r: SheetRelease) => void,
) {
    const drag = useRef<{ startY: number; startH: number; lastY: number; lastT: number; v: number } | null>(null);
    const [dragH, setDragH] = useState<number | null>(null);
    const [startH, setStartH] = useState<number | null>(null);

    const onPointerDown = (e: ReactPointerEvent<HTMLElement>) => {
        if ((e.target as HTMLElement).closest(INTERACTIVE)) return;
        const el = sheetRef.current;
        if (!el) return;
        const h = el.getBoundingClientRect().height;
        drag.current = { startY: e.clientY, startH: h, lastY: e.clientY, lastT: e.timeStamp, v: 0 };
        setStartH(h);
        e.currentTarget.setPointerCapture(e.pointerId);
    };

    const onPointerMove = (e: ReactPointerEvent<HTMLElement>) => {
        const d = drag.current;
        if (!d) return;
        const dt = e.timeStamp - d.lastT;
        if (dt > 0) {
            // Smoothed so one jittery sample does not decide the snap.
            d.v = 0.7 * ((d.lastY - e.clientY) / dt) + 0.3 * d.v;
            d.lastY = e.clientY;
            d.lastT = e.timeStamp;
        }
        const h = d.startH + (d.startY - e.clientY);
        setDragH(Math.max(0, Math.min(h, maxHeight())));
    };

    const onPointerUp = (e: ReactPointerEvent<HTMLElement>) => {
        const d = drag.current;
        if (!d) return;
        drag.current = null;
        const height = dragH ?? d.startH;
        const moved = dragH !== null && Math.abs(dragH - d.startH) > 8;
        // Finger held still before lifting: no flick.
        const velocity = moved && e.timeStamp - d.lastT < 100 ? d.v : 0;
        setDragH(null);
        setStartH(null);
        onRelease({ height, startHeight: d.startH, velocity, moved });
    };

    return {
        dragH,
        /** Height when the current drag started; null when not dragging. */
        startH,
        handlers: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel: onPointerUp },
    };
}
