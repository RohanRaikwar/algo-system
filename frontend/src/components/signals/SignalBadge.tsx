import { Badge } from '@/components/ui/badge';
import { useSignalStore } from '../../store/useSignalStore';

/** Small badge showing unread signal count in the header nav. */
export function SignalBadge() {
    const count = useSignalStore(s => s.unreadCount);
    if (count === 0) return null;
    return <Badge variant="count-nav" className="ml-[4px]">{count > 99 ? '99+' : count}</Badge>;
}
