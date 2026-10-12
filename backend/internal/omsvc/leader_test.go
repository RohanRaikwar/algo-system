package omsvc

import (
	"context"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

func TestLeader_SecondInstanceWaitsAndTakeoverStopsHolder(t *testing.T) {
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379", DB: 15})
	defer rdb.Close()
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("no local redis: %v", err)
	}
	const key = "oms:leader:test"
	rdb.Del(ctx, key)
	defer rdb.Del(ctx, key)
	ttl := 300 * time.Millisecond

	if err := acquireLeader(ctx, rdb, key, "A", ttl); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if err := acquireLeader(wctx, rdb, key, "B", ttl); err == nil {
		t.Fatal("B acquired a held lock")
	}

	lost := make(chan struct{})
	hctx, hcancel := context.WithCancel(ctx)
	defer hcancel()
	go holdLeader(hctx, rdb, key, "A", ttl, func() { close(lost) })
	time.Sleep(2 * ttl) // renewals keep A's lock past its TTL
	if v, _ := rdb.Get(ctx, key).Result(); v != "A" {
		t.Fatalf("lock holder %q after renewals, want A", v)
	}
	rdb.Set(ctx, key, "B", ttl) // takeover
	select {
	case <-lost:
	case <-time.After(2 * ttl):
		t.Fatal("holder did not notice the takeover")
	}
}
