import { useEffect, useState, useCallback } from 'react';
import { TrendingUp, TrendingDown, Trophy, Target, Activity, BarChart3 } from 'lucide-react';
import { fetchPnLSummary, type PnLSummary } from '../../services/api';
import { cn } from '@/lib/utils';
import { emptyState, footerNote } from './classes';

const card = 'grid grid-cols-[auto_1fr] grid-rows-[auto_auto] gap-x-[10px] py-[12px] px-[14px] bg-sg-bg border border-solid border-sg-line rounded-[12px]';
const iconBase = '[grid-row:span_2] flex items-center justify-center w-[34px] h-[34px] rounded-[10px]';
const icon = `${iconBase} bg-sg-raised text-sg-muted`;
const iconPos = `${iconBase} text-sg-profit bg-sg-profit-soft`;
const iconNeg = `${iconBase} text-sg-loss bg-sg-loss-soft`;
const iconAccent = `${iconBase} text-sg-accent bg-sg-accent-soft`;
const label = 'text-sg-muted text-[0.8rem]';
const valueBase = 'text-[1.1rem] font-semibold tabular-nums';
const value = `${valueBase} text-sg-text`;
const valuePos = `${valueBase} text-sg-profit`;
const valueNeg = `${valueBase} text-sg-loss`;

const DEFAULT: PnLSummary = {
    realized_pnl: 0, total_trades: 0, wins: 0, losses: 0, win_rate: 0,
    largest_win: 0, largest_loss: 0, open_positions: 0, total_exposure: 0,
};

function formatINR(paise: number): string {
    const rupees = paise / 100;
    return rupees.toLocaleString('en-IN', { style: 'currency', currency: 'INR', minimumFractionDigits: 2 });
}

export function PnLSummaryTab() {
    const [data, setData] = useState<PnLSummary>(DEFAULT);
    const [loading, setLoading] = useState(true);

    const load = useCallback(async () => {
        try {
            const d = await fetchPnLSummary();
            setData({ ...DEFAULT, ...d });
        } catch { /* ignore */ }
        setLoading(false);
    }, []);

    // Initial fetch
    useEffect(() => { load(); }, [load]);

    // Listen for live P&L updates via WS
    useEffect(() => {
        const handler = (e: CustomEvent) => {
            if (e.detail?.type === 'pnl' && e.detail.data) {
                setData({ ...DEFAULT, ...(e.detail.data as Partial<PnLSummary>) });
            }
        };
        window.addEventListener('ws:message', handler as EventListener);
        return () => window.removeEventListener('ws:message', handler as EventListener);
    }, []);

    if (loading) {
        return (
            <div className={emptyState}>
                <p>Loading P&L data…</p>
            </div>
        );
    }

    const pnlSign = data.realized_pnl > 0 ? 'pos' : data.realized_pnl < 0 ? 'neg' : 'flat';
    const winRatePct = (data.win_rate * 100).toFixed(1);

    return (
        <div className="flex flex-col gap-[12px]">
            {/* Main P&L Card */}
            <div className={cn(
                'py-[20px] px-[22px] bg-sg-bg border border-solid border-sg-line border-l-[3px] rounded-[12px]',
                pnlSign === 'pos' ? 'border-l-sg-profit' : pnlSign === 'neg' ? 'border-l-sg-loss' : 'border-l-sg-muted',
            )}>
                <div className="text-sg-muted text-[0.85rem]">Realized P&L</div>
                <div className={cn(
                    'flex items-center gap-[10px] mt-[4px] text-[2.2rem] font-semibold tracking-[-0.02em] tabular-nums lte-640:text-[1.7rem]',
                    pnlSign === 'pos' ? 'text-sg-profit' : pnlSign === 'neg' ? 'text-sg-loss' : 'text-sg-text-2',
                )}>
                    {data.realized_pnl >= 0 ? <TrendingUp size={28} /> : <TrendingDown size={28} />}
                    {formatINR(data.realized_pnl)}
                </div>
            </div>

            {/* Stats Grid */}
            <div className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-[8px]">
                <div className={card}>
                    <div className={icon}><Target size={18} /></div>
                    <div className={label}>Total trades</div>
                    <div className={value}>{data.total_trades}</div>
                </div>
                <div className={card}>
                    <div className={iconPos}><Trophy size={18} /></div>
                    <div className={label}>Wins</div>
                    <div className={valuePos}>{data.wins}</div>
                </div>
                <div className={card}>
                    <div className={iconNeg}><TrendingDown size={18} /></div>
                    <div className={label}>Losses</div>
                    <div className={valueNeg}>{data.losses}</div>
                </div>
                <div className={card}>
                    <div className={iconAccent}><Activity size={18} /></div>
                    <div className={label}>Win rate</div>
                    <div className={value}>{winRatePct}%</div>
                </div>
                <div className={card}>
                    <div className={iconPos}><BarChart3 size={18} /></div>
                    <div className={label}>Largest win</div>
                    <div className={valuePos}>{formatINR(data.largest_win)}</div>
                </div>
                <div className={card}>
                    <div className={iconNeg}><BarChart3 size={18} /></div>
                    <div className={label}>Largest loss</div>
                    <div className={valueNeg}>{formatINR(data.largest_loss)}</div>
                </div>
                <div className={card}>
                    <div className={icon}><Target size={18} /></div>
                    <div className={label}>Open positions</div>
                    <div className={value}>{data.open_positions}</div>
                </div>
                <div className={card}>
                    <div className={icon}><Activity size={18} /></div>
                    <div className={label}>Exposure</div>
                    <div className={value}>{formatINR(data.total_exposure)}</div>
                </div>
            </div>

            {data.ts && (
                <div className={footerNote}>
                    Last updated: {new Date(data.ts).toLocaleTimeString('en-IN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, timeZone: 'Asia/Kolkata' })}
                </div>
            )}
        </div>
    );
}
