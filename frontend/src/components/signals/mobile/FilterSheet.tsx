import { BottomSheet } from '../../layout/BottomSheet';
import type { SignalsData } from '../useSignalsData';

interface Props {
    open: boolean;
    onClose: () => void;
    data: SignalsData;
    /** Action and token filters apply to the log only. */
    logFilters: boolean;
}

const btn = 'flex-1 h-[44px] rounded-ctrl leading-[inherit] font-semibold cursor-pointer active:brightness-[1.15]';
const field = 'flex flex-col gap-[6px] text-sg-muted text-[12px] font-semibold';
const control = 'h-[44px] py-0 px-[12px] border border-solid border-sg-line rounded-ctrl bg-sg-bg text-sg-text leading-[inherit] text-[16px] font-normal';

/** Strategy / action / token filters. Changes apply live; chips show what is on. */
export function FilterSheet({ open, onClose, data, logFilters }: Props) {
    return (
        <BottomSheet
            open={open}
            onClose={onClose}
            title="Filters"
            footer={(
                <>
                    <button
                        type="button"
                        className={`${btn} border border-solid border-sg-line bg-transparent text-sg-text-2 disabled:opacity-[0.45]`}
                        onClick={data.clearFilters}
                        disabled={!data.filtersActive}
                    >
                        Clear
                    </button>
                    <button type="button" className={`${btn} border-0 bg-sg-accent text-white`} onClick={onClose}>Done</button>
                </>
            )}
        >
            <div className="flex flex-col gap-[14px]">
                <label className={field}>
                    <span>Strategy</span>
                    <select className={control} value={data.stratFilter} onChange={e => data.setStratFilter(e.target.value)}>
                        <option value="ALL">All strategies</option>
                        {data.strategyNames.map(n => <option key={n} value={n}>{n}</option>)}
                    </select>
                </label>
                {logFilters && (
                    <>
                        <label className={field}>
                            <span>Action</span>
                            <select className={control} value={data.actionFilter} onChange={e => data.setActionFilter(e.target.value)}>
                                <option value="ALL">All actions</option>
                                <option value="BUY">Entries (BUY)</option>
                                <option value="EXIT">Exits (EXIT)</option>
                                <option value="WATCH">Exit watch (WATCH_*)</option>
                            </select>
                        </label>
                        <label className={field}>
                            <span>Token</span>
                            <input
                                className={control}
                                type="search"
                                inputMode="search"
                                placeholder="e.g. NFO:43521"
                                value={data.tokenFilter}
                                onChange={e => data.setTokenFilter(e.target.value)}
                            />
                        </label>
                    </>
                )}
            </div>
        </BottomSheet>
    );
}
