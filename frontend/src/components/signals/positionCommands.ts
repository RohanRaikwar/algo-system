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
