import { useEffect } from 'react';
import { useStrikeStore } from '../../store/useStrikeStore';
import { fetchStrikeInfo } from '../../services/api';
import { Target, TrendingUp, TrendingDown, Clock, Activity, Package } from 'lucide-react';

/** Format paise to rupees string */
function fmtPrice(paise: number): string {
    if (!paise || paise <= 0) return '—';
    return '₹' + (paise / 100).toLocaleString('en-IN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** Format ISO timestamp to IST HH:MM:SS */
function fmtTime(iso: string): string {
    if (!iso) return '—';
    const d = new Date(iso);
    return d.toLocaleTimeString('en-IN', { timeZone: 'Asia/Kolkata', hour12: false });
}

export function FnoInstrumentsTab() {
    const strike = useStrikeStore(s => s.strike);
    const setStrike = useStrikeStore(s => s.setStrike);
    const callLTP = useStrikeStore(s => s.callLTP);
    const putLTP = useStrikeStore(s => s.putLTP);

    // Fetch from REST on mount
    useEffect(() => {
        fetchStrikeInfo().then(data => {
            if (data && data.resolved) setStrike(data);
        }).catch(() => { });
    }, [setStrike]);

    if (!strike || !strike.resolved) {
        return (
            <div className="fno-instruments-wrap">
                <div className="fno-pending">
                    <Target size={48} strokeWidth={1.5} />
                    <p>Waiting for ATM strike resolution…</p>
                    <span className="fno-pending-sub">Strikes resolve on first NIFTY tick after market open</span>
                </div>
            </div>
        );
    }

    const lotSize = strike.lot_size || 65;
    const qty = strike.qty || 1;

    return (
        <div className="fno-instruments-wrap">
            {/* ATM Info Card */}
            <div className="fno-atm-card">
                <div className="fno-atm-header">
                    <Target size={16} />
                    <span>ATM Strike</span>
                    <span className="fno-badge resolved">Resolved</span>
                </div>
                <div className="fno-atm-body">
                    <div className="fno-atm-stat">
                        <span className="fno-atm-label">Strike</span>
                        <span className="fno-atm-value">{strike.atm_strike}</span>
                    </div>
                    <div className="fno-atm-stat">
                        <span className="fno-atm-label">Spot Price</span>
                        <span className="fno-atm-value">{fmtPrice(strike.spot_price)}</span>
                    </div>
                    <div className="fno-atm-stat">
                        <span className="fno-atm-label">Resolved At</span>
                        <span className="fno-atm-value">
                            <Clock size={12} /> {fmtTime(strike.resolved_at)}
                        </span>
                    </div>
                    <div className="fno-atm-stat">
                        <span className="fno-atm-label">Lot Size</span>
                        <span className="fno-atm-value">
                            <Package size={12} /> {lotSize}
                        </span>
                    </div>
                    <div className="fno-atm-stat">
                        <span className="fno-atm-label">Qty (Lots)</span>
                        <span className="fno-atm-value">{qty} × {lotSize} = {qty * lotSize}</span>
                    </div>
                </div>
            </div>

            {/* CE / PE Instrument Cards */}
            <div className="fno-instruments-grid">
                {/* CALL Card */}
                <div className="fno-instrument-card call">
                    <div className="fno-inst-header">
                        <TrendingUp size={16} />
                        <span>CALL (CE)</span>
                    </div>
                    <div className="fno-inst-body">
                        <div className="fno-inst-row">
                            <span className="fno-inst-label">Symbol</span>
                            <span className="fno-inst-value mono">{strike.call.symbol}</span>
                        </div>
                        <div className="fno-inst-row">
                            <span className="fno-inst-label">Token</span>
                            <span className="fno-inst-value mono">{strike.call.token}</span>
                        </div>
                        <div className="fno-inst-row">
                            <span className="fno-inst-label">Live LTP</span>
                            <span className={`fno-inst-value ltp${callLTP > 0 ? ' live' : ''}`}>
                                {callLTP > 0 ? (
                                    <><Activity size={12} /> {fmtPrice(callLTP)}</>
                                ) : (
                                    <span className="fno-na">awaiting tick…</span>
                                )}
                            </span>
                        </div>
                    </div>
                </div>

                {/* PUT Card */}
                <div className="fno-instrument-card put">
                    <div className="fno-inst-header">
                        <TrendingDown size={16} />
                        <span>PUT (PE)</span>
                    </div>
                    <div className="fno-inst-body">
                        <div className="fno-inst-row">
                            <span className="fno-inst-label">Symbol</span>
                            <span className="fno-inst-value mono">{strike.put.symbol}</span>
                        </div>
                        <div className="fno-inst-row">
                            <span className="fno-inst-label">Token</span>
                            <span className="fno-inst-value mono">{strike.put.token}</span>
                        </div>
                        <div className="fno-inst-row">
                            <span className="fno-inst-label">Live LTP</span>
                            <span className={`fno-inst-value ltp${putLTP > 0 ? ' live' : ''}`}>
                                {putLTP > 0 ? (
                                    <><Activity size={12} /> {fmtPrice(putLTP)}</>
                                ) : (
                                    <span className="fno-na">awaiting tick…</span>
                                )}
                            </span>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}
