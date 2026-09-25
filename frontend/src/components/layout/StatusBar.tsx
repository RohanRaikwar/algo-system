import { useEffect, useState } from 'react';
import { Clock, MessageSquare, Timer, Gauge } from 'lucide-react';
import { useWSStore } from '../../store/useWSStore';
import styles from './StatusBar.module.css';

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
        <div className={`${styles.bar} ${inline ? styles.inline : ''}`}>
            <div className={styles.stat}><Clock size={12} /> Last update <span className={styles.val}>{lastUpdateTime || '—'}</span></div>
            <div className={styles.stat}><MessageSquare size={12} /> Messages <span className={styles.val}>{msgCount}</span></div>
            <div className={styles.stat}><Timer size={12} /> Uptime <span className={styles.val}>{uptime}</span></div>
            <div className={styles.stat}><Gauge size={12} /> Latency <span className={styles.val}>{latency !== null ? latency + 'ms' : '—'}</span></div>
        </div>
    );
}
