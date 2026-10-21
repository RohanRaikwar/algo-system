import { Moon, Sun } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { wakeLockSupported } from '../../services/wakeLock';
import { useWakeStore } from '../../store/useWakeStore';

/** Header pill: keeps the screen on while the app is open (Screen Wake Lock). */
export function KeepAwakeToggle() {
    const { want, held, setWant } = useWakeStore();
    if (!wakeLockSupported()) return null;
    const label = !want
        ? 'Screen can sleep. Tap to keep it on while the app is open'
        : held ? 'Screen stays on while the app is open. Tap to allow sleep' : 'Keep screen on: waiting for the app to be shown';
    return (
        <Button variant="pill" tone={want && held ? 'profit' : 'text-2'} onClick={() => setWant(!want)} title={label} aria-label={label} aria-pressed={want}>
            {want ? <Sun size={14} aria-hidden /> : <Moon size={14} aria-hidden />}
        </Button>
    );
}
