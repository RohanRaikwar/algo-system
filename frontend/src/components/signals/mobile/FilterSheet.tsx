import { BottomSheet } from '../../layout/BottomSheet';
import type { SignalsData } from '../useSignalsData';
import styles from './signalsMobile.module.css';

interface Props {
    open: boolean;
    onClose: () => void;
    data: SignalsData;
    /** Action and token filters apply to the log only. */
    logFilters: boolean;
}

/** Strategy / action / token filters. Changes apply live; chips show what is on. */
export function FilterSheet({ open, onClose, data, logFilters }: Props) {
    return (
        <BottomSheet
            open={open}
            onClose={onClose}
            title="Filters"
            footer={(
                <>
                    <button type="button" className={styles.btnGhost} onClick={data.clearFilters} disabled={!data.filtersActive}>
                        Clear
                    </button>
                    <button type="button" className={styles.btnPrimary} onClick={onClose}>Done</button>
                </>
            )}
        >
            <div className={styles.form}>
                <label className={styles.field}>
                    <span>Strategy</span>
                    <select value={data.stratFilter} onChange={e => data.setStratFilter(e.target.value)}>
                        <option value="ALL">All strategies</option>
                        {data.strategyNames.map(n => <option key={n} value={n}>{n}</option>)}
                    </select>
                </label>
                {logFilters && (
                    <>
                        <label className={styles.field}>
                            <span>Action</span>
                            <select value={data.actionFilter} onChange={e => data.setActionFilter(e.target.value)}>
                                <option value="ALL">All actions</option>
                                <option value="BUY">Entries (BUY)</option>
                                <option value="EXIT">Exits (EXIT)</option>
                                <option value="WATCH">Exit watch (WATCH_*)</option>
                            </select>
                        </label>
                        <label className={styles.field}>
                            <span>Token</span>
                            <input
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
