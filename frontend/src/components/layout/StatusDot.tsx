import { useEffect, useState } from 'react';
import { useMarketFeedStatus } from '../../hooks/useMarketFeedStatus';
import { haptic } from '../../utils/haptic';
import styles from './StatusDot.module.css';

const TOAST_MS = 2000;

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
        <span className={styles.wrap}>
            <button
                type="button"
                className={styles.btn}
                aria-label={st.summary}
                title={st.summary}
                onClick={() => { haptic(); setToast(true); }}
            >
                <span className={`${styles.dot} ${styles[st.dot]}`} aria-hidden />
            </button>
            {toast && <span className={styles.toast} role="status">{st.summary}</span>}
        </span>
    );
}
