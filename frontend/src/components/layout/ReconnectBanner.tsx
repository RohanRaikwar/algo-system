import { useEffect, useState } from 'react';
import { WifiOff } from 'lucide-react';
import { useWSStore } from '../../store/useWSStore';
import { Alert } from '@/components/ui/alert';

export function ReconnectBanner() {
    const connected = useWSStore(s => s.connected);
    const reconnectAttempts = useWSStore(s => s.reconnectAttempts);
    const [visible, setVisible] = useState(false);

    // Show the banner only after being disconnected for 3 seconds.
    // This prevents a brief flash during the initial WS handshake on page load.
    useEffect(() => {
        if (connected || reconnectAttempts === 0) {
            setVisible(false);
            return;
        }
        const timer = setTimeout(() => setVisible(true), 3000);
        return () => clearTimeout(timer);
    }, [connected, reconnectAttempts]);

    if (!visible) return null;

    return (
        <Alert variant="banner" role="alert" className="fixed top-0 left-0 right-0 z-[200]">
            <WifiOff size={16} /> Connection lost — reconnecting (attempt #{reconnectAttempts})…
        </Alert>
    );
}
