import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Plus, Trash2, X } from 'lucide-react';
import { useAppStore } from '../../store/useAppStore';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../../hooks/useMediaQuery';
import { FLICK_SPEED, useSheetDrag } from '../../hooks/useSheetDrag';
import { useBackToClose } from '../../hooks/useBackToClose';
import { haptic } from '../../utils/haptic';
import { useCandleStore } from '../../store/useCandleStore';
import { sendSubscribe } from '../../hooks/useWebSocket';
import { tfLabel, getEntryColor, entryKey } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import { cn } from '@/lib/utils';

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
const inputCls = 'h-[38px] py-0 px-[10px] bg-sg-bg border border-solid border-sg-line rounded-ctrl text-sg-text [font-family:inherit] text-[0.88rem] placeholder:text-sg-faint hover:border-sg-line-hover focus-visible:outline-none focus-visible:shadow-ring';
const primaryBtn = 'inline-flex items-center justify-center gap-[6px] h-[38px] py-0 px-[14px] border-none rounded-ctrl bg-sg-accent text-white [font-family:inherit] text-[0.88rem] font-semibold cursor-pointer hover:not-disabled:bg-sg-accent-hover disabled:bg-sg-raised disabled:text-sg-faint disabled:cursor-default';
const kbd = 'inline-block min-w-[20px] py-0 px-[5px] border border-solid border-sg-line border-b-[2px] rounded-[5px] bg-sg-raised text-sg-text-2 [font-family:inherit] text-[0.72rem] leading-[1.5] text-center';

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
    const returnFocus = useRef<HTMLElement | null>(null);
    const modalRef = useRef<HTMLDivElement>(null);
    // Phone portrait: a bottom sheet with a handle, swipe-down close and no autofocus.
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    const [kb, setKb] = useState<{ bottom: number; maxH: number } | null>(null);

    // Sync draft with the saved list each time the dialog opens
    useEffect(() => {
        if (!open) return;
        returnFocus.current = document.activeElement as HTMLElement | null;
        setDraft((activeIndicators || []).map((e) => ({ ...e, color: getEntryColor(e) })));
        setIndTF(chartTF);
        setPeriod('');
        setError('');
        // On a phone the keyboard would cover half the sheet; open it only on tap.
        if (isPhone) {
            modalRef.current?.focus();
            return;
        }
        const t = setTimeout(() => periodRef.current?.focus(), 30);
        return () => clearTimeout(t);
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

    const { dragH, startH, handlers } = useSheetDrag(modalRef, () => window.innerHeight, ({ height, startHeight, velocity, moved }) => {
        if (moved && (velocity <= -FLICK_SPEED || height < startHeight * 0.6)) onClose();
    });
    const dragOffset = dragH !== null && startH !== null ? Math.max(0, startH - dragH) : 0;

    // Give focus back to whatever opened the dialog
    useEffect(() => {
        if (!open && returnFocus.current) {
            returnFocus.current.focus?.();
            returnFocus.current = null;
        }
    }, [open]);

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

    // Esc closes, Ctrl/Cmd+Enter applies (only while open)
    useEffect(() => {
        if (!open) return;
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                onClose();
            } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                handleApply();
            }
        };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [open, onClose, handleApply]);

    const sorted = [...draft].sort((a, b) => {
        const cmp = a.name.localeCompare(b.name, undefined, { numeric: true });
        return cmp !== 0 ? cmp : a.tf - b.tf;
    });

    const presets = PRESETS.filter(p => !draft.some(e => e.name === `${p.type}_${p.period}` && e.tf === indTF));

    const listSection = (
        <section>
            <h3 className={sectionTitle}>
                On chart <span className="min-w-[20px] py-0 px-[6px] rounded-pill bg-sg-raised text-sg-muted text-[0.74rem] font-medium text-center tabular-nums">{draft.length}</span>
            </h3>
            {sorted.length === 0 ? (
                <p className="p-[14px] border border-dashed border-sg-line rounded-[10px] text-sg-muted text-[0.86rem] text-center">No indicators yet. Add one below or pick a quick add.</p>
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
                                <span className="py-[1px] px-[8px] rounded-pill bg-sg-raised text-sg-muted text-[0.76rem] tabular-nums">{tfLabel(entry.tf)}</span>
                                {entry.tf > chartTF && (
                                    <span className="text-sg-warn text-[0.76rem]" title="Hidden until you switch to this timeframe or higher">
                                        Hidden on {tfLabel(chartTF)}
                                    </span>
                                )}
                                <button
                                    className="flex items-center justify-center w-[30px] h-[30px] ml-auto border-none rounded-ctrl bg-transparent text-sg-faint cursor-pointer hover:bg-sg-loss-soft hover:text-sg-loss"
                                    onClick={() => removeEntry(key)}
                                    aria-label={`Remove ${displayName(entry.name)} ${tfLabel(entry.tf)}`}
                                >
                                    <Trash2 size={15} />
                                </button>
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
                <input
                    type="checkbox"
                    className="portrait-640:appearance-none portrait-640:relative portrait-640:shrink-0 portrait-640:w-[44px] portrait-640:h-[26px] portrait-640:rounded-[13px] portrait-640:bg-sg-line portrait-640:motion-safe:[transition:background_0.15s] portrait-640:cursor-pointer portrait-640:after:content-[''] portrait-640:after:absolute portrait-640:after:top-[3px] portrait-640:after:left-[3px] portrait-640:after:w-[20px] portrait-640:after:h-[20px] portrait-640:after:rounded-[50%] portrait-640:after:bg-white portrait-640:motion-safe:after:[transition:transform_0.15s] portrait-640:checked:bg-sg-accent portrait-640:checked:after:[transform:translateX(18px)] portrait-640:focus-visible:outline-none portrait-640:focus-visible:shadow-ring"
                    checked={srOverlay}
                    onChange={(e) => setOverlay('srSituation', e.target.checked)}
                />
                <span className={rowName}>SR situation: supports/resistances, day lines, sideways box</span>
            </label>
        </section>
    );

    const presetsBlock = presets.length > 0 && (
        <div className="flex items-center flex-wrap gap-[6px] mt-[12px] portrait-640:mt-0">
            <span className="mr-[4px] text-sg-faint text-[0.8rem]">Quick add on {tfLabel(indTF)}</span>
            {presets.map(p => (
                <button
                    key={`${p.type}_${p.period}`}
                    type="button"
                    className="h-[28px] py-0 px-[11px] border border-solid border-sg-line rounded-pill bg-transparent text-sg-text-2 [font-family:inherit] text-[0.8rem] tabular-nums cursor-pointer hover:border-sg-accent hover:text-sg-text hover:bg-sg-accent-soft portrait-640:min-h-[36px] portrait-640:px-[14px]"
                    onClick={() => add(p.type, p.period, indTF)}
                >
                    {p.type} {p.period}
                </button>
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
                <div className="inline-flex p-[3px] gap-[2px] bg-sg-bg border border-solid border-sg-line rounded-[10px] lte-640:w-full" role="radiogroup" aria-label="Type">
                    {TYPES.map(t => (
                        <button
                            key={t.id}
                            type="button"
                            role="radio"
                            aria-checked={indType === t.id}
                            className={cn(
                                'h-[30px] py-0 px-[12px] border-none rounded-[7px] [font-family:inherit] text-[0.84rem] font-medium cursor-pointer hover:text-sg-text lte-640:flex-1',
                                indType === t.id
                                    ? 'bg-sg-hover text-sg-text shadow-[inset_0_0_0_1px_var(--sg-line)]'
                                    : 'bg-transparent text-sg-muted',
                            )}
                            onClick={() => setIndType(t.id)}
                        >
                            {t.label}
                        </button>
                    ))}
                </div>
                <input
                    ref={periodRef}
                    className={cn(inputCls, 'flex-[1_1_120px] min-w-0 tabular-nums')}
                    type="number"
                    inputMode="numeric"
                    min={2}
                    max={500}
                    placeholder="Period, e.g. 21"
                    aria-label="Period"
                    value={period}
                    onChange={(e) => { setPeriod(e.target.value); setError(''); }}
                />
                <select
                    className={inputCls}
                    value={indTF}
                    onChange={(e) => setIndTF(Number(e.target.value))}
                    aria-label="Compute timeframe"
                    title="Timeframe the indicator is computed on"
                >
                    {allowedTFs.map((t) => (
                        <option key={t} value={t}>{tfLabel(t)}</option>
                    ))}
                </select>
                <button type="submit" className={primaryBtn} disabled={!period}>
                    <Plus size={15} /> Add
                </button>
            </form>
            {error && <p className="mt-[8px] text-sg-loss text-[0.82rem]" role="alert">{error}</p>}
            {/* Phone shows quick adds above the form instead. */}
            {!isPhone && presetsBlock}
        </section>
    );

    return (
        <div
            className={cn(
                'fixed inset-0 z-[1000] flex items-center justify-center p-[16px] bg-[rgba(8,11,16,0.6)] lte-640:items-end lte-640:p-0',
                open
                    ? 'opacity-100 visible motion-safe:[transition:opacity_0.18s_ease]'
                    : 'opacity-0 invisible motion-safe:[transition:opacity_0.18s_ease,visibility_0s_linear_0.18s]',
            )}
            onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
        >
            <div
                ref={modalRef}
                className={cn(
                    'flex flex-col w-[540px] max-w-full max-h-[min(86vh,720px)] bg-sg-surface border border-solid border-sg-line rounded-[16px] shadow-[0_24px_64px_rgba(0,0,0,0.5)] motion-safe:[transition:transform_0.18s_ease]',
                    'lte-640:w-full lte-640:max-h-[90dvh] lte-640:rounded-[16px_16px_0_0]',
                    'portrait-640:relative portrait-640:border-b-0 portrait-640:outline-none',
                    '[&_button:focus-visible]:outline-none [&_button:focus-visible]:shadow-ring portrait-640:[&_button:active]:[filter:brightness(1.15)]',
                    open ? '[transform:translateY(0)]' : '[transform:translateY(8px)] portrait-640:[transform:translateY(100%)]',
                    dragH !== null ? 'portrait-640:[transition:none]' : 'portrait-640:motion-safe:[transition:transform_0.24s_cubic-bezier(0.2,0.8,0.2,1)]',
                )}
                style={{
                    ...(dragOffset ? { transform: `translateY(${dragOffset}px)` } : {}),
                    ...(kb ? { marginBottom: kb.bottom, maxHeight: kb.maxH } : {}),
                }}
                role="dialog"
                aria-modal="true"
                aria-labelledby="ind-title"
                tabIndex={-1}
            >
                {isPhone && <span className="hidden portrait-640:flex portrait-640:justify-center portrait-640:items-center portrait-640:h-[20px] portrait-640:shrink-0 portrait-640:touch-none portrait-640:cursor-grab portrait-640:before:content-[''] portrait-640:before:w-[36px] portrait-640:before:h-[4px] portrait-640:before:rounded-[2px] portrait-640:before:bg-sg-line" aria-hidden {...handlers} />}
                <div className="flex items-start justify-between gap-[12px] pt-[18px] px-[20px] pb-[14px] border-b border-b-sg-line-soft portrait-640:pt-[2px] portrait-640:pr-[16px] portrait-640:pb-[12px] portrait-640:pl-[20px] portrait-640:touch-none portrait-640:select-none" {...(isPhone ? handlers : {})}>
                    <div>
                        <h2 id="ind-title" className="text-sg-text text-[1.1rem] font-semibold">Indicators</h2>
                        <p className="mt-[2px] text-sg-muted text-[0.84rem]">Lines drawn on the {tfLabel(chartTF)} chart</p>
                    </div>
                    <button className="flex items-center justify-center w-[32px] h-[32px] border-none rounded-ctrl bg-transparent text-sg-muted cursor-pointer hover:bg-sg-raised hover:text-sg-text" onClick={onClose} aria-label="Close">
                        <X size={18} />
                    </button>
                </div>

                <div className="flex flex-col gap-[22px] pt-[18px] px-[20px] pb-[20px] overflow-y-auto portrait-640:gap-[18px] portrait-640:pt-[14px] portrait-640:pb-[16px] portrait-640:overscroll-contain">
                    {isPhone ? (
                        <>
                            {listSection}
                            {presetsBlock && <section>{presetsBlock}</section>}
                            {addSection}
                            {overlaysSection}
                        </>
                    ) : (
                        <>
                            {listSection}
                            {overlaysSection}
                            {addSection}
                        </>
                    )}
                </div>

                <div className="flex items-center gap-[8px] py-[14px] px-[20px] border-t border-t-sg-line-soft portrait-640:pt-[12px] portrait-640:px-[16px] portrait-640:pb-[calc(12px_+_env(safe-area-inset-bottom))] portrait-640:bg-sg-surface">
                    <button
                        className="h-[34px] py-0 px-[8px] border-none rounded-ctrl bg-transparent text-sg-muted [font-family:inherit] text-[0.84rem] cursor-pointer hover:not-disabled:text-sg-loss disabled:opacity-[0.4] disabled:cursor-default"
                        onClick={() => { setDraft([]); setError(''); }}
                        disabled={draft.length === 0}
                    >
                        Remove all
                    </button>
                    <span className="ml-auto text-sg-faint text-[0.76rem] whitespace-nowrap lte-640:hidden">
                        <kbd className={kbd}>Ctrl</kbd> <kbd className={kbd}>Enter</kbd> to apply, <kbd className={kbd}>Esc</kbd> to close
                    </span>
                    <button className="h-[38px] py-0 px-[14px] border border-solid border-sg-line rounded-ctrl bg-sg-raised text-sg-text [font-family:inherit] text-[0.88rem] font-medium cursor-pointer hover:bg-sg-hover lte-640:ml-auto portrait-640:min-h-[44px]" onClick={onClose}>Cancel</button>
                    <button className={cn(primaryBtn, 'portrait-640:min-h-[44px]')} onClick={handleApply} disabled={!dirty}>
                        Apply
                    </button>
                </div>
            </div>
        </div>
    );
}
