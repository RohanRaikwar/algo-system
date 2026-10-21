import { useSRViewFor } from '../../store/useSRStore';
import { REGIME_LABEL, situationLine, warnSummary } from '../sr/srFormat';
import { haptic } from '../../utils/haptic';

interface SRSituationCardProps {
    selectedToken: string | null;
    price: number | null;
    /** Phone: render a compact tappable chip that opens the SR sheet. */
    onOpen?: () => void;
}

/** Top-left chart card: SR's read of the market now, and what a new entry would be warned about. */
export function SRSituationCard({ selectedToken, price, onOpen }: SRSituationCardProps) {
    const v = useSRViewFor(selectedToken);
    if (!v || v.key !== selectedToken) return null;
    const warn = warnSummary(v.warn);

    if (onOpen) {
        // Full situation text lives in the SR sheet; the chip keeps the chart clear.
        return (
            <button
                type="button"
                className="inline-flex items-center gap-[8px] max-w-full h-[26px] py-0 px-[10px] rounded-pill bg-[color:color-mix(in_srgb,var(--sg-surface)_88%,transparent)] border border-solid border-sg-line text-sg-text-2 leading-[inherit] text-[11px] font-medium whitespace-nowrap overflow-hidden text-ellipsis pointer-events-auto cursor-pointer [-webkit-tap-highlight-color:transparent] [&>span]:overflow-hidden [&>span]:text-ellipsis active:bg-sg-hover focus-visible:outline-none focus-visible:shadow-ring"
                onClick={() => { haptic(); onOpen(); }}
                aria-label={`SR situation: ${situationLine(v, price)}${warn ? `. Warning: ${warn}` : ''}. Open details`}
            >
                <span>SR · {REGIME_LABEL[v.regime] ?? v.regime}</span>
                {warn && <span className="text-sg-warn font-semibold">⚠ {warn}</span>}
            </button>
        );
    }

    return (
        <div className="flex flex-col gap-[2px] max-w-full py-[4px] px-[8px] rounded-ctrl bg-[color:color-mix(in_srgb,var(--sg-surface)_85%,transparent)] border border-solid border-sg-line text-[11px] text-sg-text-2 tabular-nums pointer-events-none" aria-label="SR situation">
            <span>{situationLine(v, price)}</span>
            {warn && <span className="text-sg-warn font-semibold">⚠ {warn}</span>}
        </div>
    );
}
