package redis

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/internal/model"
)

// stalledRedis accepts connections and reads requests but never replies,
// simulating a Redis server that has stopped responding.
func stalledRedis(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(io.Discard, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func newStalledWriter(t *testing.T) *Writer {
	client := goredis.NewClient(&goredis.Options{
		Addr:        stalledRedis(t),
		ReadTimeout: 5 * time.Second, // longer than the publish budget
		MaxRetries:  -1,
	})
	t.Cleanup(func() { client.Close() })
	return &Writer{client: client}
}

func TestPublishTick_TimesOutOnStalledRedis(t *testing.T) {
	w := newStalledWriter(t)

	start := time.Now()
	err := w.PublishTick(context.Background(), model.Tick{Token: "26000", Exchange: "NSE", Price: 1})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from stalled redis")
	}
	if elapsed > tickPublishTimeout+300*time.Millisecond {
		t.Fatalf("PublishTick blocked %v, budget %v", elapsed, tickPublishTimeout)
	}
}

func TestWriteCandle_TimesOutAndReportsError(t *testing.T) {
	w := newStalledWriter(t)
	var failures atomic.Int32
	w.OnWriteError = func(op string) {
		if op == OpCandle1s {
			failures.Add(1)
		}
	}

	start := time.Now()
	w.writeCandle(context.Background(), model.Candle{Token: "26000", Exchange: "NSE", TS: time.Now()})
	elapsed := time.Since(start)

	if elapsed > candleWriteTimeout+300*time.Millisecond {
		t.Fatalf("writeCandle blocked %v, budget %v", elapsed, candleWriteTimeout)
	}
	if failures.Load() != 1 {
		t.Fatalf("OnWriteError(candle_1s) called %d times, want 1", failures.Load())
	}
}

// liveWriter connects to a local Redis (DB 15) or skips the test.
func liveWriter(t *testing.T) *Writer {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379", DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		t.Skipf("no local redis: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return &Writer{client: client}
}

func testTFCandle(t *testing.T, ts time.Time) model.TFCandle {
	return model.TFCandle{Token: t.Name(), Exchange: "TEST", TF: 60, TS: ts, Open: 1, High: 2, Low: 1, Close: 2, Count: 60}
}

func cleanupStream(t *testing.T, w *Writer, key string) {
	w.client.Del(context.Background(), key)
	t.Cleanup(func() { w.client.Del(context.Background(), key) })
}

// Writing the same final TF candle twice (a retry after an ambiguous timeout)
// must not duplicate it in the stream.
func TestWriteTFCandle_ExplicitIDIsIdempotent(t *testing.T) {
	w := liveWriter(t)
	ts := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	c := testTFCandle(t, ts)
	cleanupStream(t, w, c.StreamKey())

	w.writeTFCandle(context.Background(), c)
	w.writeTFCandle(context.Background(), c)

	msgs, err := w.client.XRange(context.Background(), c.StreamKey(), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("stream has %d entries, want 1", len(msgs))
	}
	if want := strconv.FormatInt(ts.UnixMilli(), 10) + "-0"; msgs[0].ID != want {
		t.Fatalf("stream ID = %s, want %s (derived from candle TS)", msgs[0].ID, want)
	}
}

// Streams whose top ID was auto-generated (wall-clock, pre-upgrade) must still
// accept the next candle instead of treating it as already written.
func TestWriteTFCandle_LegacyAutoIDStreamStillAppends(t *testing.T) {
	w := liveWriter(t)
	ctx := context.Background()
	older := testTFCandle(t, time.Now().Add(-2*time.Minute).Truncate(time.Minute))
	cleanupStream(t, w, older.StreamKey())
	w.client.XAdd(ctx, &goredis.XAddArgs{Stream: older.StreamKey(), Values: map[string]interface{}{"data": string(older.JSON())}}) // auto ID = now

	next := testTFCandle(t, older.TS.Add(time.Minute)) // TS < top auto ID
	w.writeTFCandle(ctx, next)

	n, _ := w.client.XLen(ctx, next.StreamKey()).Result()
	if n != 2 {
		t.Fatalf("stream len = %d, want 2 (new candle appended)", n)
	}
}

// An out-of-order older candle is not appended behind a newer one.
func TestWriteTFCandle_OlderCandleNotAppended(t *testing.T) {
	w := liveWriter(t)
	ctx := context.Background()
	ts := time.Date(2026, 9, 23, 4, 1, 0, 0, time.UTC)
	newer := testTFCandle(t, ts)
	cleanupStream(t, w, newer.StreamKey())
	w.writeTFCandle(ctx, newer)
	w.writeTFCandle(ctx, testTFCandle(t, ts.Add(-time.Minute)))

	n, _ := w.client.XLen(ctx, newer.StreamKey()).Result()
	if n != 1 {
		t.Fatalf("stream len = %d, want 1", n)
	}
}

func TestWriteTFCandle_StalledRedisBoundedAndReported(t *testing.T) {
	w := newStalledWriter(t)
	var failures atomic.Int32
	w.OnWriteError = func(op string) {
		if op == OpTFCandle {
			failures.Add(1)
		}
	}

	start := time.Now()
	w.writeTFCandle(context.Background(), model.TFCandle{Token: "26000", Exchange: "NSE", TF: 60, TS: time.Now()})
	elapsed := time.Since(start)

	budget := time.Duration(tfCandleWriteAttempts)*tfCandleWriteTimeout + time.Second
	if elapsed > budget {
		t.Fatalf("writeTFCandle blocked %v, budget %v", elapsed, budget)
	}
	if failures.Load() != 1 {
		t.Fatalf("OnWriteError(tf_candle) called %d times, want 1 (after retries)", failures.Load())
	}
}

func TestPublishTicks_OnePipelineTimesOut(t *testing.T) {
	w := newStalledWriter(t)
	var failures atomic.Int32
	w.OnWriteError = func(op string) {
		if op == OpTick {
			failures.Add(1)
		}
	}
	ticks := []model.Tick{{Token: "1", Exchange: "NSE"}, {Token: "2", Exchange: "NSE"}}

	start := time.Now()
	err := w.PublishTicks(context.Background(), ticks)
	if err == nil {
		t.Fatal("expected error from stalled redis")
	}
	if elapsed := time.Since(start); elapsed > tickPublishTimeout+300*time.Millisecond {
		t.Fatalf("PublishTicks blocked %v", elapsed)
	}
	if failures.Load() != 1 {
		t.Fatalf("OnWriteError(tick) = %d, want 1 per batch", failures.Load())
	}
}

// A candle whose TS equals the stream top but whose payload differs (a
// tail-only re-finalisation, or a partial after a mid-bucket restart) must not
// overwrite the latest key or be published; it is counted instead.
func TestWriteTFCandle_EqualIDDifferentPayloadKeepsLatest(t *testing.T) {
	w := liveWriter(t)
	ctx := context.Background()
	ts := time.Date(2026, 9, 23, 4, 2, 0, 0, time.UTC)
	full := testTFCandle(t, ts)
	latestKey := "candle:60s:latest:TEST:" + full.Token
	cleanupStream(t, w, full.StreamKey())
	cleanupStream(t, w, latestKey)
	var dups atomic.Int32
	w.OnDuplicateTFCandle = func() { dups.Add(1) }

	w.writeTFCandle(ctx, full)
	partial := full
	partial.Open, partial.High, partial.Low, partial.Close, partial.Count = 5, 5, 5, 5, 3
	w.writeTFCandle(ctx, partial)

	got, err := w.client.Get(ctx, latestKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if got != string(full.JSON()) {
		t.Fatalf("latest overwritten by partial candle: %s", got)
	}
	if dups.Load() != 1 {
		t.Fatalf("duplicate-candle count = %d, want 1", dups.Load())
	}

	// An identical retry is not a conflicting duplicate.
	w.writeTFCandle(ctx, full)
	if dups.Load() != 1 {
		t.Fatalf("identical retry counted as duplicate: %d", dups.Load())
	}
}
