import { PageAppBar } from '../components/layout/PageAppBar';
import { TelegramPanel } from '../components/telegram/TelegramPanel';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';
import { signalsPage } from '../components/signals/classes';

/** Telegram calls: service status, LLM reader and every message read. */
export function TelegramPage() {
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    return (
        <>
            {isPhone && <PageAppBar title="Telegram" />}
            <div className={signalsPage}>
                <TelegramPanel />
            </div>
        </>
    );
}
