import type { SRView } from '../../types/sr';
import { boxLine, dayPicture } from './srFormat';
import { cn } from '@/lib/utils';

const row = 'flex flex-wrap items-center gap-x-[10px] gap-y-[2px] mt-[6px] text-[11px] tabular-nums';
const blocked = 'text-sg-warn font-semibold';

/** "Day: 92% of day range · 8 pts below high · …", whether the day-extreme gate holds an entry, and the sideways box. */
export function DayPosition({ v, price }: { v: SRView; price: number | null }) {
    const d = dayPicture(v, price);
    const box = boxLine(v);
    return (
        <>
            {d && <DayRow d={d} block={v.gate_mode === 'block'} />}
            {box && <div className={cn(row, box.boxed ? blocked : 'text-sg-text-2')}>{box.text}</div>}
        </>
    );
}

function DayRow({ d, block }: { d: NonNullable<ReturnType<typeof dayPicture>>; block: boolean }) {
    return (
        <div className={cn(row, 'text-sg-text-2')} aria-label="Day position">
            <span className="text-sg-muted font-semibold">Day</span>
            <span className="relative w-[64px] h-[4px] rounded-pill bg-sg-line-soft" aria-hidden>
                <span className="absolute top-[-2px] w-[2px] h-[8px] ml-[-1px] bg-sg-text" style={{ left: `${Math.min(100, Math.max(0, d.pct))}%` }} />
            </span>
            <span>{d.parts.join(' · ')}</span>
            {d.blocked && (
                <span className={blocked}>
                    {block ? `No ${d.blocked}: at the day's extreme` : `⚠ ${d.blocked} at the day's extreme`}
                </span>
            )}
        </div>
    );
}
