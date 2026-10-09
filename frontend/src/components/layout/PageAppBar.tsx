import { PushToggle } from './PushToggle';
import { StatusDot } from './StatusDot';
import styles from './PageAppBar.module.css';

/**
 * Phone-portrait page bar for Signals and Health. It replaces the brand
 * Header there; the dashboard uses the chart's own top bar instead.
 */
export function PageAppBar({ title }: { title: string }) {
    return (
        <header className={styles.bar}>
            <StatusDot />
            <h1 className={styles.title}>{title}</h1>
            <div className={styles.actions}>
                <PushToggle />
            </div>
        </header>
    );
}
