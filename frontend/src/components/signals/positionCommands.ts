import type { PositionCommand } from '../../services/api';
import type { OpenOrder } from './signalAnalytics';

/** Rupees → integer paise. */
export function rupeesToPaise(rupees: number): number {
    return Math.round(rupees * 100);
}

/** Index price snapped to the 0.05 tick. */
export function snapToTick(rupees: number): number {
    return Math.round(rupees * 20) / 20;
}

export type LevelKind = 'stop' | 'target';

/**
 * Whether a level sits on the right side of the reference price: a stop on
 * the losing side, a target on the winning side. Premium levels are always
 * long (premium up = profit); index levels flip for a PUT.
 */
export function levelOnRightSide(kind: LevelKind, level: number, ref: number, side: string, index: boolean): boolean {
    if (!(level > 0) || !(ref > 0)) return false;
    const up = !index || side !== 'PUT';
    const above = level > ref;
    return kind === 'target' ? above === up : (level < ref) === up;
}

/** Edits for a stop or target, rupees; a null leaves that level unchanged. */
export interface LevelEditRupees {
    premium: number | null;
    index: number | null;
}

/** set_levels command for an order; null when nothing would change. */
export function levelCommand(order: OpenOrder, kind: LevelKind, edit: LevelEditRupees): PositionCommand | null {
    const cmd: PositionCommand = {
        strategy: order.strategy, side: order.side, fno_token: order.fnoToken, action: 'set_levels',
    };
    if (edit.premium !== null && edit.premium > 0) {
        cmd[kind === 'stop' ? 'fno_sl' : 'fno_target'] = rupeesToPaise(edit.premium);
    }
    if (edit.index !== null && edit.index > 0) {
        cmd[kind === 'stop' ? 'index_stop' : 'index_target'] = rupeesToPaise(snapToTick(edit.index));
    }
    const { fno_sl, fno_target, index_stop, index_target } = cmd;
    return fno_sl || fno_target || index_stop || index_target ? cmd : null;
}

export function exitCommand(order: OpenOrder): PositionCommand {
    return { strategy: order.strategy, side: order.side, fno_token: order.fnoToken, action: 'exit' };
}

/** Dashboard edits are for paper single-leg positions only. */
export function canEdit(order: OpenOrder): boolean {
    return !order.real && !order.leg;
}

/** The position a chart command is for. */
export type PositionRef = Pick<PositionCommand, 'strategy' | 'side' | 'fno_token'>;

/** Index level kinds the chart can remove. */
export type IndexLevelKind = LevelKind | 'trail';

/** set_levels command removing the index stop, target or trail. */
export function removeCommand(pos: PositionRef, kind: IndexLevelKind): PositionCommand {
    const field = kind === 'stop' ? 'index_stop' : kind === 'target' ? 'index_target' : 'index_trail';
    return { ...pos, action: 'set_levels', [field]: -1 };
}

/** set_levels command trailing the index stop by points; null for a bad distance. */
export function trailCommand(pos: PositionRef, points: number): PositionCommand | null {
    const paise = rupeesToPaise(snapToTick(points));
    return paise > 0 ? { ...pos, action: 'set_levels', index_trail: paise } : null;
}

/** Index points from entry to level (paise in), positive in the position's favour. */
export function pointsFrom(entry: number, level: number, side: string): number {
    const sign = side === 'PUT' ? -1 : 1;
    return Math.round((level - entry) * sign) / 100;
}

/** Signed points label: "+42.5 pts", "−35 pts". */
export function ptsLabel(points: number): string {
    const sign = points > 0 ? '+' : points < 0 ? '−' : '';
    return `${sign}${Number(Math.abs(points).toFixed(2))} pts`;
}
