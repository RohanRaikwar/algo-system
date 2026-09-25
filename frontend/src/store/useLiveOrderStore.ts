import { create } from 'zustand';
import type { LiveOrderStatePayload } from '../types/signal';

export function buildLiveOrderKey(strategyName: string, side: string): string {
    return `${strategyName}|${side.toUpperCase()}`;
}

interface LiveOrderStateStore {
    ordersByKey: Record<string, LiveOrderStatePayload>;
    setOrders: (orders: LiveOrderStatePayload[]) => void;
}

export const useLiveOrderStore = create<LiveOrderStateStore>((set) => ({
    ordersByKey: {},
    setOrders: (orders) => set(() => {
        const next: Record<string, LiveOrderStatePayload> = {};
        for (const order of orders) {
            if (!order.strategy_name || !order.side) continue;
            next[buildLiveOrderKey(order.strategy_name, order.side)] = order;
        }
        return { ordersByKey: next };
    }),
}));
