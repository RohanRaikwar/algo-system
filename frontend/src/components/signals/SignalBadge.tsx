import { useSignalStore } from '../../store/useSignalStore';

/** Small badge showing unread signal count in the header nav. */
export function SignalBadge() {
    const count = useSignalStore(s => s.unreadCount);
    if (count === 0) return null;
    return <span className="inline-flex items-center justify-center min-w-[18px] h-[18px] py-0 px-[5px] ml-[4px] rounded-pill bg-sg-accent text-white text-[0.68rem] font-semibold leading-none tabular-nums">{count > 99 ? '99+' : count}</span>;
}
