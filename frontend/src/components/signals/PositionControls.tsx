import { useState } from 'react';
import { postPositionCommand } from '../../services/api';
import type { OpenOrder } from './signalAnalytics';
import { canEdit, exitCommand, levelCommand, levelOnRightSide, type LevelKind } from './positionCommands';

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

    if (order.real) return <span className="pos-readonly" title="Real orders are not edited from the dashboard">real · read-only</span>;
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
            <div className="pos-edit">
                <label>{label} ₹<input aria-label={`${label} premium`} type="number" step="0.05" value={premium} onChange={e => setPremium(e.target.value)} /></label>
                <label>idx<input aria-label={`${label} index`} type="number" step="0.05" value={index} onChange={e => setIndex(e.target.value)} /></label>
                <button type="button" className="pos-btn" onClick={save}>Save</button>
                <button type="button" className="pos-btn ghost" onClick={() => setEditing(null)}>Cancel</button>
            </div>
        );
    }

    return (
        <div className="pos-controls">
            <button type="button" className="pos-btn" onClick={() => open('stop')}>SL</button>
            <button type="button" className="pos-btn" onClick={() => open('target')}>TGT</button>
            {confirmExit ? (
                <>
                    <button type="button" className="pos-btn danger" onClick={() => { setConfirmExit(false); void send(exitCommand(order)); }}>Confirm exit</button>
                    <button type="button" className="pos-btn ghost" onClick={() => setConfirmExit(false)}>No</button>
                </>
            ) : (
                <button type="button" className="pos-btn danger" onClick={() => setConfirmExit(true)}>Exit</button>
            )}
            {note && <span className="pos-note">{note}</span>}
        </div>
    );
}
