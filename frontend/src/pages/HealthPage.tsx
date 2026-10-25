import { useSearchParams } from 'react-router-dom';
import { SystemHealth } from '../components/health/SystemHealth';
import { PageAppBar } from '../components/layout/PageAppBar';
import { AccountTab } from '../components/signals/AccountTab';
import { SessionHealthTab } from '../components/signals/SessionHealthTab';
import { TradingSettingsTab } from '../components/signals/TradingSettingsTab';
import { TelegramPanel } from '../components/telegram/TelegramPanel';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';
import { haptic } from '../utils/haptic';
import { signalsPage } from '../components/signals/classes';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';

type Section = 'system' | 'account' | 'sessions' | 'telegram' | 'settings';

const SECTIONS: { id: Section; label: string }[] = [
    { id: 'system', label: 'System' },
    { id: 'account', label: 'Account' },
    { id: 'sessions', label: 'Sessions' },
    { id: 'telegram', label: 'Telegram' },
    { id: 'settings', label: 'Settings' },
];

export function HealthPage() {
    const isPhone = useMediaQuery(PHONE_PORTRAIT_QUERY);
    const [params, setParams] = useSearchParams();
    if (!isPhone) return <SystemHealth />;

    // Phone: the system tabs that used to hide off-screen on Signals live here.
    const section = SECTIONS.find(s => s.id === params.get('s'))?.id ?? 'system';
    const pick = (s: Section) => {
        if (s === section) return;
        haptic();
        setParams(s === 'system' ? {} : { s }, { replace: true });
        window.scrollTo({ top: 0 });
    };

    return (
        <>
            <PageAppBar title="Health" />
            <div className="sticky top-[calc(48px_+_env(safe-area-inset-top))] z-50 flex items-center h-[52px] mt-[-12px] mx-[-12px] mb-[8px] py-0 px-[12px] bg-sg-bg">
                <Tabs value={section} onValueChange={v => pick(v as Section)} className="flex-1 min-w-0">
                    <TabsList variant="segment" className="flex-1" aria-label="Health sections">
                        {SECTIONS.map(s => (
                            <TabsTrigger key={s.id} value={s.id}>
                                {s.label}
                            </TabsTrigger>
                        ))}
                    </TabsList>
                </Tabs>
            </div>
            {section === 'system' && <SystemHealth />}
            {section !== 'system' && (
                <div className={signalsPage}>
                    {section === 'account' && <AccountTab />}
                    {section === 'sessions' && <SessionHealthTab />}
                    {section === 'telegram' && <TelegramPanel />}
                    {section === 'settings' && <TradingSettingsTab />}
                </div>
            )}
        </>
    );
}
