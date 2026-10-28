import { LayoutDashboard, BarChart3, Activity, Send } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import { SignalBadge } from '../signals/SignalBadge';
import { haptic } from '../../utils/haptic';
import { cn } from '@/lib/utils';

const linkClass = ({ isActive }: { isActive: boolean }) => cn(
    'group flex flex-col items-center justify-center gap-[3px] h-[56px] text-[11px] no-underline [-webkit-tap-highlight-color:transparent] select-none [-webkit-touch-callout:none] focus-visible:outline-none focus-visible:shadow-[inset_0_0_0_2px_var(--sg-accent)]',
    isActive ? 'text-sg-text font-semibold [&_svg]:text-sg-accent' : 'text-sg-faint font-medium',
);

/* Active tab (NavLink sets aria-current="page"): soft pill behind the icon.
   The unread count sits on the icon's top-right corner. */
const iconWrap = 'relative flex items-center justify-center w-[52px] h-[28px] rounded-pill [transition:background_0.15s,transform_0.1s] motion-reduce:[transition:none] group-active:[transform:scale(0.92)] group-aria-[current=page]:bg-sg-accent-soft [&>span]:absolute [&>span]:top-[-4px] [&>span]:left-[30px] [&>span]:m-0';

/** Bottom tab bar for phones; hidden above 640px where the header nav shows. */
export function MobileNav() {
    return (
        <nav className="hidden lte-640:sticky lte-640:bottom-0 lte-640:z-[100] lte-640:grid lte-640:grid-cols-[repeat(4,1fr)] lte-640:shrink-0 lte-640:pb-[env(safe-area-inset-bottom)] lte-640:bg-sg-surface lte-640:border-t lte-640:border-t-sg-line" aria-label="Primary" onClick={(e) => { if ((e.target as HTMLElement).closest('a')) haptic(); }}>
            <NavLink to="/" end className={linkClass}>
                <span className={iconWrap}>
                    <LayoutDashboard size={20} />
                </span>
                <span>Chart</span>
            </NavLink>
            <NavLink to="/signals" className={linkClass}>
                <span className={iconWrap}>
                    <BarChart3 size={20} />
                    <SignalBadge />
                </span>
                <span>Signals</span>
            </NavLink>
            <NavLink to="/telegram" className={linkClass}>
                <span className={iconWrap}>
                    <Send size={20} />
                </span>
                <span>Telegram</span>
            </NavLink>
            <NavLink to="/health" className={linkClass}>
                <span className={iconWrap}>
                    <Activity size={20} />
                </span>
                <span>Health</span>
            </NavLink>
        </nav>
    );
}
