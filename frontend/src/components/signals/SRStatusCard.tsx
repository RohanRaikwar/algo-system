import { Activity } from 'lucide-react';
import { useSRStore } from '../../store/useSRStore';
import type { SRView } from '../../types/sr';

const pts = (v?: number) => (v && v > 0 ? (v / 100).toFixed(2) : '—');
const hm = (min: number) => `${String(Math.floor(min / 60)).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`;

function fmtTime(iso?: string): string {
    if (!iso || iso.startsWith('0001')) return '';
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString('en-IN', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: 'Asia/Kolkata' });
}

// Readable names for the strategy's reject reasons (nifty50_sr.go).
const REJECT_LABEL: Record<string, string> = {
    not_ready: 'indicators warming up',
    regime_dead: 'market dead (ATR below floor)',
    regime_mixed: 'regime mixed: breakout-retest only',
    'fade:not_at_level': 'fade: price not at a level',
    'fade:no_pattern': 'fade: no reversal candle',
    'pullback:no_touch': 'pullback: no touch of VWAP / EMA / level',
    'pullback:no_close_back': 'pullback: bar did not close back with trend',
    'pullback:level_in_way': 'pullback: level too close ahead',
    'retest:failed': 'retest: price reclaimed the level',
    'retest:expired': 'retest: no retest in time',
    time_window: 'outside entry window',
    trade_cap: 'daily trade cap reached',
    consec_losses: 'consecutive-loss cap reached',
    day_loss: 'daily loss cap reached',
    cooldown: 'cooldown after exit',
};

export function rejectLabel(reason: string): string {
    if (REJECT_LABEL[reason]) return REJECT_LABEL[reason];
    const side = /^(\w+):sideways$/.exec(reason);
    if (side) return `${side[1]}: market sideways (boxed in)`;
    const ext = /^(\w+):day_extreme$/.exec(reason);
    if (ext) return `${ext[1]}: price at the day's extreme`;
    const m = /^(\w+):(confirmations_(\d)|bad_stop|reward_risk)$/.exec(reason);
    if (m) {
        if (m[3] !== undefined) return `${m[1]}: only ${m[3]} confirmations`;
        return `${m[1]}: ${m[2] === 'bad_stop' ? 'bad stop' : 'reward:risk too low'}`;
    }
    return reason;
}

/** What the strategy is doing right now, in one line. */
export function srStatus(v: SRView): string {
    if (v.side !== 'NONE') return `In ${v.side} (${v.kind ?? ''}) entry ${pts(v.entry)} · stop ${pts(v.stop)} · target ${pts(v.target)}`;
    if (v.regime === 'WARMING') return 'Warming up: regime not known yet';
    if (v.block) return `Blocked: ${rejectLabel(v.block)}`;
    if (v.pending_side) return `Break ${v.pending_side} at ${pts(v.pending_level)}: waiting for retest (${v.pending_left ?? 0} bars left)`;
    if (v.last_reject) return `Waiting: ${rejectLabel(v.last_reject)}`;
    return 'Waiting for a setup';
}

/** Today's reject counts, most frequent first. */
export function topRejects(r: Record<string, number> | null, n = 5): Array<[string, number]> {
    return Object.entries(r ?? {}).sort((a, b) => b[1] - a[1]).slice(0, n);
}

export function SRStatusCard() {
    const v = useSRStore(s => s.view);
    if (!v) return null;
    const levels = (v.levels ?? []).map(l => `${pts(l.price)}×${l.touches}`).join('  ');
    const stats: Array<[string, string]> = [
        ['Regime', `${v.regime} · ADX ${v.adx.toFixed(1)} · RSI ${v.rsi5.toFixed(1)}`],
        ['Close / VWAP', `${pts(v.close)} / ${pts(v.vwap)}`],
        ['EMA 9 / 21', `${pts(v.ema_fast)} / ${pts(v.ema_slow)}`],
        ['Levels', levels || 'none yet'],
        ['Trades today', `${v.trades_today} / ${v.max_trades}${v.cooldown_left > 0 ? ` · cooldown ${v.cooldown_left}m` : ''}`],
        ['Entry window', `${hm(v.entry_from_min)}–${hm(v.entry_to_min)} · ${v.entry_tf}m bars · ${v.min_confirmations}/4 confirms`],
    ];
    const rejects = topRejects(v.rejects);
    return (
        <div className="fno-atm-card">
            <div className="fno-atm-header">
                <Activity size={16} />
                <span>{v.strategy} status</span>
                <span className="fno-badge resolved">{fmtTime(v.ts)}</span>
            </div>
            <div className="fno-inst-body">
                <div className="fno-inst-row"><span className="fno-inst-value">{srStatus(v)}</span></div>
            </div>
            <div className="fno-atm-body">
                {stats.map(([label, value]) => (
                    <div key={label} className="fno-atm-stat">
                        <span className="fno-atm-label">{label}</span>
                        <span className="fno-atm-value fno-param">{value}</span>
                    </div>
                ))}
            </div>
            <div className="fno-inst-body">
                <div className="fno-inst-row">
                    <span className="fno-inst-label">Refused today</span>
                    <span className="fno-inst-value">
                        {rejects.length === 0 ? 'none' : rejects.map(([k, n]) => `${rejectLabel(k)} ×${n}`).join(' · ')}
                    </span>
                </div>
            </div>
        </div>
    );
}
