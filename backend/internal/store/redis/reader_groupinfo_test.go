package redis

import (
	"context"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// go-redis v8 XInfoGroups cannot parse the Redis 7 reply (extra
// entries-read/lag fields), so offsets came back empty.
func TestGroupLastDeliveredIDs_ReturnsOffsets(t *testing.T) {
	r, err := NewReader(ReaderConfig{Addr: "127.0.0.1:6379", DB: 15, ConsumerGroup: "groupinfo-test", ConsumerName: "c1"})
	if err != nil {
		t.Skipf("no local redis: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := "test:groupinfo:" + time.Now().Format("150405.000000")
	missing := stream + ":missing"
	defer r.client.Del(context.Background(), stream)

	if err := r.EnsureConsumerGroup(ctx, []string{stream}); err != nil {
		t.Fatal(err)
	}
	id, err := r.client.XAdd(ctx, &goredis.XAddArgs{Stream: stream, Values: map[string]any{"data": "x"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group: "groupinfo-test", Consumer: "c1", Streams: []string{stream, ">"}, Count: 1,
	}).Result(); err != nil {
		t.Fatal(err)
	}

	got := r.GroupLastDeliveredIDs(ctx, []string{stream, missing}, "groupinfo-test")
	if got[stream] != id {
		t.Fatalf("offset for %s = %q, want %q (all: %v)", stream, got[stream], id, got)
	}
	if _, ok := got[missing]; ok {
		t.Fatalf("missing stream should be skipped, got %v", got)
	}
	if other := r.GroupLastDeliveredIDs(ctx, []string{stream}, "no-such-group"); len(other) != 0 {
		t.Fatalf("unknown group should yield nothing, got %v", other)
	}
}

func TestParseXInfoGroups_ToleratesShapes(t *testing.T) {
	redis7 := []interface{}{
		[]interface{}{"name", "g1", "consumers", int64(1), "pending", int64(0),
			"last-delivered-id", "5-0", "entries-read", int64(3), "lag", nil},
	}
	redis6 := []interface{}{
		[]interface{}{"name", "g0", "consumers", int64(0), "pending", int64(0), "last-delivered-id", "1-0"},
	}
	resp3 := []interface{}{
		map[interface{}]interface{}{"name": "g2", "last-delivered-id": "9-1", "lag": int64(0)},
	}
	if id := lastDeliveredForGroup(redis7, "g1"); id != "5-0" {
		t.Fatalf("redis7: %q", id)
	}
	if id := lastDeliveredForGroup(redis6, "g0"); id != "1-0" {
		t.Fatalf("redis6: %q", id)
	}
	if id := lastDeliveredForGroup(resp3, "g2"); id != "9-1" {
		t.Fatalf("resp3 map: %q", id)
	}
	if id := lastDeliveredForGroup("garbage", "g1"); id != "" {
		t.Fatalf("garbage: %q", id)
	}
}
