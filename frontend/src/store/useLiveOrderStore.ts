import { create } from 'zustand';
import type { LiveOrderStatePayload } from '../types/signal';

export function buildLiveOrderKey(strategyName: string, side: string, leg?: string): string {
    const base = `${strategyName}|${side.toUpperCase()}`;
    return leg ? `${base}|${leg}` : base;
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
            next[buildLiveOrderKey(order.strategy_name, order.side, order.leg)] = order;
        }
        return { ordersByKey: next };
    }),
}));
