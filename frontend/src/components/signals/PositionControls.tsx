import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { postPositionCommand } from '../../services/api';
import type { OpenOrder } from './signalAnalytics';
import { canEdit, exitCommand, levelCommand, levelOnRightSide, type LevelKind } from './positionCommands';

const posRow = 'flex flex-wrap items-center gap-[4px]';
const posLabel = 'inline-flex items-center gap-[3px] text-[0.7rem] text-sg-muted';

const fmt = (v: number | null | undefined) => (v && v > 0 ? v.toFixed(2) : '');
const num = (s: string) => (s.trim() === '' ? null : Number(s));

/**
 * SL / Target / Exit buttons for an open paper position. SL and Target open
 * an inline editor for the premium and index level; Exit asks once more
 * before sending. Real-order positions are read-only.
 */
export function PositionControls({ order, currentPrice }: { order: OpenOrder; currentPrice: number | null }) {
    const [editing, setEditing] = useState<LevelKind | null>(null);
    const [premium, setPremium] = useState('');
    const [index, setIndex] = useState('');
    const [confirmExit, setConfirmExit] = useState(false);
    const [note, setNote] = useState('');

    if (order.real) return <span className="text-[0.68rem] text-sg-faint" title="Real orders are not edited from the dashboard">real · read-only</span>;
    if (!canEdit(order)) return null;

    const open = (kind: LevelKind) => {
        setConfirmExit(false);
        setNote('');
        setEditing(kind);
        setPremium(fmt(kind === 'stop' ? order.stoplossPrice : order.targetPrice));
        setIndex(fmt(kind === 'stop' ? order.indexStop : order.indexTarget));
    };

    const send = async (cmd: Parameters<typeof postPositionCommand>[0]) => {
        try {
            await postPositionCommand(cmd);
            setNote('sent');
        } catch (e) {
            setNote(e instanceof Error ? e.message : 'failed');
        }
    };

    const save = () => {
        if (!editing) return;
        const p = num(premium);
        const ix = num(index);
        if (p !== null && currentPrice !== null && !levelOnRightSide(editing, p, currentPrice, order.side, false)) {
            setNote(editing === 'stop' ? 'premium SL must be below LTP' : 'premium target must be above LTP');
            return;
        }
        // Unchanged values are not resent: an index level untouched keeps
        // the strategy's own management.
        const cmd = levelCommand(order, editing, {
            premium: p !== null && p !== num(fmt(editing === 'stop' ? order.stoplossPrice : order.targetPrice)) ? p : null,
            index: ix !== null && ix !== num(fmt(editing === 'stop' ? order.indexStop : order.indexTarget)) ? ix : null,
        });
        setEditing(null);
        if (cmd) void send(cmd);
    };

    if (editing) {
        const label = editing === 'stop' ? 'SL' : 'TGT';
        return (
            <div className={posRow}>
                <label className={posLabel}>{label} ₹<Input variant="xs" aria-label={`${label} premium`} type="number" step="0.05" value={premium} onChange={e => setPremium(e.target.value)} /></label>
                <label className={posLabel}>idx<Input variant="xs" aria-label={`${label} index`} type="number" step="0.05" value={index} onChange={e => setIndex(e.target.value)} /></label>
                <Button type="button" variant="secondary" size="xs" onClick={save}>Save</Button>
                <Button type="button" variant="bordered" size="xs" tone="muted" onClick={() => setEditing(null)}>Cancel</Button>
            </div>
        );
    }

    return (
        <div className={posRow}>
            <Button type="button" variant="secondary" size="xs" onClick={() => open('stop')}>SL</Button>
            <Button type="button" variant="secondary" size="xs" onClick={() => open('target')}>TGT</Button>
            {confirmExit ? (
                <>
                    <Button type="button" variant="destructive" size="xs" onClick={() => { setConfirmExit(false); void send(exitCommand(order)); }}>Confirm exit</Button>
                    <Button type="button" variant="bordered" size="xs" tone="muted" onClick={() => setConfirmExit(false)}>No</Button>
                </>
            ) : (
                <Button type="button" variant="destructive" size="xs" onClick={() => setConfirmExit(true)}>Exit</Button>
            )}
            {note && <span className="text-[0.68rem] text-sg-muted">{note}</span>}
        </div>
    );
}
