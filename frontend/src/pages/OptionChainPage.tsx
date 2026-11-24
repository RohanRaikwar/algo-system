import { PageAppBar } from '../components/layout/PageAppBar';
import { OptionChainPanel } from '../components/optionchain/OptionChainPanel';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';
import { signalsPage } from '../components/signals/classes';

/** Option chain of every traded index; a click charts one contract. */
export function OptionChainPage() {
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    return (
        <>
            {isPhone && <PageAppBar title="Option chain" />}
            <div className={signalsPage}>
                <OptionChainPanel />
            </div>
        </>
    );
}
