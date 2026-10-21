import { useEffect, useState } from 'react';
import { useMarketFeedStatus } from '../../hooks/useMarketFeedStatus';
import { haptic } from '../../utils/haptic';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
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
    // Hover/focus shows the same bubble (was the native title).
    const [hover, setHover] = useState(false);

    useEffect(() => {
        if (!toast) return;
        const t = setTimeout(() => setToast(false), TOAST_MS);
        return () => clearTimeout(t);
    }, [toast]);

    return (
        <span className="relative inline-flex shrink-0">
            <Tooltip open={toast || hover} onOpenChange={setHover}>
                <TooltipTrigger
                    closeOnClick={false}
                    render={(
                        <Button
                            variant="ghost-touch"
                            size="icon-32"
                            className="ml-[-10px] [-webkit-tap-highlight-color:transparent]"
                            aria-label={st.summary}
                            onClick={() => { haptic(); setToast(true); }}
                        />
                    )}
                >
                    <span className={cn('w-[10px] h-[10px] rounded-[50%]', DOT_BG[st.dot])} aria-hidden />
                </TooltipTrigger>
                {/* Left edge 4px right of the button (6px left of the old wrapper, which the -10px margin shifts). */}
                <TooltipContent side="bottom" align="start" alignOffset={4} role="status" className="pointer-events-none">
                    {st.summary}
                </TooltipContent>
            </Tooltip>
        </span>
    );
}
