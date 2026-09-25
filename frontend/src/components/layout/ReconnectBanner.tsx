import { useEffect, useState } from 'react';
import { WifiOff } from 'lucide-react';
import { useWSStore } from '../../store/useWSStore';

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
        <div
            role="alert"
            style={{
                position: 'fixed',
                top: 0,
                left: 0,
                right: 0,
                zIndex: 200,
                background: 'linear-gradient(90deg, rgba(239, 68, 68, 0.95), rgba(220, 38, 38, 0.95))',
                color: '#fff',
                textAlign: 'center',
                padding: '10px 20px',
                fontSize: '0.85rem',
                fontWeight: 600,
                letterSpacing: '0.3px',
                backdropFilter: 'blur(8px)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: '8px',
            }}
        >
            <WifiOff size={16} /> Connection lost — reconnecting (attempt #{reconnectAttempts})…
        </div>
    );
}
