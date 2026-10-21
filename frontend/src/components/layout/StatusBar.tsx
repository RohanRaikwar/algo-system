import { useEffect, useState } from 'react';
import { Clock, MessageSquare, Timer, Gauge } from 'lucide-react';
import { useWSStore } from '../../store/useWSStore';
import { cn } from '@/lib/utils';

const stat = 'flex items-center gap-[6px] min-w-0 whitespace-nowrap';
const val = 'text-sg-text-2 tabular-nums overflow-hidden text-ellipsis';

interface StatusBarProps {
    inline?: boolean;
}

export function StatusBar({ inline = false }: StatusBarProps) {
    const msgCount = useWSStore(s => s.msgCount);
    const startTime = useWSStore(s => s.startTime);
    const lastUpdateTime = useWSStore(s => s.lastUpdateTime);
    const latency = useWSStore(s => s.latency);
    const [uptime, setUptime] = useState('0s');

    useEffect(() => {
        const timer = setInterval(() => {
            const elapsed = Math.floor((Date.now() - startTime) / 1000);
            const h = Math.floor(elapsed / 3600);
            const m = Math.floor((elapsed % 3600) / 60);
            const s = elapsed % 60;
            const parts: string[] = [];
            if (h > 0) parts.push(h + 'h');
            if (m > 0 || h > 0) parts.push(m + 'm');
            parts.push(s + 's');
            setUptime(parts.join(' '));
        }, 1000);
        return () => clearInterval(timer);
    }, [startTime]);

    return (
        <div className={cn('flex items-center gap-[20px] h-[30px] px-[16px] pb-[env(safe-area-inset-bottom)] text-[0.74rem] text-sg-faint border-t border-t-sg-line bg-sg-surface bottom-0 left-0 right-0 z-[100] shrink-0 lte-640:hidden phone-land:hidden', inline ? 'static' : 'fixed')}>
            <div className={stat}><Clock size={12} /> Last update <span className={val}>{lastUpdateTime || '—'}</span></div>
            <div className={stat}><MessageSquare size={12} /> Messages <span className={val}>{msgCount}</span></div>
            <div className={stat}><Timer size={12} /> Uptime <span className={val}>{uptime}</span></div>
            <div className={stat}><Gauge size={12} /> Latency <span className={val}>{latency !== null ? latency + 'ms' : '—'}</span></div>
        </div>
    );
}
