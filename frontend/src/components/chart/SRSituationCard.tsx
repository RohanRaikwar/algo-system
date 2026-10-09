import { useSRStore } from '../../store/useSRStore';
import { REGIME_LABEL, situationLine, warnSummary } from '../sr/srFormat';
import { haptic } from '../../utils/haptic';
import styles from './SRSituationCard.module.css';

interface SRSituationCardProps {
    selectedToken: string | null;
    price: number | null;
    /** Phone: render a compact tappable chip that opens the SR sheet. */
    onOpen?: () => void;
}

/** Top-left chart card: SR's read of the market now, and what a new entry would be warned about. */
export function SRSituationCard({ selectedToken, price, onOpen }: SRSituationCardProps) {
    const v = useSRStore(s => s.view);
    if (!v || v.key !== selectedToken) return null;
    const warn = warnSummary(v.warn);

    if (onOpen) {
        // Full situation text lives in the SR sheet; the chip keeps the chart clear.
        return (
            <button
                type="button"
                className={styles.chip}
                onClick={() => { haptic(); onOpen(); }}
                aria-label={`SR situation: ${situationLine(v, price)}${warn ? `. Warning: ${warn}` : ''}. Open details`}
            >
                <span>SR · {REGIME_LABEL[v.regime] ?? v.regime}</span>
                {warn && <span className={styles.warn}>⚠ {warn}</span>}
            </button>
        );
    }

    return (
        <div className={styles.card} aria-label="SR situation">
            <span>{situationLine(v, price)}</span>
            {warn && <span className={styles.warn}>⚠ {warn}</span>}
        </div>
    );
}
