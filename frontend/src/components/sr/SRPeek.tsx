import { forwardRef } from 'react';
import { ChevronUp } from 'lucide-react';
import { useSelectedSRView } from '../../store/useSRStore';
import { REGIME_LABEL, peekSummary } from './srFormat';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import { cn } from '@/lib/utils';
import { REGIME_CLASS, TONE_CLASS, mobilePill, sheetHandle } from './srClasses';

const peek = 'flex flex-col items-stretch shrink-0 w-full h-[56px] pt-[4px] px-[12px] pb-0 border-t border-t-sg-line bg-sg-surface text-sg-text-2 text-[13px] text-left cursor-pointer [-webkit-tap-highlight-color:transparent] focus-visible:outline-none focus-visible:shadow-[inset_0_0_0_2px_var(--sg-accent)] phone-land:absolute phone-land:top-[52px] phone-land:right-[72px] phone-land:z-[5] phone-land:w-auto phone-land:h-[32px] phone-land:py-0 phone-land:px-[10px] phone-land:border phone-land:border-sg-line phone-land:rounded-pill phone-land:bg-[rgba(21,27,37,0.9)]';
const peekRow = 'flex items-center gap-[10px] flex-1 min-w-0 whitespace-nowrap phone-land:gap-[8px]';
const chevron = 'shrink-0 ml-auto text-sg-muted';

interface SRPeekProps {
    open: boolean;
    sheetId: string;
    onOpen: () => void;
}

/** Phone strip above the tab bar: strategy state at a glance; tap opens the sheet. */
export const SRPeek = forwardRef<HTMLButtonElement, SRPeekProps>(function SRPeek({ open, sheetId, onOpen }, ref) {
    const v = useSelectedSRView();
    const last = useLastNiftyPaise(v?.key);

    if (!v) {
        return (
            <button ref={ref} type="button" className={peek} onClick={onOpen}
                aria-expanded={open} aria-controls={sheetId}>
                <span className={sheetHandle} aria-hidden />
                <span className={peekRow}>
                    <span className="text-sg-text font-semibold">Strategy</span>
                    <span className="text-sg-muted">Waiting for engine…</span>
                    <ChevronUp size={16} className={chevron} aria-hidden />
                </span>
            </button>
        );
    }

    const p = peekSummary(v, last);

    return (
        <button ref={ref} type="button" className={peek} onClick={onOpen}
            aria-expanded={open} aria-controls={sheetId} aria-label="S/R strategy details">
            <span className={sheetHandle} aria-hidden />
            <span className={peekRow}>
                <span className={cn(mobilePill, REGIME_CLASS[v.regime])}>{REGIME_LABEL[v.regime] ?? v.regime}</span>
                <span className={cn('shrink-0 font-semibold tabular-nums', TONE_CLASS[p.position.tone])}>{p.position.text}</span>
                {p.alert ? (
                    <span className="flex-1 min-w-0 overflow-hidden text-ellipsis text-sg-warn phone-land:hidden">{p.alert}</span>
                ) : (
                    <span className="flex gap-[10px] min-w-0 overflow-hidden tabular-nums phone-land:hidden">
                        {p.stats.map(s => (
                            <span key={s.label} className="shrink-0 text-sg-text lte-380:nth-3:hidden lte-330:nth-2:hidden">
                                <span className="text-sg-muted">{s.label}</span> {s.value}
                            </span>
                        ))}
                    </span>
                )}
                <ChevronUp size={16} className={chevron} aria-hidden />
            </span>
        </button>
    );
});
