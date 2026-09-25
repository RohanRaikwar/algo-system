import { LayoutDashboard, BarChart3, Activity } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import { SignalBadge } from '../signals/SignalBadge';
import styles from './MobileNav.module.css';

const linkClass = ({ isActive }: { isActive: boolean }) =>
    `${styles.link}${isActive ? ` ${styles.active}` : ''}`;

/** Bottom tab bar for phones; hidden above 640px where the header nav shows. */
export function MobileNav() {
    return (
        <nav className={styles.bar} aria-label="Primary">
            <NavLink to="/" end className={linkClass}>
                <LayoutDashboard size={20} />
                <span>Chart</span>
            </NavLink>
            <NavLink to="/signals" className={linkClass}>
                <span className={styles.iconWrap}>
                    <BarChart3 size={20} />
                    <SignalBadge />
                </span>
                <span>Signals</span>
            </NavLink>
            <NavLink to="/health" className={linkClass}>
                <Activity size={20} />
                <span>Health</span>
            </NavLink>
        </nav>
    );
}
