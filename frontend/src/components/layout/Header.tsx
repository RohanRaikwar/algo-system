import { Zap, LayoutDashboard, BarChart3, Activity } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import { useMarketFeedStatus, type Tone } from '../../hooks/useMarketFeedStatus';
import { SignalBadge } from '../signals/SignalBadge';
import { PushToggle } from './PushToggle';
import styles from './Header.module.css';

const TONE_CLASS: Record<Tone, string> = {
    up: styles.toneUp,
    down: styles.toneDown,
    warn: styles.toneWarn,
    muted: styles.toneMuted,
};

const navClass = ({ isActive }: { isActive: boolean }) =>
    `${styles.navLink}${isActive ? ` ${styles.navLinkActive}` : ''}`;

export function Header() {
    const st = useMarketFeedStatus();
    const marketTone = TONE_CLASS[st.marketTone];
    const feedTone = TONE_CLASS[st.feedTone];
    const { marketLabel, nextOpen, feedLabel, connected, compactLabel } = st;
    const compactTone = st.open ? styles.bgUp : st.holiday ? styles.bgWarn : '';

    return (
        <header className={styles.header}>
            <div className={styles.brand}>
                <Zap size={16} className={styles.brandIcon} aria-hidden />
                <span className={styles.logo}>Levels</span>
            </div>

            <nav className={styles.nav} aria-label="Primary">
                <NavLink to="/" end className={navClass}>
                    <LayoutDashboard size={15} /> Dashboard
                </NavLink>
                <NavLink to="/signals" className={navClass}>
                    <BarChart3 size={15} /> Signals <SignalBadge />
                </NavLink>
                <NavLink to="/health" className={navClass}>
                    <Activity size={15} /> Health
                </NavLink>
            </nav>

            <div className={styles.status}>
                <PushToggle />
                <span className={`${styles.pill} ${styles.widePill} ${marketTone}`} title={nextOpen || marketLabel}>
                    <span className={styles.dot} aria-hidden />
                    {marketLabel}
                    {nextOpen && <span className={styles.pillSub}>{nextOpen}</span>}
                </span>
                <span className={`${styles.pill} ${styles.widePill} ${feedTone}`} title="Price feed connection">
                    <span className={`${styles.dot}${connected ? ` ${styles.dotLive}` : ''}`} aria-hidden />
                    {feedLabel}
                </span>
                <span
                    className={`${styles.pill} ${styles.compactPill} ${feedTone} ${compactTone}`}
                    title={st.summary}
                    aria-label={`${marketLabel}${nextOpen ? `, ${nextOpen}` : ''}. Price feed ${feedLabel.toLowerCase()}.`}
                >
                    <span className={`${styles.dot}${connected ? ` ${styles.dotLive}` : ''}`} aria-hidden />
                    {compactLabel}
                </span>
            </div>
        </header>
    );
}
