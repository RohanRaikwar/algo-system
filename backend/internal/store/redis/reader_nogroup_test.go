package redis

import (
	"context"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/internal/model"
)

// After a Redis restart without persistence the consumer group is gone and
// XREADGROUP returns NOGROUP forever. The consumer must recreate it.
func TestConsumeTFCandles_RecreatesMissingGroup(t *testing.T) {
	r, err := NewReader(ReaderConfig{Addr: "127.0.0.1:6379", DB: 15, ConsumerGroup: "nogroup-test", ConsumerName: "c1"})
	if err != nil {
		t.Skipf("no local redis: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := "test:nogroup:" + time.Now().Format("150405.000000")
	defer r.client.Del(context.Background(), stream)

	if err := r.EnsureConsumerGroup(ctx, []string{stream}); err != nil {
		t.Fatal(err)
	}
	// Simulate the restart: group vanishes.
	if err := r.client.XGroupDestroy(ctx, stream, "nogroup-test").Err(); err != nil {
		t.Fatal(err)
	}

	out := make(chan model.TFCandle, 1)
	go r.ConsumeTFCandles(ctx, []string{stream}, out)

	// Keep writing until the recreated group picks one up.
	deadline := time.After(8 * time.Second)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-out:
			return
		case <-tick.C:
			r.client.XAdd(ctx, &goredis.XAddArgs{Stream: stream, Values: map[string]any{"data": `{"token":"T"}`}})
		case <-deadline:
			t.Fatal("consumer never recovered from NOGROUP")
		}
	}
}
