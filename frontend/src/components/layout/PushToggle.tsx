import { useEffect, useRef, useState } from 'react';
import { Bell, BellOff, BellRing, Send } from 'lucide-react';
import { disablePush, enablePush, sendTestPush, type PushState } from '../../services/push';
import { usePushStore } from '../../store/usePushStore';
import styles from './Header.module.css';

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
    const tone = state === 'on' ? styles.toneUp : state === 'off' ? '' : styles.toneMuted;

    return (
        <div className={styles.pushWrap} ref={ref}>
            <button
                type="button"
                className={`${styles.pill} ${styles.pushBtn} ${tone}`}
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
                    className={`${styles.pill} ${styles.pushBtn}`}
                    onClick={sendTest}
                    disabled={busy}
                    title="Send a test alert to this device"
                >
                    <Send size={13} aria-hidden /> Test
                </button>
            )}
            {menu && (
                <div className={styles.pushMenu} role="menu">
                    <button type="button" role="menuitem" disabled={busy}
                        onClick={() => run(async () => { setMenu(false); return disablePush(); })}>
                        Turn off alerts
                    </button>
                </div>
            )}
            {note && (
                <div className={styles.pushNote} role="status" onClick={() => setNote('')}>{note}</div>
            )}
        </div>
    );
}
