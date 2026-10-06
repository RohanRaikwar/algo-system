import { forwardRef } from 'react';
import { ChevronUp } from 'lucide-react';
import { useSRStore } from '../../store/useSRStore';
import { REGIME_LABEL, peekSummary } from './srFormat';
import { useLastNiftyPaise } from './useLastNiftyPaise';
import styles from './SRMobile.module.css';

interface SRPeekProps {
    open: boolean;
    sheetId: string;
    onOpen: () => void;
}

/** Phone strip above the tab bar: strategy state at a glance; tap opens the sheet. */
export const SRPeek = forwardRef<HTMLButtonElement, SRPeekProps>(function SRPeek({ open, sheetId, onOpen }, ref) {
    const v = useSRStore(s => s.view);
    const last = useLastNiftyPaise(v?.key);

    if (!v) {
        return (
            <button ref={ref} type="button" className={styles.peek} onClick={onOpen}
                aria-expanded={open} aria-controls={sheetId}>
                <span className={styles.handle} aria-hidden />
                <span className={styles.peekRow}>
                    <span className={styles.peekTitle}>Strategy</span>
                    <span className={styles.muted}>Waiting for engine…</span>
                    <ChevronUp size={16} className={styles.chevron} aria-hidden />
                </span>
            </button>
        );
    }

    const p = peekSummary(v, last);

    return (
        <button ref={ref} type="button" className={styles.peek} onClick={onOpen}
            aria-expanded={open} aria-controls={sheetId} aria-label="S/R strategy details">
            <span className={styles.handle} aria-hidden />
            <span className={styles.peekRow}>
                <span className={`${styles.pill} ${styles[`regime${v.regime}`] ?? ''}`}>{REGIME_LABEL[v.regime] ?? v.regime}</span>
                <span className={`${styles.position} ${styles[p.position.tone]}`}>{p.position.text}</span>
                {p.alert ? (
                    <span className={styles.alert}>{p.alert}</span>
                ) : (
                    <span className={styles.peekStats}>
                        {p.stats.map(s => (
                            <span key={s.label} className={styles.peekStat}>
                                <span className={styles.muted}>{s.label}</span> {s.value}
                            </span>
                        ))}
                    </span>
                )}
                <ChevronUp size={16} className={styles.chevron} aria-hidden />
            </span>
        </button>
    );
});
