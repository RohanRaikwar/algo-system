import { useRef, useState } from 'react';
import { Bell, BellOff, BellRing, Send } from 'lucide-react';
import { disablePush, enablePush, sendTestPush, type PushState } from '../../services/push';
import { usePushStore } from '../../store/usePushStore';
import { useAlertSetupStore } from '../../store/useAlertSetupStore';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { Popover, PopoverContent } from '@/components/ui/popover';

/* The bell and Test buttons are Button variant="pill" (font inherited from the header).
   Menu and note open 6px below the bell + Test group, right aligned. */

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
    const openSetup = useAlertSetupStore(s => s.setOpen);

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

    // 'on' opens the menu (DropdownMenuTrigger); other states act on click.
    const onClick = () => {
        if (state === 'on') return;
        if (state === 'off' || state === 'disabled') run(enablePush);
        else setNote(HINT[state]);
    };

    const sendTest = () => run(async () => {
        setMenu(false);
        const n = await sendTestPush();
        setNote(n > 0 ? 'Test sent. Check the notification bar.' : 'Push service did not accept the test');
    });

    const Icon = state === 'on' ? BellRing : state === 'off' ? Bell : BellOff;
    const tone = state === 'on' ? 'profit' : 'text-2';

    const bell = (
        <Button
            variant="pill"
            tone={tone}
            onClick={onClick}
            disabled={busy}
            title={HINT[state]}
            aria-label={HINT[state]}
        >
            <Icon size={14} aria-hidden />
        </Button>
    );

    return (
        <div className="relative flex gap-[6px] shrink-0" ref={ref}>
            {state === 'on' ? (
                <DropdownMenu open={menu} onOpenChange={setMenu}>
                    <DropdownMenuTrigger render={bell} disabled={busy} />
                    <DropdownMenuContent anchor={ref} align="end" sideOffset={6}>
                        <DropdownMenuItem onClick={() => { setMenu(false); openSetup(true); }}>
                            Alert setup (sleeping phone)
                        </DropdownMenuItem>
                        <DropdownMenuItem disabled={busy} onClick={() => run(async () => { setMenu(false); return disablePush(); })}>
                            Turn off alerts
                        </DropdownMenuItem>
                    </DropdownMenuContent>
                </DropdownMenu>
            ) : bell}
            {state === 'on' && (
                <Button
                    variant="pill"
                    tone="text-2"
                    onClick={sendTest}
                    disabled={busy}
                    title="Send a test alert to this device"
                >
                    <Send size={13} aria-hidden /> Test
                </Button>
            )}
            {/* Note: stays until clicked (or Esc); never takes focus. */}
            <Popover open={note !== ''} onOpenChange={(open, d) => { if (!open && d.reason === 'escape-key') setNote(''); }}>
                <PopoverContent
                    anchor={ref}
                    align="end"
                    role="status"
                    initialFocus={false}
                    finalFocus={false}
                    className="cursor-pointer"
                    onClick={() => setNote('')}
                >
                    {note}
                </PopoverContent>
            </Popover>
        </div>
    );
}
