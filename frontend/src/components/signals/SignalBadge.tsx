import { useSignalStore } from '../../store/useSignalStore';

/** Small badge showing unread signal count in the header nav. */
export function SignalBadge() {
    const count = useSignalStore(s => s.unreadCount);
    if (count === 0) return null;
    return <span className="signal-badge">{count > 99 ? '99+' : count}</span>;
}
