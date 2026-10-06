import { useSRStore } from '../../store/useSRStore';
import { situationLine, warnSummary } from '../sr/srFormat';
import styles from './SRSituationCard.module.css';

/** Top-left chart card: SR's read of the market now, and what a new entry would be warned about. */
export function SRSituationCard({ selectedToken, price }: { selectedToken: string | null; price: number | null }) {
    const v = useSRStore(s => s.view);
    if (!v || v.key !== selectedToken) return null;
    const warn = warnSummary(v.warn);
    return (
        <div className={styles.card} aria-label="SR situation">
            <span>{situationLine(v, price)}</span>
            {warn && <span className={styles.warn}>⚠ {warn}</span>}
        </div>
    );
}
