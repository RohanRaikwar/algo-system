package mdengine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"trading-systemv1/internal/marketdata/ws"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

type fakeCounter struct{ n int }

func (f *fakeCounter) Inc() { f.n++ }

type fakeObserver struct{ samples []float64 }

func (f *fakeObserver) Observe(v float64) { f.samples = append(f.samples, v) }

type testCounters struct {
	ticks, wsDrop, aggDrop, formingDrop, tfRedisDrop, tfSQLiteDrop *fakeCounter
	tickPubDrop, failTick, fail1s, failTF, clockSkew               *fakeCounter
	feedStale                                                      *fakeCounter
	e2e, redisDur                                                  *fakeObserver
}

func newTestService(cfg Config) (*Service, *testCounters) {
	tc := &testCounters{
		ticks: &fakeCounter{}, wsDrop: &fakeCounter{}, aggDrop: &fakeCounter{}, formingDrop: &fakeCounter{},
		tfRedisDrop: &fakeCounter{}, tfSQLiteDrop: &fakeCounter{},
		tickPubDrop: &fakeCounter{}, failTick: &fakeCounter{}, fail1s: &fakeCounter{}, failTF: &fakeCounter{},
		clockSkew: &fakeCounter{}, feedStale: &fakeCounter{},
		e2e: &fakeObserver{}, redisDur: &fakeObserver{},
	}
	s := &Service{cfg: cfg}
	s.ctr = pipelineCounters{
		ticksTotal:    tc.ticks,
		e2eLatency:    tc.e2e,
		redisWriteDur: tc.redisDur,
		dropWSTick:    tc.wsDrop,
		dropAggTick:   tc.aggDrop,
		dropTFForming: tc.formingDrop,
		dropTFRedis:   tc.tfRedisDrop,
		dropTFSQLite:  tc.tfSQLiteDrop,
		dropTickPub:   tc.tickPubDrop,
		redisFailTick: tc.failTick,
		redisFail1s:   tc.fail1s,
		redisFailTF:   tc.failTF,
		clockSkew:     tc.clockSkew,
		feedStale:     tc.feedStale,
	}
	return s, tc
}

func TestRouteTick_CountsAggChannelDrop(t *testing.T) {
	s, tc := newTestService(Config{})
	aggTickCh := make(chan model.Tick, 1)

	s.routeTick(model.Tick{Token: "A"}, aggTickCh)
	s.routeTick(model.Tick{Token: "A"}, aggTickCh) // channel full

	if tc.aggDrop.n != 1 {
		t.Fatalf("agg tick drops = %d, want 1", tc.aggDrop.n)
	}
}

func TestRouteTick_CandleTokenFilterIsNotADrop(t *testing.T) {
	s, tc := newTestService(Config{CandleTokens: map[string]bool{"A": true}})
	aggTickCh := make(chan model.Tick, 1)

	s.routeTick(model.Tick{Token: "B"}, aggTickCh)

	if tc.aggDrop.n != 0 {
		t.Fatalf("filtered tick counted as drop: %d", tc.aggDrop.n)
	}
	if len(aggTickCh) != 0 {
		t.Fatal("filtered token reached aggregator")
	}
}

func TestRouteTFCandle_FullQueuesDropAndCount(t *testing.T) {
	s, tc := newTestService(Config{})
	s.redisTFCandleCh = make(chan model.TFCandle) // unbuffered, no reader: always full
	s.sqliteTFCandleCh = make(chan model.TFCandle)
	formingCh := make(chan model.TFCandle)

	s.routeTFCandle(model.TFCandle{Forming: true}, formingCh)
	if tc.formingDrop.n != 1 {
		t.Fatalf("forming drops = %d, want 1", tc.formingDrop.n)
	}

	// A final candle is given up per sink only when that sink's queue is full.
	s.routeTFCandle(model.TFCandle{}, formingCh)
	if tc.tfRedisDrop.n != 1 || tc.tfSQLiteDrop.n != 1 {
		t.Fatalf("final drops on full queues redis=%d sqlite=%d, want 1 each", tc.tfRedisDrop.n, tc.tfSQLiteDrop.n)
	}
}

// A stalled Redis sink must never hold back SQLite: the router does not block
// on either sink, SQLite receives every final candle, and only the Redis
// queue overflow is counted.
func TestRouteTFCandle_StalledRedisDoesNotBlockSQLite(t *testing.T) {
	s, tc := newTestService(Config{})
	s.redisTFCandleCh = make(chan model.TFCandle, 2) // stalled: never drained
	s.sqliteTFCandleCh = make(chan model.TFCandle, 100)
	const n = 50
	done := make(chan struct{})
	go func() {
		base := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
		for i := 0; i < n; i++ {
			s.routeTFCandle(model.TFCandle{Token: "A", TF: 60, TS: base.Add(time.Duration(i) * time.Minute)}, nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("router blocked on the stalled Redis sink; SQLite got %d/%d finals", len(s.sqliteTFCandleCh), n)
	}
	if got := len(s.sqliteTFCandleCh); got != n || tc.tfSQLiteDrop.n != 0 {
		t.Fatalf("SQLite finals=%d drops=%d, want %d/0", got, tc.tfSQLiteDrop.n, n)
	}
	if tc.tfRedisDrop.n != n-2 {
		t.Fatalf("Redis drops = %d, want %d", tc.tfRedisDrop.n, n-2)
	}
}

// Tick publishing is off the router: a full publish queue never blocks and
// keeps the newest tick.
func TestRouteTick_PublishQueueFullKeepsLatest(t *testing.T) {
	s, tc := newTestService(Config{})
	s.tickPubCh = make(chan model.Tick, 1)
	aggTickCh := make(chan model.Tick, 10)

	s.routeTick(model.Tick{Token: "A", Price: 1}, aggTickCh)
	s.routeTick(model.Tick{Token: "A", Price: 2}, aggTickCh)

	if tc.tickPubDrop.n != 1 {
		t.Fatalf("tick publish drops = %d, want 1", tc.tickPubDrop.n)
	}
	if got := <-s.tickPubCh; got.Price != 2 {
		t.Fatalf("queued tick price = %d, want latest (2)", got.Price)
	}
	if len(aggTickCh) != 2 {
		t.Fatalf("aggregator got %d ticks, want 2", len(aggTickCh))
	}
}

type fakeTickPublisher struct {
	batches chan []model.Tick
}

func (f *fakeTickPublisher) PublishTicks(ctx context.Context, ticks []model.Tick) error {
	f.batches <- append([]model.Tick(nil), ticks...)
	return nil
}

func TestRunTickPublisher_BatchesQueuedTicks(t *testing.T) {
	s, _ := newTestService(Config{})
	s.tickPubCh = make(chan model.Tick, 10)
	for i := 1; i <= 3; i++ {
		s.tickPubCh <- model.Tick{Token: "A", Price: int64(i)}
	}
	pub := &fakeTickPublisher{batches: make(chan []model.Tick, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.runTickPublisher(ctx, pub)

	select {
	case b := <-pub.batches:
		if len(b) != 3 || b[0].Price != 1 || b[2].Price != 3 {
			t.Fatalf("batch = %+v, want 3 ticks in order", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no batch published")
	}
}

func TestOnRedisWriteError_UsesPreResolvedCounters(t *testing.T) {
	s, tc := newTestService(Config{})
	s.onRedisWriteError(redisstore.OpTick)
	s.onRedisWriteError(redisstore.OpCandle1s)
	s.onRedisWriteError(redisstore.OpTFCandle)
	s.onRedisWriteError(redisstore.OpTFCandle)
	if tc.failTick.n != 1 || tc.fail1s.n != 1 || tc.failTF.n != 2 {
		t.Fatalf("failures tick=%d 1s=%d tf=%d, want 1/1/2", tc.failTick.n, tc.fail1s.n, tc.failTF.n)
	}
}

func TestFeedLostMidSession(t *testing.T) {
	open := time.Date(2026, 9, 23, 10, 0, 0, 0, markethours.IST) // Wednesday, market hours
	closed := time.Date(2026, 9, 23, 16, 0, 0, 0, markethours.IST)
	lost := fmt.Errorf("%w: auth", ws.ErrFeedLost)

	if !feedLostMidSession(lost, nil, open) {
		t.Error("feed lost during market hours should re-login")
	}
	if feedLostMidSession(lost, nil, closed) {
		t.Error("feed lost after close should take the close path")
	}
	if feedLostMidSession(nil, context.Canceled, open) {
		t.Error("normal session end (close detection) is not a feed loss")
	}
	if feedLostMidSession(lost, context.DeadlineExceeded, open) {
		t.Error("session deadline reached is not a feed loss")
	}
}

// A connect failure in the 9:14 pre-open window (market not yet open) must
// back off and retry, not run the close path and tight-loop re-logins.
func TestFeedLostMidSession_PreOpenFailureIsFeedLoss(t *testing.T) {
	lost := fmt.Errorf("%w: connect: dial timeout", ws.ErrFeedLost)
	preOpen := time.Date(2026, 9, 23, 9, 14, 30, 0, markethours.IST) // Wednesday
	if !feedLostMidSession(lost, nil, preOpen) {
		t.Error("start failure at 9:14:30 on a trading day should be a feed loss (backoff), not market close")
	}
	justBeforeClose := time.Date(2026, 9, 23, 15, 29, 59, 0, markethours.IST)
	if !feedLostMidSession(lost, nil, justBeforeClose) {
		t.Error("start failure before the close should be a feed loss")
	}
	atClose := time.Date(2026, 9, 23, 15, 30, 0, 0, markethours.IST)
	if feedLostMidSession(lost, nil, atClose) {
		t.Error("failure at/after the close should take the close path")
	}
	weekend := time.Date(2026, 9, 26, 9, 14, 30, 0, markethours.IST) // Saturday
	if feedLostMidSession(lost, nil, weekend) {
		t.Error("non-trading day is never a mid-session feed loss")
	}
}

func TestSessionTokenList_IncludesDynamicTokens(t *testing.T) {
	base := []smartconnect.TokenListEntry{{ExchangeType: 1, Tokens: []string{"26000"}}}
	s, _ := newTestService(Config{TokenList: base})
	s.rememberDynamicTokens([]smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"57710"}}})

	got := s.sessionTokenList()
	if len(got) != 2 || got[1].ExchangeType != 2 || got[1].Tokens[0] != "57710" {
		t.Fatalf("session tokens = %+v, want base + dynamic FNO", got)
	}
	if len(s.cfg.TokenList) != 1 {
		t.Fatal("config token list mutated")
	}
}

func TestRecordIngest_TicksAndE2E(t *testing.T) {
	s, tc := newTestService(Config{})

	now := time.Now().UTC()
	s.recordIngest(&model.Tick{TickTS: now, EventTS: now.Add(-20 * time.Millisecond)})
	s.recordIngest(&model.Tick{TickTS: now}) // no exchange time: counted, no latency sample

	if tc.ticks.n != 2 {
		t.Fatalf("TicksTotal = %d, want 2", tc.ticks.n)
	}
	if len(tc.e2e.samples) != 1 {
		t.Fatalf("E2E samples = %d, want 1", len(tc.e2e.samples))
	}
	if got := tc.e2e.samples[0]; got < 0.0199 || got > 0.0201 {
		t.Fatalf("E2E sample = %v, want 0.02", got)
	}
}

func TestNewStagingIngest_RecordsSameMetricsAsProduction(t *testing.T) {
	s, tc := newTestService(Config{SimWSURL: "ws://localhost:9001/ws"})

	ing, err := s.newStagingIngest()
	if err != nil {
		t.Fatal(err)
	}
	if ing.OnIngested == nil || ing.OnDrop == nil {
		t.Fatal("staging ingest hooks not wired")
	}

	now := time.Now().UTC()
	ing.OnIngested(&model.Tick{TickTS: now, EventTS: now.Add(-5 * time.Millisecond)})
	ing.OnDrop()

	if tc.ticks.n != 1 || len(tc.e2e.samples) != 1 {
		t.Fatalf("TicksTotal=%d E2E samples=%d, want 1/1", tc.ticks.n, len(tc.e2e.samples))
	}
	if tc.wsDrop.n != 1 {
		t.Fatalf("ws_tick drops = %d, want 1", tc.wsDrop.n)
	}
}
