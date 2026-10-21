import { useState } from 'react';
import { postPositionCommand } from '../../services/api';
import type { OpenOrder } from './signalAnalytics';
import { canEdit, exitCommand, levelCommand, levelOnRightSide, type LevelKind } from './positionCommands';

const posBtnBase = 'py-[2px] px-[8px] border border-solid rounded-ctrl text-[0.72rem] font-semibold cursor-pointer focus-visible:outline-none';
const posBtn = `${posBtnBase} border-sg-line bg-sg-raised text-sg-text hover:bg-sg-hover`;
const posBtnDanger = `${posBtnBase} border-sg-loss-soft bg-sg-raised text-sg-loss hover:bg-sg-hover`;
/* .pos-btn.ghost kept its transparent background on hover. */
const posBtnGhost = `${posBtnBase} border-sg-line bg-transparent text-sg-muted`;
const posRow = 'flex flex-wrap items-center gap-[4px]';
const posLabel = 'inline-flex items-center gap-[3px] text-[0.7rem] text-sg-muted';
const posInput = 'w-[76px] py-[2px] px-[4px] border border-solid border-sg-line rounded-ctrl bg-sg-surface text-sg-text leading-[inherit] text-[0.74rem]';

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
                <label className={posLabel}>{label} ₹<input className={posInput} aria-label={`${label} premium`} type="number" step="0.05" value={premium} onChange={e => setPremium(e.target.value)} /></label>
                <label className={posLabel}>idx<input className={posInput} aria-label={`${label} index`} type="number" step="0.05" value={index} onChange={e => setIndex(e.target.value)} /></label>
                <button type="button" className={posBtn} onClick={save}>Save</button>
                <button type="button" className={posBtnGhost} onClick={() => setEditing(null)}>Cancel</button>
            </div>
        );
    }

    return (
        <div className={posRow}>
            <button type="button" className={posBtn} onClick={() => open('stop')}>SL</button>
            <button type="button" className={posBtn} onClick={() => open('target')}>TGT</button>
            {confirmExit ? (
                <>
                    <button type="button" className={posBtnDanger} onClick={() => { setConfirmExit(false); void send(exitCommand(order)); }}>Confirm exit</button>
                    <button type="button" className={posBtnGhost} onClick={() => setConfirmExit(false)}>No</button>
                </>
            ) : (
                <button type="button" className={posBtnDanger} onClick={() => setConfirmExit(true)}>Exit</button>
            )}
            {note && <span className="text-[0.68rem] text-sg-muted">{note}</span>}
        </div>
    );
}
