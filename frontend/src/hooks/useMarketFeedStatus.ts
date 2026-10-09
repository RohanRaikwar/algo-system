import { useWSStore } from '../store/useWSStore';
import { useMarketStatusStore, type MarketStatus } from '../store/useMarketStatusStore';

export type Tone = 'up' | 'down' | 'warn' | 'muted';
/** One-dot summary: market state and feed state combined. */
export type DotState = 'live' | 'open' | 'holiday' | 'closed' | 'reconnecting';

export interface MarketFeedStatus {
    open: boolean;
    holiday: boolean;
    marketLabel: string;
    marketTone: Tone;
    nextOpen: string;
    feedLabel: string;
    feedTone: Tone;
    connected: boolean;
    /** Phone pill text: market state, plus feed state while open. */
    compactLabel: string;
    dot: DotState;
    /** Full sentence for aria-label, title and the dot toast. */
    summary: string;
}

export interface MarketFeedInput {
    connected: boolean;
    reconnectAttempts: number;
    marketOpen: boolean;
    status: MarketStatus | null;
    weekend: boolean;
}

export function isWeekendIST(date: Date = new Date()): boolean {
    const weekday = new Intl.DateTimeFormat('en-US', {
        timeZone: 'Asia/Kolkata',
        weekday: 'short',
    }).format(date);
    return weekday === 'Sat' || weekday === 'Sun';
}

export function deriveMarketFeed({ connected, reconnectAttempts, marketOpen, status, weekend }: MarketFeedInput): MarketFeedStatus {
    const open = marketOpen || status?.isOpen === true;
    const holiday = !open && status?.isHoliday === true;
    let marketLabel = 'Market closed';
    let marketTone: Tone = 'muted';
    if (open) {
        marketLabel = 'Market open';
        marketTone = 'up';
    } else if (holiday) {
        marketLabel = status?.holidayName ? `Holiday: ${status.holidayName}` : 'Market holiday';
        marketTone = 'warn';
    } else if (weekend) {
        marketLabel = 'Weekend';
    }
    const nextOpen = !open && status?.nextOpenLabel ? `Opens ${status.nextOpenLabel}` : '';

    const reconnecting = !connected && reconnectAttempts > 0;
    const feedLabel = connected ? 'Live' : reconnecting ? 'Reconnecting' : 'Connecting';
    const feedTone: Tone = connected ? 'up' : reconnecting ? 'down' : 'muted';

    const marketShort = open ? 'Open' : holiday ? 'Holiday' : marketLabel === 'Weekend' ? 'Weekend' : 'Closed';
    const compactLabel = !connected && reconnectAttempts === 0
        ? 'Connecting…'
        : open ? `${marketShort} · ${feedLabel}` : marketShort;

    const dot: DotState = reconnecting ? 'reconnecting'
        : open ? (connected ? 'live' : 'open')
        : holiday ? 'holiday'
        : 'closed';

    const summary = [marketLabel, nextOpen, `Feed ${feedLabel.toLowerCase()}`].filter(Boolean).join(' · ');

    return { open, holiday, marketLabel, marketTone, nextOpen, feedLabel, feedTone, connected, compactLabel, dot, summary };
}

/** Market + price-feed state for headers and the status dot. */
export function useMarketFeedStatus(): MarketFeedStatus {
    const connected = useWSStore(s => s.connected);
    const reconnectAttempts = useWSStore(s => s.reconnectAttempts);
    const marketOpen = useWSStore(s => s.marketOpen);
    const status = useMarketStatusStore(s => s.status);
    return deriveMarketFeed({ connected, reconnectAttempts, marketOpen, status, weekend: isWeekendIST() });
}
