import { useState, type ReactNode } from 'react';
import { BatteryCharging, Bell, Check, Lock } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { enablePush, sendTestPush } from '../../services/push';
import { markAlertSetupDone } from '../../services/alertSetup';
import { usePushStore } from '../../store/usePushStore';
import { useAlertSetupStore } from '../../store/useAlertSetupStore';

function Step({ n, icon, title, done, children }: { n: number; icon: ReactNode; title: string; done: boolean; children: ReactNode }) {
    return (
        <section className="flex gap-[12px]">
            <span className={cn('flex items-center justify-center shrink-0 size-[28px] rounded-pill text-[0.8rem] font-bold',
                done ? 'bg-sg-profit-soft text-sg-profit' : 'bg-sg-raised text-sg-text-2')} aria-hidden>
                {done ? <Check size={15} /> : n}
            </span>
            <div className="flex flex-col gap-[6px] min-w-0">
                <h3 className="m-0 flex items-center gap-[6px] text-[0.92rem] font-semibold text-sg-text">{icon}{title}</h3>
                {children}
            </div>
        </section>
    );
}

const text = 'm-0 text-[0.84rem] leading-[1.45] text-sg-text-2';

/**
 * Opens at the first start on Android: asks for notification permission,
 * guides the one Chrome setting a web app cannot change itself (battery
 * "Unrestricted", or Doze holds alerts back), then sends a test to try
 * with the phone locked. Reopened from the bell menu.
 */
export function AlertSetupDialog() {
    const open = useAlertSetupStore(s => s.open);
    const setOpen = useAlertSetupStore(s => s.setOpen);
    const push = usePushStore(s => s.state);
    const setPush = usePushStore(s => s.setState);
    const [battery, setBattery] = useState(false);
    const [note, setNote] = useState('');
    const [busy, setBusy] = useState(false);

    const allowed = push === 'on';
    const run = async (fn: () => Promise<void>) => {
        setBusy(true);
        setNote('');
        try { await fn(); } catch (e) { setNote(e instanceof Error ? e.message : 'Failed'); } finally { setBusy(false); }
    };
    const allow = () => run(async () => {
        const s = await enablePush();
        setPush(s);
        if (s === 'denied') setNote('Notifications are blocked: Chrome → ⋮ → Settings → Site settings → Notifications → allow this site.');
        else if (s === 'disabled') setNote('Push is not configured on the server.');
    });
    const test = () => run(async () => {
        const n = await sendTestPush();
        setNote(n > 0 ? 'Test sent. Lock the phone now: it should ring within a minute.' : 'Push service did not accept the test.');
    });
    const close = (finished: boolean) => {
        if (finished) markAlertSetupDone();
        setOpen(false);
    };

    return (
        <Dialog open={open} onOpenChange={o => { if (!o) close(false); }}>
            <DialogContent aria-labelledby="alert-setup-title">
                <DialogHeader>
                    <div>
                        <DialogTitle id="alert-setup-title">Set up trade alerts</DialogTitle>
                        <DialogDescription>So alerts ring even while the phone sleeps.</DialogDescription>
                    </div>
                </DialogHeader>
                <DialogBody className="gap-[18px]">
                    <Step n={1} icon={<Bell size={15} aria-hidden />} title="Allow notifications" done={allowed}>
                        <p className={text}>Entries, exits and refused entries arrive as notifications.</p>
                        {!allowed && <Button variant="default" size="md" className="self-start" disabled={busy} onClick={allow}>Allow notifications</Button>}
                    </Step>
                    <Step n={2} icon={<BatteryCharging size={15} aria-hidden />} title="Let Chrome run in the background" done={battery}>
                        <p className={text}>
                            Android holds back Chrome's alerts while the phone sleeps unless Chrome's battery use is unrestricted.
                            Android only lets you change this yourself:
                        </p>
                        <ol className={cn(text, 'pl-[18px] flex flex-col gap-[2px]')}>
                            <li>Long-press the <b>Chrome</b> icon → <b>App info</b></li>
                            <li><b>Battery</b> (or App battery usage) → <b>Unrestricted</b></li>
                            <li>Samsung: also remove Chrome from <b>Sleeping apps</b> (Settings → Battery → Background usage limits)</li>
                        </ol>
                        {!battery && <Button variant="secondary" size="md" className="self-start" onClick={() => setBattery(true)}>I did this</Button>}
                    </Step>
                    <Step n={3} icon={<Lock size={15} aria-hidden />} title="Test with the phone locked" done={false}>
                        <p className={text}>Send a test, lock the phone, and check it rings.</p>
                        <Button variant="secondary" size="md" className="self-start" disabled={busy || !allowed} onClick={test}>Send test alert</Button>
                    </Step>
                    {note && <p className={cn(text, 'text-sg-text')} role="status">{note}</p>}
                </DialogBody>
                <DialogFooter className="justify-end">
                    <Button variant="bordered" size="md" tone="muted" onClick={() => close(true)}>Skip</Button>
                    <Button variant="default" size="md" disabled={!allowed || !battery} onClick={() => close(true)}>Done</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
