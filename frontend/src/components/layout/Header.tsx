import { useEffect, useState } from 'react';
import { Zap, LayoutDashboard, BarChart3, Activity } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import { useWSStore } from '../../store/useWSStore';
import { SignalBadge } from '../signals/SignalBadge';
import styles from './Header.module.css';

interface MarketStatus {
    isOpen: boolean;
    isHoliday: boolean;
    holidayName: string;
    nextOpenLabel: string;
}

function isWeekendIST(date: Date = new Date()): boolean {
    const weekday = new Intl.DateTimeFormat('en-US', {
        timeZone: 'Asia/Kolkata',
        weekday: 'short',
    }).format(date);
    return weekday === 'Sat' || weekday === 'Sun';
}

const navClass = ({ isActive }: { isActive: boolean }) =>
    `${styles.navLink}${isActive ? ` ${styles.navLinkActive}` : ''}`;

export function Header() {
    const connected = useWSStore(s => s.connected);
    const reconnectAttempts = useWSStore(s => s.reconnectAttempts);
    const marketOpen = useWSStore(s => s.marketOpen);
    const [status, setStatus] = useState<MarketStatus | null>(null);

    useEffect(() => {
        const apiUrl = import.meta.env.VITE_API_URL || '';
        const load = () => fetch(`${apiUrl}/api/market-status`)
            .then(r => r.json())
            .then((data: MarketStatus) => setStatus(data))
            .catch(() => { });
        load();
        const timer = setInterval(load, 5 * 60 * 1000);
        return () => clearInterval(timer);
    }, []);

    const open = marketOpen || status?.isOpen === true;
    let marketLabel = 'Market closed';
    let marketTone = styles.toneMuted;
    if (open) {
        marketLabel = 'Market open';
        marketTone = styles.toneUp;
    } else if (status?.isHoliday) {
        marketLabel = status.holidayName ? `Holiday: ${status.holidayName}` : 'Market holiday';
        marketTone = styles.toneWarn;
    } else if (isWeekendIST()) {
        marketLabel = 'Weekend';
    }
    const nextOpen = !open && status?.nextOpenLabel ? `Opens ${status.nextOpenLabel}` : '';

    const feedLabel = connected ? 'Live' : reconnectAttempts > 0 ? 'Reconnecting' : 'Connecting';
    const feedTone = connected ? styles.toneUp : reconnectAttempts > 0 ? styles.toneDown : styles.toneMuted;

    return (
        <header className={styles.header}>
            <div className={styles.brand}>
                <Zap size={16} className={styles.brandIcon} aria-hidden />
                <span className={styles.logo}>TradingPulse</span>
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
                <span className={`${styles.pill} ${marketTone}`} title={nextOpen || marketLabel}>
                    <span className={styles.dot} aria-hidden />
                    {marketLabel}
                    {nextOpen && <span className={styles.pillSub}>{nextOpen}</span>}
                </span>
                <span className={`${styles.pill} ${feedTone}`} title="Price feed connection">
                    <span className={`${styles.dot}${connected ? ` ${styles.dotLive}` : ''}`} aria-hidden />
                    {feedLabel}
                </span>
            </div>
        </header>
    );
}
