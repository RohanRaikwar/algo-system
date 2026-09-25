//go:build drills

package drills

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
)

// Budget for one hot-path write while Redis is dead or stalled: the writer's
// 200ms deadline (tickPublishTimeout / candleWriteTimeout) plus scheduling slack.
const hotPathBudget = 200*time.Millisecond + 150*time.Millisecond

func drillTick(i int) model.Tick {
	now := time.Now().UTC()
	return model.Tick{Token: "99926000", Exchange: "NSE", Price: 2_500_000 + int64(i%100), TickTS: now, EventTS: now}
}

// publishFor calls PublishTick at ~50Hz for d and returns the slowest call
// and how many calls returned an error.
func publishFor(ctx context.Context, w *redisstore.Writer, d time.Duration) (slowest time.Duration, errs, calls int) {
	end := time.Now().Add(d)
	for i := 0; time.Now().Before(end); i++ {
		start := time.Now()
		if err := w.PublishTick(ctx, drillTick(i)); err != nil {
			errs++
		}
		if el := time.Since(start); el > slowest {
			slowest = el
		}
		calls++
		time.Sleep(20 * time.Millisecond)
	}
	return
}

// Drill R1 — Redis killed (SIGKILL) and stalled (SIGSTOP) mid-stream.
//
// Expected: every PublishTick returns within its 200ms deadline, so the
// mdengine tick router (which calls it inline) cannot back up tickCh; each
// failure is reported through OnWriteError (→ mdengine_redis_write_failures_total{op});
// 1s candle writes fail the same way ({op="candle_1s"}); once Redis is back
// the same client recovers with no restart.
func TestDrill_RedisKillAndStall_HotPathStaysBounded(t *testing.T) {
	rp := startRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := redisstore.New(redisstore.WriterConfig{Addr: rp.addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	fails := newFailCounter()
	w.OnWriteError = fails.hook

	candleCh := make(chan model.Candle, 1024)
	go w.Run(ctx, candleCh)
	feedCandles := func(n int) {
		for i := 0; i < n; i++ {
			candleCh <- model.Candle{Token: "99926000", Exchange: "NSE", TS: time.Now().UTC().Truncate(time.Second),
				Open: 1, High: 1, Low: 1, Close: 1, TicksCount: 1}
		}
	}

	// Healthy baseline.
	if slow, errs, _ := publishFor(ctx, w, 500*time.Millisecond); errs != 0 || slow > hotPathBudget {
		t.Fatalf("baseline: errs=%d slowest=%s", errs, slow)
	}

	// SIGSTOP: sockets open, no replies. Only the client deadline saves us.
	rp.stall()
	feedCandles(3)
	slow, errs, calls := publishFor(ctx, w, 2*time.Second)
	t.Logf("stalled: %d/%d PublishTick calls failed, slowest %s", errs, calls, slow)
	if slow > hotPathBudget {
		t.Fatalf("PublishTick took %s during a Redis stall; budget %s — tick router would back up", slow, hotPathBudget)
	}
	if errs == 0 {
		t.Fatal("no errors while Redis was stalled — deadline not applied?")
	}
	rp.resume()

	// SIGKILL: connection refused, errors are immediate.
	rp.kill()
	feedCandles(3)
	slow, errs, calls = publishFor(ctx, w, time.Second)
	t.Logf("killed: %d/%d PublishTick calls failed, slowest %s", errs, calls, slow)
	if slow > hotPathBudget || errs != calls {
		t.Fatalf("killed: errs=%d/%d slowest=%s", errs, calls, slow)
	}

	got := fails.drain()
	if got[redisstore.OpTick] == 0 || got[redisstore.OpCandle1s] == 0 {
		t.Fatalf("OnWriteError by op = %v; want both %q and %q counted", got, redisstore.OpTick, redisstore.OpCandle1s)
	}

	// Recovery on the same client, no process restart.
	rp.start()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := w.PublishTick(ctx, drillTick(0)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer did not recover within 10s of Redis coming back")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("recovered; failures by op during drill: %v", fails.drain())
}

// TestDrill_RedisRestart_ConsumerGroupRecovers: after a Redis restart
// without AOF/RDB the consumer group is gone and XREADGROUP returns NOGROUP.
// The reader recreates the group at "$" on NOGROUP, so consumption resumes
// without restarting the service. Candles written between the restart and
// the recreation (≤ ~1s) are skipped by design — replaying from the start
// would feed old candles to strategies.
func TestDrill_RedisRestart_ConsumerGroupRecovers(t *testing.T) {
	rp := startRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := redisstore.New(redisstore.WriterConfig{Addr: rp.addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r, err := redisstore.NewReader(redisstore.ReaderConfig{Addr: rp.addr(), ConsumerGroup: "drill", ConsumerName: "drill-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	tf := model.TFCandle{Token: "DRILL", Exchange: "NSE", TF: 60, Open: 1, High: 1, Low: 1, Close: 1, Count: 1}
	stream := tf.StreamKey()
	if err := r.EnsureConsumerGroup(ctx, []string{stream}); err != nil {
		t.Fatal(err)
	}

	tfCh := make(chan model.TFCandle, 16)
	out := make(chan model.TFCandle, 16)
	go w.RunTFCandles(ctx, tfCh)
	go r.ConsumeTFCandles(ctx, []string{stream}, out)

	send := func(minute int) { c := tf; c.TS = time.Unix(int64(minute)*60, 0).UTC(); tfCh <- c }
	send(1)
	select {
	case <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("baseline: TF candle not consumed")
	}

	rp.restart()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for m := 2; ; m++ {
		select {
		case c := <-out:
			t.Logf("consumption resumed after non-persistent Redis restart (first candle %v)", c.TS)
			return
		case <-tick.C:
			send(m)
		case <-deadline:
			t.Fatal("no TF candle consumed after Redis restart — NOGROUP self-heal not working")
		}
	}
}
