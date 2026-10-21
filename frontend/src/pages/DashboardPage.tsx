import { useCallback, useEffect, useRef, useState } from 'react';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { TradingChart } from '../components/chart/TradingChart';
import { SRPanel } from '../components/sr/SRPanel';
import { FnoInstrumentsTab } from '../components/signals/FnoInstrumentsTab';
import { SRPeek } from '../components/sr/SRPeek';
import { SRSheet, type SheetSnap } from '../components/sr/SRSheet';
import { useMediaQuery, PHONE_QUERY, PHONE_LANDSCAPE_QUERY } from '../hooks/useMediaQuery';
import { useAppStore } from '../store/useAppStore';
import { loadLayout, saveLayout, LAYOUTS, type ChartLayout } from './dashboardLayout';
import { cn } from '@/lib/utils';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';

const GRID_COLS: Record<number, string> = {
    1: 'grid-cols-[minmax(0,1fr)] lte-640:grid-rows-[minmax(0,1fr)] phone-land:grid-rows-[minmax(0,1fr)]',
    2: 'grid-cols-[minmax(0,1fr)_minmax(0,1fr)]',
    4: 'grid-cols-[minmax(0,1fr)_minmax(0,1fr)] grid-rows-[minmax(0,1fr)_minmax(0,1fr)]',
};

const SHEET_ID = 'sr-sheet';
const SNAP_KEY = 'rangeSheetSnap';
const VIEW_KEY = 'dashboardView';

type DashView = 'charts' | 'fno';

/** The desktop view last chosen in this browser; charts when none. */
function loadView(): DashView {
    try {
        return localStorage.getItem(VIEW_KEY) === 'fno' ? 'fno' : 'charts';
    } catch {
        return 'charts';
    }
}

function saveView(v: DashView) {
    try {
        localStorage.setItem(VIEW_KEY, v);
    } catch {
        // blocked storage: the view just isn't remembered
    }
}

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
    const [view, setView] = useState<DashView>(loadView);

    useEffect(() => saveView(view), [view]);

    useEffect(() => saveLayout(layout), [layout]);

    // Phones show one chart and the strategy as peek strip + sheet. The saved
    // layout is left alone so desktop keeps its 2/4-chart choice.
    const isPhone = useMediaQuery(PHONE_QUERY);
    const isLandscape = useMediaQuery(PHONE_LANDSCAPE_QUERY);
    const compactUI = isPhone || isLandscape;
    const count = compactUI ? 1 : layout.count;
    // Phones keep the chart; F&O instruments stay in Signals there.
    const showFno = !compactUI && view === 'fno';
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
            <div className="relative flex flex-col flex-[1_1_auto] min-w-0 min-h-0">
                <div className="flex items-center justify-end gap-[8px] py-[4px] px-[12px] border-b border-b-sg-line bg-sg-surface shrink-0 lte-640:hidden phone-land:hidden">
                    <Tabs value={view} onValueChange={v => setView(v as DashView)}>
                        <TabsList variant="compact" aria-label="Dashboard view">
                            {(['charts', 'fno'] as const).map(v => (
                                <TabsTrigger key={v} value={v}>
                                    {v === 'charts' ? 'Charts' : 'F&O instruments'}
                                </TabsTrigger>
                            ))}
                        </TabsList>
                    </Tabs>
                    <span className="flex-[1_1_auto]" />
                    {!showFno && (<>
                    <span className="text-[11px] text-sg-muted">Charts</span>
                    <ToggleGroup
                        variant="compact"
                        aria-label="Chart layout"
                        value={[String(layout.count)]}
                        onValueChange={v => v[0] && setLayout(l => ({ ...l, count: Number(v[0]) as ChartLayout['count'] }))}
                    >
                        {LAYOUTS.map(n => (
                            <ToggleGroupItem
                                key={n}
                                value={String(n)}
                                title={n === 1 ? 'One chart' : `${n} charts, each with its own timeframe`}
                            >
                                {n}
                            </ToggleGroupItem>
                        ))}
                    </ToggleGroup>
                    </>)}
                </div>
                {showFno ? (
                    <div className="flex-[1_1_auto] min-h-0 overflow-y-auto py-[12px] px-[16px] bg-sg-bg">
                        <FnoInstrumentsTab />
                    </div>
                ) : (
                    <div className={cn('grid flex-[1_1_auto] min-h-0 gap-[1px] bg-sg-line', GRID_COLS[count])}>
                        <TradingChart onOpenIndicators={onOpenIndicators} onOpenSR={compactUI ? openSR : undefined} />
                        {panes.map((tf, i) => (
                            <TradingChart key={i} compact paneTF={tf} onPaneTFChange={t => setPaneTF(i, t)} />
                        ))}
                    </div>
                )}
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
