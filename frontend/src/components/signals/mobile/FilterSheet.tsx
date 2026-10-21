import { BottomSheet } from '../../layout/BottomSheet';
import type { SignalsData } from '../useSignalsData';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select';

interface Props {
    open: boolean;
    onClose: () => void;
    data: SignalsData;
    /** Action and token filters apply to the log only. */
    logFilters: boolean;
}

const field = 'flex flex-col gap-[6px] text-sg-muted text-[12px] font-semibold';

/** Strategy / action / token filters. Changes apply live; chips show what is on. */
export function FilterSheet({ open, onClose, data, logFilters }: Props) {
    return (
        <BottomSheet
            open={open}
            onClose={onClose}
            title="Filters"
            footer={(
                <>
                    <Button variant="bordered" size="sheet" onClick={data.clearFilters} disabled={!data.filtersActive}>
                        Clear
                    </Button>
                    <Button variant="default" size="sheet" onClick={onClose}>Done</Button>
                </>
            )}
        >
            <div className="flex flex-col gap-[14px]">
                <label className={field}>
                    <span>Strategy</span>
                    <NativeSelect variant="sheet" value={data.stratFilter} onChange={e => data.setStratFilter(e.target.value)}>
                        <NativeSelectOption value="ALL">All strategies</NativeSelectOption>
                        {data.strategyNames.map(n => <NativeSelectOption key={n} value={n}>{n}</NativeSelectOption>)}
                    </NativeSelect>
                </label>
                {logFilters && (
                    <>
                        <label className={field}>
                            <span>Action</span>
                            <NativeSelect variant="sheet" value={data.actionFilter} onChange={e => data.setActionFilter(e.target.value)}>
                                <NativeSelectOption value="ALL">All actions</NativeSelectOption>
                                <NativeSelectOption value="BUY">Entries (BUY)</NativeSelectOption>
                                <NativeSelectOption value="EXIT">Exits (EXIT)</NativeSelectOption>
                                <NativeSelectOption value="WATCH">Exit watch (WATCH_*)</NativeSelectOption>
                            </NativeSelect>
                        </label>
                        <label className={field}>
                            <span>Token</span>
                            <Input
                                variant="sheet"
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
