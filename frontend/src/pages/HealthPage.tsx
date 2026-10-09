import { useSearchParams } from 'react-router-dom';
import { SystemHealth } from '../components/health/SystemHealth';
import { PageAppBar } from '../components/layout/PageAppBar';
import { AccountTab } from '../components/signals/AccountTab';
import { SessionHealthTab } from '../components/signals/SessionHealthTab';
import { TradingSettingsTab } from '../components/signals/TradingSettingsTab';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';
import { haptic } from '../utils/haptic';
import '../components/signals/signals.css';
import styles from './HealthPage.module.css';

type Section = 'system' | 'account' | 'sessions' | 'settings';

const SECTIONS: { id: Section; label: string }[] = [
    { id: 'system', label: 'System' },
    { id: 'account', label: 'Account' },
    { id: 'sessions', label: 'Sessions' },
    { id: 'settings', label: 'Settings' },
];

export function HealthPage() {
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    const [params, setParams] = useSearchParams();
    if (!isPhone) return <SystemHealth />;

    // Phone: the system tabs that used to hide off-screen on Signals live here.
    const section = SECTIONS.find(s => s.id === params.get('s'))?.id ?? 'system';
    const pick = (s: Section) => {
        if (s === section) return;
        haptic();
        setParams(s === 'system' ? {} : { s }, { replace: true });
        window.scrollTo({ top: 0 });
    };

    return (
        <>
            <PageAppBar title="Health" />
            <div className={styles.segRow}>
                <div className={styles.seg} role="tablist" aria-label="Health sections">
                    {SECTIONS.map(s => (
                        <button
                            key={s.id}
                            type="button"
                            role="tab"
                            aria-selected={section === s.id}
                            className={`${styles.segBtn}${section === s.id ? ` ${styles.segActive}` : ''}`}
                            onClick={() => pick(s.id)}
                        >
                            {s.label}
                        </button>
                    ))}
                </div>
            </div>
            {section === 'system' && <SystemHealth />}
            {section !== 'system' && (
                <div className="signals-page">
                    {section === 'account' && <AccountTab />}
                    {section === 'sessions' && <SessionHealthTab />}
                    {section === 'settings' && <TradingSettingsTab />}
                </div>
            )}
        </>
    );
}
