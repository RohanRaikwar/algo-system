import { useEffect, useState } from 'react';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { TradingChart } from '../components/chart/TradingChart';
import { RangePanel } from '../components/range/RangePanel';
import { useAppStore } from '../store/useAppStore';
import { loadLayout, saveLayout, LAYOUTS, type ChartLayout } from './dashboardLayout';
import styles from './DashboardPage.module.css';

interface DashboardPageProps {
    onOpenIndicators?: () => void;
}

export function DashboardPage({ onOpenIndicators }: DashboardPageProps) {
    const tfs = useAppStore(s => s.config.tfs);
    const [layout, setLayout] = useState<ChartLayout>(() => loadLayout(tfs));

    useEffect(() => saveLayout(layout), [layout]);

    const panes = layout.panes.slice(0, layout.count - 1);
    const setPaneTF = (i: number, tf: number) =>
        setLayout(l => ({ ...l, panes: l.panes.map((p, j) => (j === i ? tf : p)) }));

    return (
        <ErrorBoundary>
            <div className={styles.page}>
                <div className={styles.layoutBar}>
                    <span className={styles.layoutLabel}>Charts</span>
                    <div className={styles.layoutGroup} role="radiogroup" aria-label="Chart layout">
                        {LAYOUTS.map(n => (
                            <button
                                key={n}
                                role="radio"
                                aria-checked={layout.count === n}
                                className={`${styles.layoutBtn}${layout.count === n ? ` ${styles.layoutActive}` : ''}`}
                                onClick={() => setLayout(l => ({ ...l, count: n }))}
                                title={n === 1 ? 'One chart' : `${n} charts, each with its own timeframe`}
                            >
                                {n}
                            </button>
                        ))}
                    </div>
                </div>
                <div className={`${styles.grid} ${styles[`grid${layout.count}`]}`}>
                    <TradingChart onOpenIndicators={onOpenIndicators} />
                    {panes.map((tf, i) => (
                        <TradingChart key={i} compact paneTF={tf} onPaneTFChange={t => setPaneTF(i, t)} />
                    ))}
                </div>
                <RangePanel />
            </div>
        </ErrorBoundary>
    );
}
