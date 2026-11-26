import { useCallback, useEffect, useState } from 'react';
import { cn } from '@/lib/utils';
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select';
import { useAppStore } from '../../store/useAppStore';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog';
import { BottomSheet } from '../layout/BottomSheet';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../../hooks/useMediaQuery';
import { tokenLabel } from '../../utils/helpers';
import {
    fetchChain, fetchChainIndexes,
    type OptChainIndex, type OptChainLeg, type OptChainView,
} from '../../services/optionChain';
import { OptionChart } from './OptionChart';
import { rupees } from './liveBars';

const POLL_MS = 15_000;

export interface Picked {
    exchange: string;
    token: string;
    symbol: string;
}

/** Polls fn every POLL_MS while the page is visible. */
function useVisiblePoll(fn: () => void, deps: unknown[]) {
    useEffect(() => {
        fn();
        const id = window.setInterval(() => {
            if (document.visibilityState === 'visible') fn();
        }, POLL_MS);
        return () => window.clearInterval(id);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, deps);
}

/**
 * Option chain of NIFTY, SENSEX and BANKNIFTY: nearest expiry, ATM ± N
 * strikes, prices from one quote snapshot (nothing is subscribed). Clicking
 * a CE/PE price opens its chart, which streams that one contract.
 */
interface OptionChainPanelProps {
    /** 'panel': inside the dashboard side sheet; the toolbar's index picker drives it. */
    variant?: 'page' | 'panel';
    /** When set, a picked contract goes here instead of the built-in dialog. */
    onPick?: (p: Picked) => void;
}

export function OptionChainPanel({ variant = 'page', onPick }: OptionChainPanelProps = {}) {
    const inPanel = variant === 'panel';
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    const [indexes, setIndexes] = useState<OptChainIndex[]>([]);
    const selected = useAppStore(s => s.selectedToken);
    const setSelected = useAppStore(s => s.setSelectedToken);
    const [view, setView] = useState<OptChainView | null>(null);
    const [error, setError] = useState('');
    const [picked, setPicked] = useState<Picked | null>(null);

    useEffect(() => {
        fetchChainIndexes().then(r => setIndexes(r.indexes)).catch((e: Error) => setError(e.message));
    }, []);

    // The chain follows the app's selected index (the chart's dropdown);
    // an index without a chain falls back to the first listed one.
    const listed = indexes.some(i => i.index === selected);
    const index = listed ? (selected as string) : (indexes[0]?.index ?? '');
    useEffect(() => {
        if (index && !listed) setSelected(index);
    }, [index, listed, setSelected]);
    useEffect(() => { setView(null); }, [index]);

    const load = useCallback(() => {
        if (!index) return;
        fetchChain(index).then(v => { setView(v); setError(''); }).catch((e: Error) => setError(e.message));
    }, [index]);
    useVisiblePoll(load, [load]);

    const open = (leg?: OptChainLeg) => {
        if (!leg || !view) return;
        const p = { exchange: view.exchange, token: leg.token, symbol: leg.symbol };
        if (onPick) onPick(p);
        else setPicked(p);
    };
    const close = () => setPicked(null);

    const legCell = (leg: OptChainLeg | undefined, itm: boolean) => (
        <td className={cn('px-2 py-1 text-right tabular-nums', itm && 'bg-sg-raised')}>
            {leg ? (
                <button
                    type="button"
                    onClick={() => open(leg)}
                    className="underline-offset-2 hover:underline text-sg-text"
                    title={`Chart ${leg.symbol}`}
                >
                    {rupees(leg.ltp)}
                </button>
            ) : '—'}
        </td>
    );
    const greek = (n?: number, digits = 2) => (n ? n.toFixed(digits) : '');

    const spot = view ? view.spot / 100 : 0;
    const chart = picked && <OptionChart key={picked.token} {...picked} />;

    return (
        <div className="flex flex-col gap-3 min-w-0">
            {indexes.length > 0 && !inPanel && (
                <div className="flex items-center gap-2">
                    <NativeSelect variant="filter" value={index} onChange={e => setSelected(e.target.value)} aria-label="Index">
                        {indexes.map(i => (
                            <NativeSelectOption key={i.index} value={i.index}>{tokenLabel(i.index)}</NativeSelectOption>
                        ))}
                    </NativeSelect>
                </div>
            )}

            {error && <div className="text-sg-warn text-sm">{error}</div>}

            {view && (
                <>
                    <div className="flex gap-4 flex-wrap text-sm text-sg-muted">
                        <span>Spot <b className="text-sg-text tabular-nums">{spot.toFixed(2)}</b></span>
                        <span>Expiry <b className="text-sg-text">{view.expiry}</b></span>
                        <span>
                            {view.priced_at
                                ? `Prices as of ${new Date(view.priced_at).toLocaleTimeString()}`
                                : view.price_note || 'No prices'}
                        </span>
                    </div>
                    <div className={cn('overflow-auto border border-sg-line rounded-xl bg-sg-bg', !inPanel && 'max-h-[min(72vh,820px)]')}>
                        <table className="w-full text-sm border-collapse">
                            <thead className="sticky top-0 bg-sg-surface text-sg-muted text-xs">
                                <tr>
                                    <th className="px-2 py-1 text-right">IV</th>
                                    <th className="px-2 py-1 text-right">Δ</th>
                                    <th className="px-2 py-1 text-right">CE</th>
                                    <th className="px-2 py-1 text-center">Strike</th>
                                    <th className="px-2 py-1 text-right">PE</th>
                                    <th className="px-2 py-1 text-right">Δ</th>
                                    <th className="px-2 py-1 text-right">IV</th>
                                </tr>
                            </thead>
                            <tbody>
                                {view.rows.map(r => {
                                    const atm = r.strike === view.atm;
                                    return (
                                        <tr key={r.strike} className={cn('border-t border-sg-line-soft', atm && 'bg-sg-hover')}>
                                            <td className="px-2 py-1 text-right text-sg-faint tabular-nums">{greek(r.ce?.iv, 1)}</td>
                                            <td className="px-2 py-1 text-right text-sg-faint tabular-nums">{greek(r.ce?.delta)}</td>
                                            {legCell(r.ce, r.strike < spot)}
                                            <td className={cn('px-2 py-1 text-center tabular-nums font-semibold', atm && 'text-sg-accent')}>{r.strike}</td>
                                            {legCell(r.pe, r.strike > spot)}
                                            <td className="px-2 py-1 text-right text-sg-faint tabular-nums">{greek(r.pe?.delta)}</td>
                                            <td className="px-2 py-1 text-right text-sg-faint tabular-nums">{greek(r.pe?.iv, 1)}</td>
                                        </tr>
                                    );
                                })}
                            </tbody>
                        </table>
                    </div>
                </>
            )}

            {onPick ? null : isPhone ? (
                <BottomSheet open={!!picked} onClose={close} title={picked?.symbol ?? ''}>
                    {chart}
                </BottomSheet>
            ) : (
                <Dialog open={!!picked} onOpenChange={o => { if (!o) close(); }}>
                    <DialogContent aria-labelledby="optchart-title" showCloseButton className="sm:max-w-[920px] w-[min(96vw,920px)]">
                        <DialogHeader>
                            <div>
                                <DialogTitle id="optchart-title">{picked?.symbol}</DialogTitle>
                                <DialogDescription>Last 7 days + live. History is kept one day.</DialogDescription>
                            </div>
                        </DialogHeader>
                        {chart}
                    </DialogContent>
                </Dialog>
            )}
        </div>
    );
}
