import { Zap, LayoutDashboard, BarChart3, Activity, Send } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import { useMarketFeedStatus, type Tone } from '../../hooks/useMarketFeedStatus';
import { SignalBadge } from '../signals/SignalBadge';
import { PushToggle } from './PushToggle';
import { KeepAwakeToggle } from './KeepAwakeToggle';
import { cn } from '@/lib/utils';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Badge } from '@/components/ui/badge';

/** Pill text colour per tone (only "down" recolours the pill itself). */
const TONE_TEXT: Record<Tone, string> = {
    up: 'text-sg-text-2',
    down: 'text-sg-loss',
    warn: 'text-sg-text-2',
    muted: 'text-sg-text-2',
};

const TONE_DOT: Record<Tone, string> = {
    up: 'bg-sg-profit',
    down: 'bg-sg-loss',
    warn: 'bg-sg-warn',
    muted: 'bg-sg-faint',
};

const navClass = ({ isActive }: { isActive: boolean }) => cn(
    'relative inline-flex items-center gap-[7px] h-full px-[12px] text-[0.88rem] font-medium no-underline whitespace-nowrap transition-[color] duration-150 ease-[ease] hover:text-sg-text focus-visible:outline-none focus-visible:shadow-[inset_0_0_0_2px_var(--sg-accent)] focus-visible:rounded-ctrl',
    isActive
        ? "text-sg-text after:content-[''] after:absolute after:left-[10px] after:right-[10px] after:bottom-[-1px] after:h-[2px] after:rounded-[2px_2px_0_0] after:bg-sg-accent [&_svg]:text-sg-accent"
        : 'text-sg-muted',
);

/* Status pills are Badge variant="header"; display comes from the caller. */
const widePill = 'inline-flex lte-640:hidden phone-land:hidden';
const compactPill = 'hidden lte-640:inline-flex phone-land:inline-flex';
const dot = 'w-[7px] h-[7px] rounded-[50%] shrink-0';
const dotLive = 'animate-[layout-header-pulse_2s_ease-out_infinite] motion-reduce:animate-none';

export function Header() {
    const st = useMarketFeedStatus();
    const { marketTone, feedTone, marketLabel, nextOpen, feedLabel, connected, compactLabel } = st;
    const compactBg = st.open ? 'bg-profit' : st.holiday ? 'bg-warn' : 'bg-raised';

    return (
        <header className="sticky top-0 z-[100] flex items-center gap-[20px] h-[52px] px-[16px] bg-sg-surface border-b border-b-sg-line shrink-0 lte-640:h-[44px] lte-640:gap-[10px] lte-640:px-[12px] phone-land:h-[40px] phone-land:gap-[12px] phone-land:px-[12px]">
            <div className="flex items-center gap-[7px] shrink-0 phone-land:hidden">
                <Zap size={16} className="text-sg-warn" aria-hidden />
                <span className="text-sg-text text-[1rem] font-bold tracking-[-0.01em] lte-640:text-[15px] lte-640:font-semibold">Levels</span>
            </div>

            <nav className="flex items-center gap-[2px] h-full min-w-0 lte-640:hidden" aria-label="Primary">
                <NavLink to="/" end className={navClass}>
                    <LayoutDashboard size={15} /> Dashboard
                </NavLink>
                <NavLink to="/signals" className={navClass}>
                    <BarChart3 size={15} /> Signals <SignalBadge />
                </NavLink>
                <NavLink to="/telegram" className={navClass}>
                    <Send size={15} /> Telegram
                </NavLink>
                <NavLink to="/health" className={navClass}>
                    <Activity size={15} /> Health
                </NavLink>
            </nav>

            <div className="flex items-center gap-[8px] ml-auto min-w-0 lte-640:gap-[6px]">
                <KeepAwakeToggle />
                <PushToggle />
                <Tooltip>
                    <TooltipTrigger render={<Badge variant="header" tone="bg-raised" className={cn(widePill, TONE_TEXT[marketTone])} />}>
                        <span className={cn(dot, TONE_DOT[marketTone])} aria-hidden />
                        {marketLabel}
                        {nextOpen && <span className="text-sg-faint font-normal overflow-hidden text-ellipsis lte-900:hidden">{nextOpen}</span>}
                    </TooltipTrigger>
                    <TooltipContent side="bottom">{nextOpen || marketLabel}</TooltipContent>
                </Tooltip>
                <Tooltip>
                    <TooltipTrigger render={<Badge variant="header" tone="bg-raised" className={cn(widePill, TONE_TEXT[feedTone])} />}>
                        <span className={cn(dot, TONE_DOT[feedTone], connected && dotLive)} aria-hidden />
                        {feedLabel}
                    </TooltipTrigger>
                    <TooltipContent side="bottom">Price feed connection</TooltipContent>
                </Tooltip>
                <Tooltip>
                    <TooltipTrigger
                        render={(
                            <Badge
                                variant="header"
                                tone={compactBg}
                                className={cn(compactPill, TONE_TEXT[feedTone])}
                                aria-label={`${marketLabel}${nextOpen ? `, ${nextOpen}` : ''}. Price feed ${feedLabel.toLowerCase()}.`}
                            />
                        )}
                    >
                        <span className={cn(dot, TONE_DOT[feedTone], connected && dotLive)} aria-hidden />
                        {compactLabel}
                    </TooltipTrigger>
                    <TooltipContent side="bottom">{st.summary}</TooltipContent>
                </Tooltip>
            </div>
        </header>
    );
}
