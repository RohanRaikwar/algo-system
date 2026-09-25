import { describe, expect, it } from 'vitest';
import { normalizeRealtimeEnvelope } from '../useWebSocket';
import type { WSEnvelope } from '../../types/ws';

describe('normalizeRealtimeEnvelope', () => {
    it('maps pub:signal channel envelopes to type=signal', () => {
        const input: WSEnvelope = {
            channel: 'pub:signal',
            data: { strategy_name: 'NIFTY50_FNO', action: 'BUY' },
        };

        const out = normalizeRealtimeEnvelope(input);
        expect(out.type).toBe('signal');
        expect(out.channel).toBe('pub:signal');
    });

    it('maps pub:pnl channel envelopes to type=pnl', () => {
        const input: WSEnvelope = {
            channel: 'pub:pnl',
            data: { realized_pnl: 1234 },
        };

        const out = normalizeRealtimeEnvelope(input);
        expect(out.type).toBe('pnl');
        expect(out.channel).toBe('pub:pnl');
    });

    it('does not override existing type', () => {
        const input: WSEnvelope = {
            type: 'metrics',
            channel: 'pub:pnl',
            metrics: {
                cpu_percent: 0,
                cpu_cores: 1,
                cpu_load_1: 0,
                cpu_load_5: 0,
                cpu_load_15: 0,
                mem_percent: 0,
                mem_used_mb: 0,
                mem_total_mb: 0,
                heap_alloc_mb: 0,
                sys_mb: 0,
                goroutines: 0,
                gc_runs: 0,
                uptime_sec: 0,
            },
        };

        const out = normalizeRealtimeEnvelope(input);
        expect(out.type).toBe('metrics');
    });
});
