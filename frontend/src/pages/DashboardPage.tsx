import { ErrorBoundary } from '../components/ErrorBoundary';
import { TradingChart } from '../components/chart/TradingChart';
import styles from './DashboardPage.module.css';

interface DashboardPageProps {
    onOpenIndicators?: () => void;
}

export function DashboardPage({ onOpenIndicators }: DashboardPageProps) {
    return (
        <ErrorBoundary>
            <div className={styles.page}>
                <TradingChart onOpenIndicators={onOpenIndicators} />
            </div>
        </ErrorBoundary>
    );
}
