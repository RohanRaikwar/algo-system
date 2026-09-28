import { useEffect, useRef } from 'react';
import { sendSubscribe, sendUnsubscribe } from '../../../hooks/useWebSocket';
import { entryKey } from '../../../utils/helpers';
import { acquirePaneSub, isPaneSub, releasePaneSub } from '../../../services/paneSubscriptions';
import { useAppStore } from '../../../store/useAppStore';
import type { IndicatorEntry } from '../../../types/api';

/**
 * Sends SUBSCRIBE messages over WebSocket when TF, token, or active entries change.
 * Includes deduplication to avoid redundant re-subscribes.
 * Sends UNSUBSCRIBE for the previous subscription when switching symbol/TF.
 */
export function useChartSubscription(
    selectedTF: number,
    selectedToken: string | null,
    activeEntries: IndicatorEntry[],
) {
    const prevTFRef = useRef<number>(0);
    const prevTokenRef = useRef<string>('');
    const prevEntriesRef = useRef<string[]>([]);

    useEffect(() => {
        const tf = selectedTF || 60;
        const token = selectedToken || '';

        const tfOrTokenChanged = tf !== prevTFRef.current || token !== prevTokenRef.current;
        const currentKeys = activeEntries.map(e => entryKey(e));
        const prevKeys = new Set(prevEntriesRef.current);
        const entriesChanged = currentKeys.length !== prevEntriesRef.current.length ||
            currentKeys.some(k => !prevKeys.has(k));

        // Unsubscribe from previous subscription if symbol/TF changed
        if (tfOrTokenChanged && prevTokenRef.current && prevTFRef.current > 0 && !isPaneSub(prevTokenRef.current, prevTFRef.current)) {
            sendUnsubscribe(prevTokenRef.current, prevTFRef.current);
        }

        prevTFRef.current = tf;
        prevTokenRef.current = token;
        prevEntriesRef.current = currentKeys;

        // Only re-subscribe if something meaningful changed
        if (!tfOrTokenChanged && !entriesChanged) return;

        if (token) {
            sendSubscribe(token, tf, activeEntries);
        }
    }, [selectedTF, selectedToken, activeEntries]);
}

/**
 * Subscription for an extra dashboard pane: candles only, reference-counted
 * so panes (and the main chart) showing the same symbol:tf share it.
 */
export function usePaneSubscription(token: string | null, tf: number) {
    useEffect(() => {
        if (!token || !tf) return;
        const isMain = () => {
            const s = useAppStore.getState();
            return s.selectedToken === token && (s.selectedTF || 60) === tf;
        };
        if (acquirePaneSub(token, tf) && !isMain()) sendSubscribe(token, tf, []);
        return () => {
            if (releasePaneSub(token, tf) && !isMain()) sendUnsubscribe(token, tf);
        };
    }, [token, tf]);
}
