import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Plus, Trash2, X } from 'lucide-react';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { sendSubscribe } from '../../hooks/useWebSocket';
import { tfLabel, getEntryColor, entryKey } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import styles from './Settings.module.css';

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
    const chartTF = selectedTF || 60;

    const [draft, setDraft] = useState<IndicatorEntry[]>([]);
    const [indType, setIndType] = useState<IndType>('EMA');
    const [period, setPeriod] = useState('');
    const [indTF, setIndTF] = useState(chartTF);
    const [error, setError] = useState('');
    const periodRef = useRef<HTMLInputElement>(null);
    const returnFocus = useRef<HTMLElement | null>(null);

    // Sync draft with the saved list each time the dialog opens
    useEffect(() => {
        if (!open) return;
        returnFocus.current = document.activeElement as HTMLElement | null;
        setDraft((activeIndicators || []).map((e) => ({ ...e, color: getEntryColor(e) })));
        setIndTF(chartTF);
        setPeriod('');
        setError('');
        const t = setTimeout(() => periodRef.current?.focus(), 30);
        return () => clearTimeout(t);
    }, [open, chartTF]); // eslint-disable-line react-hooks/exhaustive-deps

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
        return true;
    }, [draft]);

    const addFromForm = useCallback(() => {
        if (add(indType, Number(period), indTF)) {
            setPeriod('');
            periodRef.current?.focus();
        }
    }, [add, indType, period, indTF]);

    const removeEntry = useCallback((key: string) => {
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

    return (
        <div
            className={`${styles.overlay} ${open ? styles.open : ''}`}
            onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
        >
            <div className={styles.modal} role="dialog" aria-modal="true" aria-labelledby="ind-title">
                <div className={styles.header}>
                    <div>
                        <h2 id="ind-title" className={styles.title}>Indicators</h2>
                        <p className={styles.subtitle}>Lines drawn on the {tfLabel(chartTF)} chart</p>
                    </div>
                    <button className={styles.iconBtn} onClick={onClose} aria-label="Close">
                        <X size={18} />
                    </button>
                </div>

                <div className={styles.body}>
                    <section>
                        <h3 className={styles.sectionTitle}>
                            On chart <span className={styles.count}>{draft.length}</span>
                        </h3>
                        {sorted.length === 0 ? (
                            <p className={styles.empty}>No indicators yet. Add one below or pick a quick add.</p>
                        ) : (
                            <ul className={styles.list}>
                                {sorted.map((entry) => {
                                    const key = entryKey(entry);
                                    const color = getEntryColor(entry);
                                    return (
                                        <li key={key} className={styles.row}>
                                            <label className={styles.swatch} style={{ background: color }} title="Change line colour">
                                                <input
                                                    type="color"
                                                    value={color}
                                                    onChange={(e) => recolor(key, e.target.value)}
                                                    aria-label={`Colour for ${displayName(entry.name)}`}
                                                />
                                            </label>
                                            <span className={styles.rowName}>{displayName(entry.name)}</span>
                                            <span className={styles.tfTag}>{tfLabel(entry.tf)}</span>
                                            {entry.tf > chartTF && (
                                                <span className={styles.hiddenTag} title="Hidden until you switch to this timeframe or higher">
                                                    Hidden on {tfLabel(chartTF)}
                                                </span>
                                            )}
                                            <button
                                                className={styles.rowRemove}
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

                    <section>
                        <h3 className={styles.sectionTitle}>Add indicator</h3>
                        <form
                            className={styles.addRow}
                            onSubmit={(e) => { e.preventDefault(); addFromForm(); }}
                        >
                            <div className={styles.segmented} role="radiogroup" aria-label="Type">
                                {TYPES.map(t => (
                                    <button
                                        key={t.id}
                                        type="button"
                                        role="radio"
                                        aria-checked={indType === t.id}
                                        className={`${styles.segBtn}${indType === t.id ? ` ${styles.segActive}` : ''}`}
                                        onClick={() => setIndType(t.id)}
                                    >
                                        {t.label}
                                    </button>
                                ))}
                            </div>
                            <input
                                ref={periodRef}
                                className={styles.input}
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
                                className={styles.input}
                                value={indTF}
                                onChange={(e) => setIndTF(Number(e.target.value))}
                                aria-label="Compute timeframe"
                                title="Timeframe the indicator is computed on"
                            >
                                {allowedTFs.map((t) => (
                                    <option key={t} value={t}>{tfLabel(t)}</option>
                                ))}
                            </select>
                            <button type="submit" className={styles.addBtn} disabled={!period}>
                                <Plus size={15} /> Add
                            </button>
                        </form>
                        {error && <p className={styles.error} role="alert">{error}</p>}

                        {presets.length > 0 && (
                            <div className={styles.presets}>
                                <span className={styles.presetsLabel}>Quick add on {tfLabel(indTF)}</span>
                                {presets.map(p => (
                                    <button
                                        key={`${p.type}_${p.period}`}
                                        type="button"
                                        className={styles.preset}
                                        onClick={() => add(p.type, p.period, indTF)}
                                    >
                                        {p.type} {p.period}
                                    </button>
                                ))}
                            </div>
                        )}
                    </section>
                </div>

                <div className={styles.footer}>
                    <button
                        className={styles.textBtn}
                        onClick={() => { setDraft([]); setError(''); }}
                        disabled={draft.length === 0}
                    >
                        Remove all
                    </button>
                    <span className={styles.hint}>
                        <kbd>Ctrl</kbd> <kbd>Enter</kbd> to apply, <kbd>Esc</kbd> to close
                    </span>
                    <button className={styles.secondaryBtn} onClick={onClose}>Cancel</button>
                    <button className={styles.primaryBtn} onClick={handleApply} disabled={!dirty}>
                        Apply
                    </button>
                </div>
            </div>
        </div>
    );
}
