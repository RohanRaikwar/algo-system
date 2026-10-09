import { useCallback, useEffect, useRef, useState } from 'react';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { TradingChart } from '../components/chart/TradingChart';
import { SRPanel } from '../components/sr/SRPanel';
import { SRPeek } from '../components/sr/SRPeek';
import { SRSheet, type SheetSnap } from '../components/sr/SRSheet';
import { useMediaQuery, PHONE_QUERY, PHONE_LANDSCAPE_QUERY } from '../hooks/useMediaQuery';
import { useAppStore } from '../store/useAppStore';
import { loadLayout, saveLayout, LAYOUTS, type ChartLayout } from './dashboardLayout';
import styles from './DashboardPage.module.css';

const SHEET_ID = 'sr-sheet';
const SNAP_KEY = 'rangeSheetSnap';

/** Last open snap point this session; half when none or storage is blocked. */
function lastOpenSnap(): SheetSnap {
    try {
        return sessionStorage.getItem(SNAP_KEY) === 'full' ? 'full' : 'half';
    } catch {
        return 'half';
    }
}

function rememberSnap(s: SheetSnap) {
    try {
        sessionStorage.setItem(SNAP_KEY, s);
    } catch {
        // blocked storage: next open just uses half
    }
}

interface DashboardPageProps {
    onOpenIndicators?: () => void;
}

export function DashboardPage({ onOpenIndicators }: DashboardPageProps) {
    const tfs = useAppStore(s => s.config.tfs);
    const [layout, setLayout] = useState<ChartLayout>(() => loadLayout(tfs));

    useEffect(() => saveLayout(layout), [layout]);

    // Phones show one chart and the strategy as peek strip + sheet. The saved
    // layout is left alone so desktop keeps its 2/4-chart choice.
    const isPhone = useMediaQuery(PHONE_QUERY);
    const isLandscape = useMediaQuery(PHONE_LANDSCAPE_QUERY);
    const compactUI = isPhone || isLandscape;
    const count = compactUI ? 1 : layout.count;
    const [snap, setSnap] = useState<SheetSnap>('closed');
    const peekRef = useRef<HTMLButtonElement>(null);
    const openSheet = useCallback(() => setSnap(s => (s === 'closed' ? lastOpenSnap() : 'closed')), []);
    const onSnap = useCallback((s: SheetSnap) => {
        if (s !== 'closed') rememberSnap(s);
        setSnap(s);
    }, []);
    const openSR = useCallback(() => onSnap(lastOpenSnap()), [onSnap]);
    useEffect(() => { if (!compactUI) setSnap('closed'); }, [compactUI]);

    const panes = layout.panes.slice(0, count - 1);
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
                <div className={`${styles.grid} ${styles[`grid${count}`]}`}>
                    <TradingChart onOpenIndicators={onOpenIndicators} onOpenSR={compactUI ? openSR : undefined} />
                    {panes.map((tf, i) => (
                        <TradingChart key={i} compact paneTF={tf} onPaneTFChange={t => setPaneTF(i, t)} />
                    ))}
                </div>
                {compactUI ? (
                    <>
                        <SRPeek ref={peekRef} open={snap !== 'closed'} sheetId={SHEET_ID} onOpen={openSheet} />
                        <SRSheet id={SHEET_ID} snap={snap} onSnap={onSnap} returnFocusRef={peekRef} />
                    </>
                ) : (
                    <SRPanel />
                )}
            </div>
        </ErrorBoundary>
    );
}
