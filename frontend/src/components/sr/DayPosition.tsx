import type { SRView } from '../../types/sr';
import { boxLine, dayPicture } from './srFormat';
import styles from './DayPosition.module.css';

/** "Day: 92% of day range · 8 pts below high · …", whether the day-extreme gate holds an entry, and the sideways box. */
export function DayPosition({ v, price }: { v: SRView; price: number | null }) {
    const d = dayPicture(v, price);
    const box = boxLine(v);
    return (
        <>
            {d && <DayRow d={d} block={v.gate_mode === 'block'} />}
            {box && <div className={`${styles.row} ${box.boxed ? styles.blocked : ''}`}>{box.text}</div>}
        </>
    );
}

function DayRow({ d, block }: { d: NonNullable<ReturnType<typeof dayPicture>>; block: boolean }) {
    return (
        <div className={styles.row} aria-label="Day position">
            <span className={styles.title}>Day</span>
            <span className={styles.bar} aria-hidden>
                <span className={styles.mark} style={{ left: `${Math.min(100, Math.max(0, d.pct))}%` }} />
            </span>
            <span>{d.parts.join(' · ')}</span>
            {d.blocked && (
                <span className={styles.blocked}>
                    {block ? `No ${d.blocked}: at the day's extreme` : `⚠ ${d.blocked} at the day's extreme`}
                </span>
            )}
        </div>
    );
}
