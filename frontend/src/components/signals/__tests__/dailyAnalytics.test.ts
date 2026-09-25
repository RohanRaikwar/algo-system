import { describe, expect, it } from 'vitest';
import { computeDailyWindowStats, createOrdersByDateMap } from '../dailyAnalytics';

describe('dailyAnalytics helpers', () => {
    it('computes window stats from daily rows', () => {
        const stats = computeDailyWindowStats([
            {
                date: '2026-03-20',
                pnl: 1500,
                trades: 4,
                wins: 3,
                losses: 1,
                win_rate: 75,
                largest_win: 1000,
                largest_loss: -200,
            },
            {
                date: '2026-03-19',
                pnl: -500,
                trades: 2,
                wins: 0,
                losses: 2,
                win_rate: 0,
                largest_win: 0,
                largest_loss: -300,
            },
        ]);

        expect(stats.pnl).toBe(1000);
        expect(stats.trades).toBe(6);
        expect(stats.wins).toBe(3);
        expect(stats.losses).toBe(3);
        expect(stats.winRate).toBe(50);
    });

    it('creates date-keyed lookup for order groups', () => {
        const byDate = createOrdersByDateMap([
            {
                date: '2026-03-20',
                pnl: 100,
                trades: 1,
                orders: [{
                    strategy: 'S1',
                    side: 'CALL',
                    exchange: 'NFO',
                    token: '111',
                    instrument: 'NFO:111',
                    qty: 1,
                    entry_price: 100,
                    exit_price: 110,
                    realized_pnl: 10,
                    entry_time: '2026-03-20T10:00:00Z',
                    exit_time: '2026-03-20T10:05:00Z',
                }],
            },
        ]);

        expect(byDate.has('2026-03-20')).toBe(true);
        expect(byDate.get('2026-03-20')?.trades).toBe(1);
        expect(byDate.get('2026-03-20')?.orders[0].instrument).toBe('NFO:111');
    });
});
