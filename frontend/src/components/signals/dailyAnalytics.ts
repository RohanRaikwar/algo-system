import type { DailyOrdersGroup, DailyPnLItem } from '../../services/api';

export interface DailyWindowStats {
    pnl: number;
    trades: number;
    wins: number;
    losses: number;
    winRate: number;
}

export function computeDailyWindowStats(rows: DailyPnLItem[]): DailyWindowStats {
    let pnl = 0;
    let trades = 0;
    let wins = 0;
    let losses = 0;

    for (const row of rows) {
        pnl += row.pnl;
        trades += row.trades;
        wins += row.wins;
        losses += row.losses;
    }

    const winRate = wins + losses > 0 ? (wins / (wins + losses)) * 100 : 0;
    return { pnl, trades, wins, losses, winRate };
}

export function createOrdersByDateMap(rows: DailyOrdersGroup[]): Map<string, DailyOrdersGroup> {
    const byDate = new Map<string, DailyOrdersGroup>();
    for (const row of rows) {
        byDate.set(row.date, row);
    }
    return byDate;
}
