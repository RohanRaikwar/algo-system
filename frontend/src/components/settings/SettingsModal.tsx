import { useState, useEffect, useCallback, useMemo, useRef, type ReactNode } from 'react';
import { Plus, Trash2, X } from 'lucide-react';
import { useAppStore } from '../../store/useAppStore';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../../hooks/useMediaQuery';
import { useBackToClose } from '../../hooks/useBackToClose';
import { haptic } from '../../utils/haptic';
import { useCandleStore } from '../../store/useCandleStore';
import { sendSubscribe } from '../../hooks/useWebSocket';
import { tfLabel, getEntryColor, entryKey } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Drawer, DrawerBody, DrawerContent, DrawerDescription, DrawerFooter, DrawerHeader, DrawerTitle } from '@/components/ui/drawer';
import { Empty } from '@/components/ui/empty';
import { FieldError } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select';
import { Switch } from '@/components/ui/switch';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';

interface Props {
    open: boolean;
    onClose: () => void;
}

type IndType = 'SMA' | 'EMA' | 'SMMA';

const TYPES: { id: IndType; label: string }[] = [
    { id: 'EMA', label: 'EMA' },
    { id: 'SMA', label: 'SMA' },
    { id: 'SMMA', label: 'SMMA' },
];

const PRESETS: { type: IndType; period: number }[] = [
    { type: 'EMA', period: 9 },
    { type: 'EMA', period: 21 },
    { type: 'EMA', period: 50 },
    { type: 'SMA', period: 20 },
    { type: 'SMA', period: 50 },
    { type: 'SMA', period: 200 },
    { type: 'SMMA', period: 10 },
];

// Distinct hues for new lines; avoids the candle green and red.
const PALETTE = ['#4fb8d6', '#e5a23a', '#a78bfa', '#f472b6', '#facc15', '#60a5fa', '#fb923c', '#e2e8f0'];

const sectionTitle = 'flex items-center gap-[8px] mb-[10px] text-sg-text-2 text-[0.86rem] font-semibold';
const rowCls = 'flex items-center gap-[12px] pt-[8px] pr-[8px] pb-[8px] pl-[12px] border-b border-b-sg-line-soft last:border-b-0';
const rowName = 'text-sg-text text-[0.9rem] font-medium';

function displayName(name: string): string {
    return name.replace('_', ' ');
}

function nextColor(used: IndicatorEntry[]): string {
    const taken = new Set(used.map(e => getEntryColor(e).toLowerCase()));
    return PALETTE.find(c => !taken.has(c.toLowerCase())) ?? PALETTE[used.length % PALETTE.length];
}

function sameEntries(a: IndicatorEntry[], b: IndicatorEntry[]): boolean {
    if (a.length !== b.length) return false;
    const key = (e: IndicatorEntry) => `${entryKey(e)}:${getEntryColor(e)}`;
    const sa = a.map(key).sort().join('|');
    const sb = b.map(key).sort().join('|');
    return sa === sb;
}

export function SettingsModal({ open, onClose }: Props) {
    const config = useAppStore(s => s.config);
    const selectedTF = useAppStore(s => s.selectedTF);
    const activeIndicators = useAppStore(s => s.activeIndicators);
    const setActiveIndicators = useAppStore(s => s.setActiveIndicators);
    const srOverlay = useAppStore(s => s.overlays.srSituation);
    const setOverlay = useAppStore(s => s.setOverlay);
    const chartTF = selectedTF || 60;

    const [draft, setDraft] = useState<IndicatorEntry[]>([]);
    const [indType, setIndType] = useState<IndType>('EMA');
    const [period, setPeriod] = useState('');
    const [indTF, setIndTF] = useState(chartTF);
    const [error, setError] = useState('');
    const periodRef = useRef<HTMLInputElement>(null);
    // Phone portrait: a bottom sheet (Drawer) with a handle, swipe-down close and no autofocus.
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    const [kb, setKb] = useState<{ bottom: number; maxH: number } | null>(null);

    // Sync draft with the saved list each time the dialog opens. Initial focus
    // (Period on desktop, the sheet itself on a phone) and focus return are
    // handled by the Dialog/Drawer popup.
    useEffect(() => {
        if (!open) return;
        setDraft((activeIndicators || []).map((e) => ({ ...e, color: getEntryColor(e) })));
        setIndTF(chartTF);
        setPeriod('');
        setError('');
    }, [open, chartTF]); // eslint-disable-line react-hooks/exhaustive-deps

    useBackToClose(open && isPhone, onClose);

    // Keep the sheet (and its footer) above the on-screen keyboard.
    useEffect(() => {
        const vv = window.visualViewport;
        if (!open || !isPhone || !vv) return;
        const update = () => {
            const bottom = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
            setKb(bottom > 0 ? { bottom, maxH: vv.height * 0.92 } : null);
        };
        update();
        vv.addEventListener('resize', update);
        vv.addEventListener('scroll', update);
        return () => {
            vv.removeEventListener('resize', update);
            vv.removeEventListener('scroll', update);
            setKb(null);
        };
    }, [open, isPhone]);

    // Clamp selected indicator TF when chart TF changes
    useEffect(() => {
        setIndTF((prev) => (prev <= chartTF ? prev : chartTF));
    }, [chartTF]);

    const allowedTFs = (config.tfs || [60, 120, 180, 300]).filter((t) => t <= chartTF);
    const dirty = useMemo(() => !sameEntries(draft, activeIndicators || []), [draft, activeIndicators]);

    const add = useCallback((type: IndType, p: number, tf: number): boolean => {
        if (!Number.isInteger(p) || p < 2 || p > 500) {
            setError('Period must be a whole number from 2 to 500.');
            return false;
        }
        const name = `${type}_${p}`;
        if (draft.some((e) => e.name === name && e.tf === tf)) {
            setError(`${displayName(name)} on ${tfLabel(tf)} is already on the chart.`);
            return false;
        }
        setDraft((d) => [...d, { name, tf, color: nextColor(d) }]);
        setError('');
        haptic();
        return true;
    }, [draft]);

    const addFromForm = useCallback(() => {
        if (add(indType, Number(period), indTF)) {
            setPeriod('');
            if (!isPhone) periodRef.current?.focus();
        }
    }, [add, indType, period, indTF, isPhone]);

    const removeEntry = useCallback((key: string) => {
        haptic();
        setDraft((d) => d.filter((e) => entryKey(e) !== key));
        setError('');
    }, []);

    const recolor = useCallback((key: string, color: string) => {
        setDraft((d) => d.map((e) => (entryKey(e) === key ? { ...e, color } : e)));
    }, []);

    const handleApply = useCallback(() => {
        const entries = draft.map((e) => ({ ...e }));

        // Clear removed indicator data from candle store
        const keepNames = entries.map(e => e.name);
        const allTFs = new Set(entries.map(e => e.tf));
        for (const tf of allTFs) {
            useCandleStore.getState().clearIndicatorsForTF(tf, keepNames);
        }

        // Persist in tab-local store only
        setActiveIndicators(entries);

        // Re-subscribe with updated indicator profile via WS for this tab connection
        const token = useAppStore.getState().selectedToken;
        const tf = useAppStore.getState().selectedTF;
        if (token) {
            sendSubscribe(token, tf, entries);
        }

        haptic();
        onClose();
    }, [draft, setActiveIndicators, onClose]);

    // Ctrl/Cmd+Enter applies (only while open); Esc is handled by Dialog/Drawer.
    useEffect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                handleApply();
            }
        };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [open, handleApply]);

    const sorted = [...draft].sort((a, b) => {
        const cmp = a.name.localeCompare(b.name, undefined, { numeric: true });
        return cmp !== 0 ? cmp : a.tf - b.tf;
    });

    const presets = PRESETS.filter(p => !draft.some(e => e.name === `${p.type}_${p.period}` && e.tf === indTF));

    const listSection = (
        <section>
            <h3 className={sectionTitle}>
                On chart <Badge variant="count">{draft.length}</Badge>
            </h3>
            {sorted.length === 0 ? (
                <Empty variant="note" render={<p />}>No indicators yet. Add one below or pick a quick add.</Empty>
            ) : (
                <ul className="list-none border border-solid border-sg-line rounded-[12px] bg-sg-bg overflow-hidden">
                    {sorted.map((entry) => {
                        const key = entryKey(entry);
                        const color = getEntryColor(entry);
                        return (
                            <li key={key} className={rowCls}>
                                <label className="relative w-[18px] h-[18px] rounded-[6px] shadow-[inset_0_0_0_1px_rgba(255,255,255,0.15)] cursor-pointer shrink-0 focus-within:shadow-ring" style={{ background: color }} title="Change line colour">
                                    <input
                                        type="color"
                                        className="absolute inset-0 w-full h-full opacity-0 cursor-pointer"
                                        value={color}
                                        onChange={(e) => recolor(key, e.target.value)}
                                        aria-label={`Colour for ${displayName(entry.name)}`}
                                    />
                                </label>
                                <span className={rowName}>{displayName(entry.name)}</span>
                                <Badge variant="meta">{tfLabel(entry.tf)}</Badge>
                                {entry.tf > chartTF && (
                                    <span className="text-sg-warn text-[0.76rem]" title="Hidden until you switch to this timeframe or higher">
                                        Hidden on {tfLabel(chartTF)}
                                    </span>
                                )}
                                <Button
                                    variant="ghost-danger"
                                    size="icon-30"
                                    className="ml-auto"
                                    onClick={() => removeEntry(key)}
                                    aria-label={`Remove ${displayName(entry.name)} ${tfLabel(entry.tf)}`}
                                >
                                    <Trash2 size={15} />
                                </Button>
                            </li>
                        );
                    })}
                </ul>
            )}
        </section>
    );

    const overlaysSection = (
        <section>
            <h3 className={sectionTitle}>Overlays</h3>
            <label className={cn(rowCls, 'portrait-640:flex-row-reverse portrait-640:justify-between portrait-640:py-[10px] portrait-640:px-0 portrait-640:cursor-pointer')}>
                {isPhone ? (
                    <Switch checked={srOverlay} onCheckedChange={(on) => setOverlay('srSituation', on)} />
                ) : (
                    <Checkbox checked={srOverlay} onCheckedChange={(on) => setOverlay('srSituation', on)} />
                )}
                <span className={rowName}>SR situation: supports/resistances, day lines, sideways box</span>
            </label>
        </section>
    );

    const presetsBlock = presets.length > 0 && (
        <div className="flex items-center flex-wrap gap-[6px] mt-[12px] portrait-640:mt-0">
            <span className="mr-[4px] text-sg-faint text-[0.8rem]">Quick add on {tfLabel(indTF)}</span>
            {presets.map(p => (
                <Button
                    key={`${p.type}_${p.period}`}
                    variant="chip"
                    onClick={() => add(p.type, p.period, indTF)}
                >
                    {p.type} {p.period}
                </Button>
            ))}
        </div>
    );

    const addSection = (
        <section>
            <h3 className={sectionTitle}>Add indicator</h3>
            <form
                className="flex gap-[8px] flex-wrap"
                onSubmit={(e) => { e.preventDefault(); addFromForm(); }}
            >
                <ToggleGroup
                    variant="segment"
                    size="md"
                    className="lte-640:w-full"
                    aria-label="Type"
                    value={[indType]}
                    onValueChange={(v) => v[0] && setIndType(v[0] as IndType)}
                >
                    {TYPES.map(t => (
                        <ToggleGroupItem key={t.id} value={t.id} type="button">
                            {t.label}
                        </ToggleGroupItem>
                    ))}
                </ToggleGroup>
                <Input
                    ref={periodRef}
                    variant="form"
                    className="flex-[1_1_120px] min-w-0 tabular-nums"
                    type="number"
                    inputMode="numeric"
                    min={2}
                    max={500}
                    placeholder="Period, e.g. 21"
                    aria-label="Period"
                    value={period}
                    onChange={(e) => { setPeriod(e.target.value); setError(''); }}
                />
                <NativeSelect
                    variant="form"
                    value={indTF}
                    onChange={(e) => setIndTF(Number(e.target.value))}
                    aria-label="Compute timeframe"
                    title="Timeframe the indicator is computed on"
                >
                    {allowedTFs.map((t) => (
                        <NativeSelectOption key={t} value={t}>{tfLabel(t)}</NativeSelectOption>
                    ))}
                </NativeSelect>
                <Button type="submit" size="lg" disabled={!period}>
                    <Plus size={15} /> Add
                </Button>
            </form>
            {error && <FieldError className="mt-[8px] text-sg-loss text-[0.82rem]">{error}</FieldError>}
            {/* Phone shows quick adds above the form instead. */}
            {!isPhone && presetsBlock}
        </section>
    );

    // Every close (X, Cancel, Apply, Esc, backdrop, swipe, back) ends in onClose,
    // so useBackToClose sees open go false and drops its history entry.
    const onOpenChange = (next: boolean) => { if (!next) onClose(); };

    const subtitle = <>Lines drawn on the {tfLabel(chartTF)} chart</>;
    const closeButton = (
        <Button variant="ghost" size="icon-32" onClick={onClose} aria-label="Close">
            <X size={18} />
        </Button>
    );
    const footerButtons: ReactNode = (
        <>
            <Button
                variant="danger-text"
                onClick={() => { setDraft([]); setError(''); }}
                disabled={draft.length === 0}
            >
                Remove all
            </Button>
            <span className="ml-auto text-sg-faint text-[0.76rem] whitespace-nowrap lte-640:hidden">
                <Kbd>Ctrl</Kbd> <Kbd>Enter</Kbd> to apply, <Kbd>Esc</Kbd> to close
            </span>
            <Button variant="secondary" size="lg" className="lte-640:ml-auto portrait-640:min-h-[44px]" onClick={onClose}>Cancel</Button>
            <Button size="lg" className="portrait-640:min-h-[44px]" onClick={handleApply} disabled={!dirty}>
                Apply
            </Button>
        </>
    );

    if (isPhone) {
        return (
            <Drawer open={open} onOpenChange={onOpenChange} swipeDirection="down">
                <DrawerContent
                    id="ind-sheet"
                    aria-labelledby="ind-title"
                    // No autofocus on a phone: the keyboard would cover half the sheet.
                    initialFocus={() => document.getElementById('ind-sheet')}
                    className="[&_button:focus-visible]:outline-none [&_button:focus-visible]:shadow-ring [&_button:active]:[filter:brightness(1.15)]"
                    style={kb ? { marginBottom: kb.bottom, maxHeight: kb.maxH } : undefined}
                >
                    <DrawerHeader className="items-start justify-between gap-[12px] pt-[6px] pr-[16px] pb-[12px] pl-[20px] touch-none select-none">
                        <div>
                            <DrawerTitle id="ind-title" className="text-[1.1rem]">Indicators</DrawerTitle>
                            <DrawerDescription className="mt-[2px]">{subtitle}</DrawerDescription>
                        </div>
                        {closeButton}
                    </DrawerHeader>
                    <DrawerBody className="flex flex-col gap-[18px]">
                        {listSection}
                        {presetsBlock && <section>{presetsBlock}</section>}
                        {addSection}
                        {overlaysSection}
                    </DrawerBody>
                    <DrawerFooter className="items-center">
                        {footerButtons}
                    </DrawerFooter>
                </DrawerContent>
            </Drawer>
        );
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent aria-labelledby="ind-title" initialFocus={periodRef}>
                <DialogHeader>
                    <div>
                        <DialogTitle id="ind-title">Indicators</DialogTitle>
                        <DialogDescription>{subtitle}</DialogDescription>
                    </div>
                    {closeButton}
                </DialogHeader>
                <DialogBody>
                    {listSection}
                    {overlaysSection}
                    {addSection}
                </DialogBody>
                <DialogFooter>
                    {footerButtons}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
