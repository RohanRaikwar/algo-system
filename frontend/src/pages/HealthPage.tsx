import { useSearchParams } from 'react-router-dom';
import { SystemHealth } from '../components/health/SystemHealth';
import { PageAppBar } from '../components/layout/PageAppBar';
import { AccountTab } from '../components/signals/AccountTab';
import { SessionHealthTab } from '../components/signals/SessionHealthTab';
import { TradingSettingsTab } from '../components/signals/TradingSettingsTab';
import { PHONE_PORTRAIT_QUERY, useMediaQuery } from '../hooks/useMediaQuery';
import { haptic } from '../utils/haptic';
import { signalsPage } from '../components/signals/classes';
import { cn } from '@/lib/utils';

type Section = 'system' | 'account' | 'sessions' | 'settings';

const SECTIONS: { id: Section; label: string }[] = [
    { id: 'system', label: 'System' },
    { id: 'account', label: 'Account' },
    { id: 'sessions', label: 'Sessions' },
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
                <div className="flex-1 flex p-[3px] gap-[2px] rounded-[10px] bg-sg-surface border border-solid border-sg-line" role="tablist" aria-label="Health sections">
                    {SECTIONS.map(s => (
                        <button
                            key={s.id}
                            type="button"
                            role="tab"
                            aria-selected={section === s.id}
                            className={cn(
                                'flex-1 h-[34px] min-w-0 py-0 px-[4px] border-0 rounded-[7px] text-[13px] leading-[inherit] cursor-pointer [-webkit-tap-highlight-color:transparent] select-none active:bg-sg-hover',
                                section === s.id
                                    ? 'bg-sg-hover text-sg-text font-semibold shadow-[inset_0_0_0_1px_var(--sg-line)]'
                                    : 'bg-transparent text-sg-muted font-medium',
                            )}
                            onClick={() => pick(s.id)}
                        >
                            {s.label}
                        </button>
                    ))}
                </div>
            </div>
            {section === 'system' && <SystemHealth />}
            {section !== 'system' && (
                <div className={signalsPage}>
                    {section === 'account' && <AccountTab />}
                    {section === 'sessions' && <SessionHealthTab />}
                    {section === 'settings' && <TradingSettingsTab />}
                </div>
            )}
        </>
    );
}
