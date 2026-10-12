package oms

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

func testRedis(t *testing.T) *goredis.Client {
	t.Helper()
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379", DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("no local redis: %v", err)
	}
	rdb.Del(context.Background(), IntentStream, EventStream)
	t.Cleanup(func() {
		rdb.Del(context.Background(), IntentStream, EventStream)
		rdb.Close()
	})
	return rdb
}

func TestIntentID(t *testing.T) {
	ts := time.Date(2026, 10, 12, 4, 31, 0, 0, time.UTC)
	a := IntentID("S", "CALL", "BUY", "123", ts)
	if a != IntentID("S", "CALL", "BUY", "123", ts) {
		t.Fatal("stamped intent id not deterministic")
	}
	if a == IntentID("S", "CALL", "EXIT", "123", ts) {
		t.Fatal("different action, same id")
	}
	if IntentID("S", "CALL", "BUY", "123", time.Time{}) == IntentID("S", "CALL", "BUY", "123", time.Time{}) {
		t.Fatal("unstamped intent ids repeat")
	}
}

func TestIntentStale(t *testing.T) {
	now := time.Now()
	old := Intent{CreatedTS: now.Add(-time.Minute)}
	if !old.Stale(now, 30*time.Second, false) {
		t.Fatal("old entry not stale")
	}
	if old.Stale(now, 30*time.Second, true) {
		t.Fatal("exit marked stale; exits must never be dropped")
	}
}

// Intents published before the consumer starts — and ones read but never
// acknowledged — are still delivered.
func TestBus_DeliversAndRedeliversUnacked(t *testing.T) {
	rdb := testRedis(t)
	bus := NewBus(rdb)
	ctx := context.Background()
	if err := rdb.XGroupCreateMkStream(ctx, IntentStream, "g", "$").Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"i1", "i2"} {
		if err := bus.PublishIntent(ctx, Intent{ID: id, Strategy: "S", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a crash: read i1 and i2 without acknowledging.
	if err := rdb.XReadGroup(ctx, &goredis.XReadGroupArgs{Group: "g", Consumer: "c", Streams: []string{IntentStream, ">"}, Count: 2}).Err(); err != nil {
		t.Fatal(err)
	}
	_ = bus.PublishIntent(ctx, Intent{ID: "i3", Strategy: "S", Payload: json.RawMessage(`{}`)})

	var mu sync.Mutex
	var got []string
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = bus.ConsumeIntents(cctx, "g", "c", func(in Intent) error {
			mu.Lock()
			got = append(got, in.ID)
			mu.Unlock()
			return nil
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || got[0] != "i1" || got[1] != "i2" || got[2] != "i3" {
		t.Fatalf("delivered %v, want [i1 i2 i3] (pending first, then new)", got)
	}
	pending, err := rdb.XPending(ctx, IntentStream, "g").Result()
	if err != nil {
		t.Fatal(err)
	}
	if pending.Count != 0 {
		t.Fatalf("%d entries left unacknowledged", pending.Count)
	}
}
