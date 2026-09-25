package redis

import (
	"context"
	"strconv"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

func cleanupPattern(t *testing.T, w *Writer, pattern string) {
	del := func() {
		ctx := context.Background()
		keys, _ := w.client.Keys(ctx, pattern).Result()
		if len(keys) > 0 {
			w.client.Del(ctx, keys...)
		}
	}
	del()
	t.Cleanup(del)
}

// A TF stream whose top entry has an auto (write-time) ID is renamed aside,
// keeping its consumer groups on the original key, so explicit candle-TS IDs
// take effect; streams already on explicit IDs are left alone; re-running is
// a no-op.
func TestMigrateLegacyTFStreams(t *testing.T) {
	w := liveWriter(t)
	ctx := context.Background()
	cleanupPattern(t, w, "candle:60s:TEST:"+t.Name()+"*")

	legacy := testTFCandle(t, time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC))
	legacy.Token = t.Name() + "-legacy"
	w.client.XAdd(ctx, &goredis.XAddArgs{Stream: legacy.StreamKey(), Values: map[string]interface{}{"data": string(legacy.JSON())}})
	if err := w.client.XGroupCreate(ctx, legacy.StreamKey(), "indengine", "0").Err(); err != nil {
		t.Fatal(err)
	}

	explicit := testTFCandle(t, time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC))
	explicit.Token = t.Name() + "-explicit"
	w.writeTFCandle(ctx, explicit)

	n, err := w.MigrateLegacyTFStreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("migrated %d streams, want 1", n)
	}
	aside, _ := w.client.Keys(ctx, legacy.StreamKey()+":legacy:*").Result()
	if len(aside) != 1 {
		t.Fatalf("legacy copies = %v, want 1", aside)
	}
	if l, _ := w.client.XLen(ctx, aside[0]).Result(); l != 1 {
		t.Fatalf("legacy copy has %d entries, want 1 (data kept)", l)
	}
	groups, err := w.streamGroupNames(ctx, legacy.StreamKey())
	if err != nil || len(groups) != 1 || groups[0] != "indengine" {
		t.Fatalf("consumer groups on migrated key = %+v (%v), want [indengine]", groups, err)
	}
	if l, _ := w.client.XLen(ctx, explicit.StreamKey()).Result(); l != 1 {
		t.Fatalf("explicit-ID stream touched: len %d", l)
	}

	// New writes now get explicit IDs.
	next := legacy
	next.TS = legacy.TS.Add(time.Minute)
	w.writeTFCandle(ctx, next)
	msgs, _ := w.client.XRange(ctx, legacy.StreamKey(), "-", "+").Result()
	if want := strconv.FormatInt(next.TS.UnixMilli(), 10) + "-0"; len(msgs) != 1 || msgs[0].ID != want {
		t.Fatalf("after migration entries = %+v, want one with ID %s", msgs, want)
	}

	if n, err := w.MigrateLegacyTFStreams(ctx); err != nil || n != 0 {
		t.Fatalf("second run migrated %d (%v), want 0", n, err)
	}
}
