import { useEffect, useState } from 'react';
import { useMarketFeedStatus } from '../../hooks/useMarketFeedStatus';
import { haptic } from '../../utils/haptic';
import { cn } from '@/lib/utils';
import type { DotState } from '../../hooks/useMarketFeedStatus';

const TOAST_MS = 2000;

const DOT_BG: Record<DotState, string> = {
    live: 'bg-sg-profit animate-[layout-status-pulse_2s_ease-in-out_infinite] motion-reduce:animate-none',
    open: 'bg-sg-profit',
    holiday: 'bg-sg-warn',
    reconnecting: 'bg-sg-loss',
    closed: 'bg-sg-faint',
};

/**
 * Phone replacement for the "Open · Live" pill: one colored dot. A tap shows
 * the full market + feed sentence for two seconds.
 */
export function StatusDot() {
    const st = useMarketFeedStatus();
    const [toast, setToast] = useState(false);

    useEffect(() => {
        if (!toast) return;
        const t = setTimeout(() => setToast(false), TOAST_MS);
        return () => clearTimeout(t);
    }, [toast]);

    return (
        <span className="relative inline-flex shrink-0">
            <button
                type="button"
                className="inline-flex items-center justify-center w-[32px] h-[32px] ml-[-10px] p-0 border-0 rounded-pill bg-transparent cursor-pointer [-webkit-tap-highlight-color:transparent] active:bg-sg-hover focus-visible:outline-none focus-visible:shadow-ring"
                aria-label={st.summary}
                title={st.summary}
                onClick={() => { haptic(); setToast(true); }}
            >
                <span className={cn('w-[10px] h-[10px] rounded-[50%]', DOT_BG[st.dot])} aria-hidden />
            </button>
            {toast && <span className="absolute top-[calc(100%_+_6px)] left-[-6px] z-[200] py-[6px] px-[10px] rounded-ctrl bg-sg-raised border border-solid border-sg-line shadow-panel text-sg-text text-[12px] font-medium whitespace-nowrap pointer-events-none" role="status">{st.summary}</span>}
        </span>
    );
}
