import { useEffect, useRef, useState } from 'react';
import { Bell, BellOff, BellRing, Send } from 'lucide-react';
import { disablePush, enablePush, sendTestPush, type PushState } from '../../services/push';
import { usePushStore } from '../../store/usePushStore';
import { cn } from '@/lib/utils';

/* Header status pill as a button. Font comes from the parent (font: inherit
   in the old CSS), so the pill's own size/weight do not apply here. */
const pushBtn = 'inline-flex items-center gap-[5px] h-[28px] px-[9px] rounded-pill bg-sg-raised whitespace-nowrap min-w-0 border-none cursor-pointer leading-[inherit] phone-land:h-[26px] hover:enabled:bg-sg-hover focus-visible:outline-none focus-visible:shadow-ring disabled:opacity-60 disabled:cursor-progress';
const popover = 'absolute top-[calc(100%_+_6px)] right-0 z-[50] bg-sg-raised border border-solid border-sg-line rounded-ctrl shadow-[0_8px_24px_rgba(0,0,0,0.35)]';

const HINT: Record<PushState, string> = {
    'unsupported': 'This browser does not support push notifications',
    'ios-install': 'On iPhone: Share → Add to Home Screen, then open the app to enable alerts',
    'denied': 'Notifications are blocked. Allow them in browser site settings',
    'disabled': 'Push is not configured on the server (VAPID keys missing)',
    'off': 'Enable trade alerts on this device',
    'on': 'Trade alerts on. Click for options',
};

/** Header bell: turns Web Push trade alerts on or off for this device. */
export function PushToggle() {
    const state = usePushStore(s => s.state);
    const setState = usePushStore(s => s.setState);
    const [busy, setBusy] = useState(false);
    const [menu, setMenu] = useState(false);
    const [note, setNote] = useState('');
    const ref = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (!menu) return;
        const close = (e: MouseEvent) => {
            if (!ref.current?.contains(e.target as Node)) setMenu(false);
        };
        document.addEventListener('mousedown', close);
        return () => document.removeEventListener('mousedown', close);
    }, [menu]);

    if (state === null || state === 'unsupported') return null;

    const run = async (fn: () => Promise<PushState | void>) => {
        setBusy(true);
        setNote('');
        try {
            const next = await fn();
            if (next) setState(next);
        } catch (err) {
            setNote(err instanceof Error ? err.message : 'Failed');
        } finally {
            setBusy(false);
        }
    };

    const onClick = () => {
        if (state === 'on') setMenu(m => !m);
        else if (state === 'off' || state === 'disabled') run(enablePush);
        else setNote(HINT[state]);
    };

    const sendTest = () => run(async () => {
        setMenu(false);
        const n = await sendTestPush();
        setNote(n > 0 ? 'Test sent. Check the notification bar.' : 'Push service did not accept the test');
    });

    const Icon = state === 'on' ? BellRing : state === 'off' ? Bell : BellOff;
    const tone = state === 'on' ? 'text-sg-profit' : 'text-sg-text-2';

    return (
        <div className="relative flex gap-[6px] shrink-0" ref={ref}>
            <button
                type="button"
                className={cn(pushBtn, tone)}
                onClick={onClick}
                disabled={busy}
                title={HINT[state]}
                aria-label={HINT[state]}
                aria-expanded={state === 'on' ? menu : undefined}
            >
                <Icon size={14} aria-hidden />
            </button>
            {state === 'on' && (
                <button
                    type="button"
                    className={cn(pushBtn, 'text-sg-text-2')}
                    onClick={sendTest}
                    disabled={busy}
                    title="Send a test alert to this device"
                >
                    <Send size={13} aria-hidden /> Test
                </button>
            )}
            {menu && (
                <div className={cn(popover, 'flex flex-col min-w-[170px] p-[4px]')} role="menu">
                    <button type="button" role="menuitem" disabled={busy}
                        className="py-[8px] px-[10px] border-none rounded-[6px] bg-transparent text-sg-text leading-[inherit] text-[0.82rem] text-left cursor-pointer hover:enabled:bg-sg-hover"
                        onClick={() => run(async () => { setMenu(false); return disablePush(); })}>
                        Turn off alerts
                    </button>
                </div>
            )}
            {note && (
                <div className={cn(popover, 'w-max max-w-[min(280px,calc(100vw_-_32px))] py-[8px] px-[10px] text-sg-text-2 text-[0.78rem] leading-[1.4] cursor-pointer')} role="status" onClick={() => setNote('')}>{note}</div>
            )}
        </div>
    );
}
