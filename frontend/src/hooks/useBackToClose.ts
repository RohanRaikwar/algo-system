import { useEffect, useRef } from 'react';

/**
 * Android back / browser back closes the top-most open sheet instead of
 * leaving the page. Each open sheet pushes one history entry (same URL, so
 * no route change); back pops it and closes only the top sheet.
 */

type Entry = { key: string; close: () => void };
const stack: Entry[] = [];
let ignorePops = 0;
let listening = false;
let seq = 0;

function onPop() {
    if (ignorePops > 0) {
        ignorePops--;
        return;
    }
    stack.pop()?.close();
}

function sheetKeyOnTop(): string | undefined {
    return (window.history.state as { sheet?: string } | null)?.sheet;
}

export function useBackToClose(open: boolean, close: () => void): void {
    const key = useRef(`sheet-${++seq}`);
    const closeRef = useRef(close);
    closeRef.current = close;

    useEffect(() => {
        if (!open || typeof window === 'undefined') return;
        if (!listening) {
            window.addEventListener('popstate', onPop);
            listening = true;
        }
        const k = key.current;
        const entry: Entry = { key: k, close: () => closeRef.current() };
        window.history.pushState({ ...(window.history.state ?? {}), sheet: k }, '');
        stack.push(entry);

        return () => {
            const i = stack.indexOf(entry);
            if (i < 0) return; // closed by back: its entry is already gone
            stack.splice(i, 1);
            // Closed by tap/swipe: drop our entry. Skip when a route change
            // already pushed over it, or back would undo the navigation.
            if (sheetKeyOnTop() === k) {
                ignorePops++;
                window.history.back();
            }
        };
    }, [open]);
}
