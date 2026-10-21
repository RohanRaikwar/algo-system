import { PushToggle } from './PushToggle';
import { StatusDot } from './StatusDot';

/**
 * Phone-portrait page bar for Signals and Health. It replaces the brand
 * Header there; the dashboard uses the chart's own top bar instead.
 */
export function PageAppBar({ title }: { title: string }) {
    return (
        <header className="sticky top-0 z-[60] flex items-center gap-[4px] h-[calc(48px_+_env(safe-area-inset-top))] mt-[-12px] mx-[-12px] mb-[12px] pt-[env(safe-area-inset-top)] pr-[12px] pb-0 pl-[16px] bg-sg-bg border-b border-b-sg-line-soft select-none [-webkit-touch-callout:none]">
            <StatusDot />
            <h1 className="flex-1 min-w-0 m-0 text-sg-text text-[20px] font-bold tracking-[-0.01em]">{title}</h1>
            <div className="flex items-center gap-[6px]">
                <PushToggle />
            </div>
        </header>
    );
}
