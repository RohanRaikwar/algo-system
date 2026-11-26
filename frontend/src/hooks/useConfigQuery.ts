import { useQuery } from '@tanstack/react-query';
import { fetchConfig, fetchActiveConfig } from '../services/api';
import type { AppConfig, IndicatorEntry } from '../types/api';

/**
 * React Query hook for loading app config on mount.
 * Returns config, loading, and error states.
 */
export function useConfigQuery() {
    return useQuery<AppConfig>({
        queryKey: ['config'],
        queryFn: fetchConfig,
        staleTime: Infinity,   // Config doesn't change during a session
        retry: 2,
        // While the gateway is down (restart, deploy) the app runs on
        // NIFTY-only defaults; keep asking so the real index list replaces
        // them once the gateway answers.
        refetchInterval: (q) => (q.state.status === 'error' ? 5_000 : false),
    });
}

/**
 * React Query hook for loading per-TF active indicator config.
 * Only fetches after config is loaded.
 */
export function useActiveConfigQuery(enabled: boolean) {
    return useQuery<Record<number, IndicatorEntry[]>>({
        queryKey: ['activeConfig'],
        queryFn: fetchActiveConfig,
        staleTime: Infinity,
        enabled,
        retry: 2,
    });
}
