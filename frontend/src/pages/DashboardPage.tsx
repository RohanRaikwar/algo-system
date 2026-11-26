import { useCallback, useEffect, useRef, useState } from 'react';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { TradingChart } from '../components/chart/TradingChart';
import { SRPanel } from '../components/sr/SRPanel';
import { FnoInstrumentsTab } from '../components/signals/FnoInstrumentsTab';
import { SRSheet, type SheetSnap } from '../components/sr/SRSheet';
import { useMediaQuery, PHONE_QUERY, PHONE_LANDSCAPE_QUERY } from '../hooks/useMediaQuery';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Button } from '@/components/ui/button';
import { Drawer, DrawerContent, DrawerHeader, DrawerTitle } from '@/components/ui/drawer';
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { tokenLabel } from '../utils/helpers';
import { BottomSheet } from '../components/layout/BottomSheet';
import { X } from 'lucide-react';
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from '@/components/ui/resizable';
import { useDefaultLayout, type LayoutStorage } from 'react-resizable-panels';
import { OptionChainPanel, type Picked } from '../components/optionchain/OptionChainPanel';
import { OptionChart } from '../components/optionchain/OptionChart';
import { useAppStore } from '../store/useAppStore';

const SHEET_ID = 'sr-sheet';
const SNAP_KEY = 'rangeSheetSnap';
const SIDE_TAB_KEY = 'dashboardSideTab';

type SideTab = 'sr' | 'fno';

/** localStorage that never throws (private mode, blocked site data): the split just isn't remembered. */
const splitStorage: LayoutStorage = {
    getItem(key) {
        try {
            const v = localStorage.getItem(key);
            if (v) JSON.parse(v);
            return v;
        } catch {
            return null;
        }
    },
    setItem(key, value) {
        try {
            localStorage.setItem(key, value);
        } catch {
            // blocked storage
        }
    },
};

/** The desktop side-sheet tab last chosen in this browser; S/R when none. */
function loadSideTab(): SideTab {
    try {
        return localStorage.getItem(SIDE_TAB_KEY) === 'fno' ? 'fno' : 'sr';
    } catch {
        return 'sr';
    }
}

function saveSideTab(t: SideTab) {
    try {
        localStorage.setItem(SIDE_TAB_KEY, t);
    } catch {
        // blocked storage: the tab just isn't remembered
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
    // Desktop: the chart fills the page; S/R strategy and F&O instruments
    // open in a sheet on the right.
    const [sideOpen, setSideOpen] = useState(false);
    const [sideTab, setSideTab] = useState<SideTab>(loadSideTab);
    const openSide = useCallback(() => setSideOpen(true), []);
    // Option chain opens in a modal; a picked contract's chart then takes
    // the right half of the screen beside the index chart.
    const [chainOpen, setChainOpen] = useState(false);
    const openChain = useCallback(() => setChainOpen(true), []);
    const [optionPick, setOptionPick] = useState<Picked | null>(null);
    // The split starts at 50/50; a dragged width is remembered in this browser.
    // Side by side on desktop, stacked (index on top) on phones; each remembers its own split.
    const splitWide = useDefaultLayout({ id: 'dashboard-option-split', onlySaveAfterUserInteractions: true, storage: splitStorage });
    const splitTall = useDefaultLayout({ id: 'dashboard-option-split-phone', onlySaveAfterUserInteractions: true, storage: splitStorage });
    const pickOption = useCallback((p: Picked) => { setOptionPick(p); setChainOpen(false); }, []);
    const selectedToken = useAppStore(s => s.selectedToken);
    // The picked contract belongs to the index it came from.
    useEffect(() => { setOptionPick(null); }, [selectedToken]);

    useEffect(() => saveSideTab(sideTab), [sideTab]);

    // Phones show the strategy as peek strip + sheet.
    const isPhone = useMediaQuery(PHONE_QUERY);
    const isLandscape = useMediaQuery(PHONE_LANDSCAPE_QUERY);
    const compactUI = isPhone || isLandscape;
    const [snap, setSnap] = useState<SheetSnap>('closed');
    // Focus goes back to the button that opened the sheet.
    const openerRef = useRef<HTMLElement | null>(null);
    const onSnap = useCallback((s: SheetSnap) => {
        if (s !== 'closed') rememberSnap(s);
        setSnap(s);
    }, []);
    const openSR = useCallback(() => {
        openerRef.current = document.activeElement as HTMLElement | null;
        onSnap(lastOpenSnap());
    }, [onSnap]);
    useEffect(() => { if (!compactUI) setSnap('closed'); }, [compactUI]);

    const indexChart = (
        <TradingChart
            onOpenIndicators={onOpenIndicators}
            onOpenSR={compactUI ? openSR : undefined}
            onOpenStrategy={compactUI ? openSR : openSide}
            onOpenOptionChain={openChain}
        />
    );

    return (
        <ErrorBoundary>
            <div className="relative flex flex-col flex-[1_1_auto] min-w-0 min-h-0">
                <div className="grid flex-[1_1_auto] min-h-0 grid-cols-[minmax(0,1fr)] lte-640:grid-rows-[minmax(0,1fr)] phone-land:grid-rows-[minmax(0,1fr)]">
                    {optionPick ? (
                        <ResizablePanelGroup
                            key={isPhone ? 'tall' : 'wide'}
                            orientation={isPhone ? 'vertical' : 'horizontal'}
                            className="min-h-0"
                            defaultLayout={(isPhone ? splitTall : splitWide).defaultLayout}
                            onLayoutChanged={(isPhone ? splitTall : splitWide).onLayoutChanged}
                        >
                            <ResizablePanel id="index" defaultSize="50%" minSize="25%" className="flex min-w-0 min-h-0">
                                {indexChart}
                            </ResizablePanel>
                            <ResizableHandle withHandle />
                            <ResizablePanel id="option" defaultSize="50%" minSize="25%" className="min-w-0 min-h-0">
                                <OptionChart key={optionPick.token} {...optionPick} fill onClose={() => setOptionPick(null)} />
                            </ResizablePanel>
                        </ResizablePanelGroup>
                    ) : indexChart}
                </div>
                {compactUI ? (
                    <SRSheet id={SHEET_ID} snap={snap} onSnap={onSnap} returnFocusRef={openerRef} />
                ) : (
                    <Drawer open={sideOpen} onOpenChange={setSideOpen} swipeDirection="right" showSwipeHandle={false}>
                        <DrawerContent aria-label="Strategy and instruments" className="[--drawer-content-width:min(40rem,92vw)]! sm:[--drawer-content-width:min(40rem,92vw)]!">
                            <div className="flex flex-col min-h-0 flex-1">
                                <DrawerHeader className="justify-between pt-[12px] pr-[8px] pl-[16px]">
                                    <DrawerTitle className="sr-only">{sideTab === 'sr' ? 'S/R strategy' : 'F&O instruments'}</DrawerTitle>
                                    <Tabs value={sideTab} onValueChange={v => setSideTab(v as SideTab)}>
                                        <TabsList variant="compact" aria-label="Sheet view">
                                            <TabsTrigger value="sr">S/R strategy</TabsTrigger>
                                            <TabsTrigger value="fno">F&amp;O instruments</TabsTrigger>
                                        </TabsList>
                                    </Tabs>
                                    <Button variant="ghost" size="icon-32" onClick={() => setSideOpen(false)} aria-label="Close">
                                        <X size={18} />
                                    </Button>
                                </DrawerHeader>
                                <div className="flex-1 min-h-0 overflow-y-auto">
                                    {sideTab === 'sr' ? (
                                        <SRPanel variant="sheet" />
                                    ) : (
                                        <div className="py-[12px] px-[16px]">
                                            <FnoInstrumentsTab />
                                        </div>
                                    )}
                                </div>
                            </div>
                        </DrawerContent>
                    </Drawer>
                )}
                {isPhone ? (
                    <BottomSheet open={chainOpen} onClose={() => setChainOpen(false)} title={`${tokenLabel(selectedToken)} option chain`}>
                        <OptionChainPanel variant="panel" onPick={pickOption} />
                    </BottomSheet>
                ) : (
                    <Dialog open={chainOpen} onOpenChange={setChainOpen}>
                        <DialogContent showCloseButton className="sm:max-w-[760px] w-[min(96vw,760px)]">
                            <DialogHeader>
                                <div>
                                    <DialogTitle>{tokenLabel(selectedToken)} option chain</DialogTitle>
                                    <DialogDescription>Click a CE or PE price to open its chart beside the index chart.</DialogDescription>
                                </div>
                            </DialogHeader>
                            <DialogBody>
                                <OptionChainPanel variant="panel" onPick={pickOption} />
                            </DialogBody>
                        </DialogContent>
                    </Dialog>
                )}
            </div>
        </ErrorBoundary>
    );
}
