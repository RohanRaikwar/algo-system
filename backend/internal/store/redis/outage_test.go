package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/internal/model"
)

// switchableRedis returns a writer whose connections fail while *down is
// true, and a direct client for inspecting the same DB. Skips without a
// local Redis.
func switchableRedis(t *testing.T) (*Writer, *goredis.Client, *atomic.Bool) {
	t.Helper()
	direct := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379", DB: 15})
	if err := direct.Ping(context.Background()).Err(); err != nil {
		t.Skipf("no local redis: %v", err)
	}
	t.Cleanup(func() { direct.Close() })
	down := &atomic.Bool{}
	client := goredis.NewClient(&goredis.Options{
		Addr: "127.0.0.1:6379", DB: 15, MaxRetries: -1,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if down.Load() {
				return nil, errors.New("redis down (test)")
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	})
	t.Cleanup(func() { client.Close() })
	return &Writer{client: client}, direct, down
}

func fastOutageRetry(t *testing.T) {
	old := tfOutageRetryEvery
	tfOutageRetryEvery = 10 * time.Millisecond
	t.Cleanup(func() { tfOutageRetryEvery = old })
}

func outageCandle(token string, i int) model.TFCandle {
	return model.TFCandle{Token: token, Exchange: "NSE", TF: 60,
		TS: time.Unix(1_700_000_000+int64(i)*60, 0).UTC(), Open: int64(i), High: int64(i), Low: int64(i), Close: int64(i)}
}

func streamCloses(t *testing.T, c *goredis.Client, stream string) []int64 {
	t.Helper()
	msgs, err := c.XRange(context.Background(), stream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, m := range msgs {
		var tfc model.TFCandle
		if err := jsonUnmarshalTF(m.Values["data"], &tfc); err != nil {
			t.Fatal(err)
		}
		out = append(out, tfc.Close)
	}
	return out
}

func waitCloses(t *testing.T, c *goredis.Client, stream string, want []int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got []int64
	for time.Now().Before(deadline) {
		got = streamCloses(t, c, stream)
		if fmt.Sprint(got) == fmt.Sprint(want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stream %s closes %v, want %v", stream, got, want)
}

// Finals that arrive while Redis is down are written, in order, once it is
// back — none dropped.
func TestRunTFCandles_BuffersThroughOutage(t *testing.T) {
	fastOutageRetry(t)
	w, direct, down := switchableRedis(t)
	token := fmt.Sprintf("OUT%d", time.Now().UnixNano())
	stream := "candle:60s:NSE:" + token
	t.Cleanup(func() { direct.Del(context.Background(), stream) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan model.TFCandle, 10)
	down.Store(true)
	go w.RunTFCandles(ctx, ch)
	for i := 1; i <= 3; i++ {
		ch <- outageCandle(token, i)
	}
	time.Sleep(100 * time.Millisecond)
	if got := streamCloses(t, direct, stream); len(got) != 0 {
		t.Fatalf("written while down: %v", got)
	}

	down.Store(false)
	waitCloses(t, direct, stream, []int64{1, 2, 3})
	ch <- outageCandle(token, 4)
	waitCloses(t, direct, stream, []int64{1, 2, 3, 4})
}

// On start, finals the stream is missing (e.g. Redis was down while
// mdengine was not running) are restored from history ahead of new ones.
func TestRunTFCandles_RestoresMissingFromHistory(t *testing.T) {
	fastOutageRetry(t)
	w, direct, _ := switchableRedis(t)
	token := fmt.Sprintf("HIST%d", time.Now().UnixNano())
	stream := "candle:60s:NSE:" + token
	t.Cleanup(func() { direct.Del(context.Background(), stream) })

	w.writeTFCandle(context.Background(), outageCandle(token, 1)) // already in Redis
	w.TFHistory = func(context.Context) ([]model.TFCandle, error) {
		return []model.TFCandle{outageCandle(token, 1), outageCandle(token, 2), outageCandle(token, 3), outageCandle(token, 4)}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan model.TFCandle, 10)
	ch <- outageCandle(token, 4) // live copy of the newest, arriving first
	ch <- outageCandle(token, 5)
	go w.RunTFCandles(ctx, ch)

	waitCloses(t, direct, stream, []int64{1, 2, 3, 4, 5})
}

func jsonUnmarshalTF(v any, out *model.TFCandle) error {
	s, _ := v.(string)
	return json.Unmarshal([]byte(s), out)
}
