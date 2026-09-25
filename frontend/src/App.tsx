import { useState, useEffect } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { useAppStore } from './store/useAppStore';
import { useWebSocket } from './hooks/useWebSocket';
import { useConfigQuery } from './hooks/useConfigQuery';
import { Header } from './components/layout/Header';
import { StatusBar } from './components/layout/StatusBar';
import { MobileNav } from './components/layout/MobileNav';
import { ReconnectBanner } from './components/layout/ReconnectBanner';
import { SettingsModal } from './components/settings/SettingsModal';
import { DashboardPage } from './pages/DashboardPage';
import { SignalsPage } from './pages/SignalsPage';
import { HealthPage } from './pages/HealthPage';
import styles from './App.module.css';

const queryClient = new QueryClient({
    defaultOptions: { queries: { refetchOnWindowFocus: false } },
});

function AppShell() {
    const setConfig = useAppStore(s => s.setConfig);
    const setSelectedToken = useAppStore(s => s.setSelectedToken);
    const setSelectedTF = useAppStore(s => s.setSelectedTF);
    const [settingsOpen, setSettingsOpen] = useState(false);
    const isDashboard = useLocation().pathname === '/';

    // Connect WebSocket
    useWebSocket();

    // Signal loading is handled by SignalsPage — no global preload needed.

    // Fetch config via React Query
    const { data: cfg, isLoading: cfgLoading, isError: cfgError } = useConfigQuery();

    // Apply config when loaded
    useEffect(() => {
        if (!cfg) return;
        setConfig(cfg);
        if (cfg.tokens.length > 0) setSelectedToken(cfg.tokens[0]);
        if (cfg.tfs.length > 0) setSelectedTF(cfg.tfs[0]);
    }, [cfg, setConfig, setSelectedToken, setSelectedTF]);

    // "/" or "i" opens indicator settings on the dashboard (TradingView convention).
    // Ignored while typing in a field or when a modifier is held.
    useEffect(() => {
        if (!isDashboard) return;
        const onKey = (e: KeyboardEvent) => {
            if (e.ctrlKey || e.metaKey || e.altKey || e.repeat) return;
            if (e.key !== '/' && e.key !== 'i' && e.key !== 'I') return;
            const t = e.target as HTMLElement | null;
            if (t && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName))) return;
            e.preventDefault();
            setSettingsOpen(true);
        };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [isDashboard]);

    // Apply defaults on config fetch error
    useEffect(() => {
        if (cfgError && !cfg) {
            const defaults = { tfs: [60, 300, 900], tokens: ['NSE:99926000'], indicators: ['EMA_6', 'EMA_9', 'EMA_21'] };
            setConfig(defaults);
        }
    }, [cfgError, cfg, setConfig]);

    // Loading state — all hooks must be declared ABOVE this early return
    if (cfgLoading) {
        return (
            <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                height: '100vh', color: 'var(--text-muted)', fontFamily: 'var(--font)',
            }}>
                Loading…
            </div>
        );
    }

    return (
        <div className={`${styles.shell}${isDashboard ? ` ${styles.shellFixed}` : ''}`}>
            <ReconnectBanner />
            <Header />
            <main className={`${styles.main}${isDashboard ? ` ${styles.mainFull}` : ''}`}>
                <Routes>
                    <Route path="/" element={<DashboardPage onOpenIndicators={() => setSettingsOpen(true)} />} />
                    <Route path="/signals" element={<SignalsPage />} />
                    <Route path="/health" element={<HealthPage />} />
                    <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
            </main>
            <StatusBar inline />
            <MobileNav />
            <SettingsModal open={settingsOpen} onClose={() => setSettingsOpen(false)} />
        </div>
    );
}

export default function App() {
    return (
        <QueryClientProvider client={queryClient}>
            <AppShell />
        </QueryClientProvider>
    );
}
