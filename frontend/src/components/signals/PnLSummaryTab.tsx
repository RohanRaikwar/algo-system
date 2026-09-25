import { useEffect, useState, useCallback } from 'react';
import { TrendingUp, TrendingDown, Trophy, Target, Activity, BarChart3 } from 'lucide-react';
import { fetchPnLSummary, type PnLSummary } from '../../services/api';

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
            <div className="signals-empty">
                <p>Loading P&L data…</p>
            </div>
        );
    }

    const pnlTone = data.realized_pnl > 0 ? ' pos' : data.realized_pnl < 0 ? ' neg' : '';
    const winRatePct = (data.win_rate * 100).toFixed(1);

    return (
        <div className="pnl-summary">
            {/* Main P&L Card */}
            <div className={`pnl-hero${pnlTone}`}>
                <div className="pnl-hero-label">Realized P&L</div>
                <div className="pnl-hero-value">
                    {data.realized_pnl >= 0 ? <TrendingUp size={28} /> : <TrendingDown size={28} />}
                    {formatINR(data.realized_pnl)}
                </div>
            </div>

            {/* Stats Grid */}
            <div className="pnl-grid">
                <div className="pnl-card">
                    <div className="pnl-card-icon"><Target size={18} /></div>
                    <div className="pnl-card-label">Total trades</div>
                    <div className="pnl-card-value">{data.total_trades}</div>
                </div>
                <div className="pnl-card">
                    <div className="pnl-card-icon pos"><Trophy size={18} /></div>
                    <div className="pnl-card-label">Wins</div>
                    <div className="pnl-card-value pos">{data.wins}</div>
                </div>
                <div className="pnl-card">
                    <div className="pnl-card-icon neg"><TrendingDown size={18} /></div>
                    <div className="pnl-card-label">Losses</div>
                    <div className="pnl-card-value neg">{data.losses}</div>
                </div>
                <div className="pnl-card">
                    <div className="pnl-card-icon accent"><Activity size={18} /></div>
                    <div className="pnl-card-label">Win rate</div>
                    <div className="pnl-card-value">{winRatePct}%</div>
                </div>
                <div className="pnl-card">
                    <div className="pnl-card-icon pos"><BarChart3 size={18} /></div>
                    <div className="pnl-card-label">Largest win</div>
                    <div className="pnl-card-value pos">{formatINR(data.largest_win)}</div>
                </div>
                <div className="pnl-card">
                    <div className="pnl-card-icon neg"><BarChart3 size={18} /></div>
                    <div className="pnl-card-label">Largest loss</div>
                    <div className="pnl-card-value neg">{formatINR(data.largest_loss)}</div>
                </div>
                <div className="pnl-card">
                    <div className="pnl-card-icon"><Target size={18} /></div>
                    <div className="pnl-card-label">Open positions</div>
                    <div className="pnl-card-value">{data.open_positions}</div>
                </div>
                <div className="pnl-card">
                    <div className="pnl-card-icon"><Activity size={18} /></div>
                    <div className="pnl-card-label">Exposure</div>
                    <div className="pnl-card-value">{formatINR(data.total_exposure)}</div>
                </div>
            </div>

            {data.ts && (
                <div className="pnl-updated">
                    Last updated: {new Date(data.ts).toLocaleTimeString('en-IN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, timeZone: 'Asia/Kolkata' })}
                </div>
            )}
        </div>
    );
}
