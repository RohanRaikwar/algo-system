import { describe, expect, it } from 'vitest';
import { tfBucketSec } from '../helpers';

// 2026-10-19 hh:mm IST as Unix seconds.
const at = (h: number, m: number) => Date.UTC(2026, 9, 19, h, m) / 1000 - 19800;

describe('tfBucketSec', () => {
    it('starts 10m, 30m and 1h bars at the 09:15 open', () => {
        expect(tfBucketSec(at(10, 14), 3600)).toBe(at(9, 15));
        expect(tfBucketSec(at(10, 15), 3600)).toBe(at(10, 15));
        expect(tfBucketSec(at(9, 15), 1800)).toBe(at(9, 15));
        expect(tfBucketSec(at(9, 44), 1800)).toBe(at(9, 15));
        expect(tfBucketSec(at(9, 45), 1800)).toBe(at(9, 45));
        expect(tfBucketSec(at(9, 24), 600)).toBe(at(9, 15));
        expect(tfBucketSec(at(9, 25), 600)).toBe(at(9, 25));
    });

    it('keeps clock alignment for the other timeframes (backend model.TFBucket)', () => {
        expect(tfBucketSec(at(9, 17), 300)).toBe(at(9, 15));
        expect(tfBucketSec(at(9, 29), 900)).toBe(at(9, 15));
        expect(tfBucketSec(at(9, 15), 120)).toBe(at(9, 14));
    });
});
