package indengine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
)

var (
	promOnce sync.Once
	testProm *metrics.Metrics
)

func testService(t *testing.T) *Service {
	t.Helper()
	promOnce.Do(func() { testProm = metrics.NewMetrics() })
	return &Service{
		prom:          testProm,
		engine:        indicator.NewEngine(testConfigs()),
		tfCandleCh:    make(chan model.TFCandle, 100),
		cmds:          make(chan func()),
		loopDone:      make(chan struct{}),
		lastProcessed: make(map[string]string),
	}
}

func testConfigs() []indicator.TFIndicatorConfig {
	return []indicator.TFIndicatorConfig{{TF: 60, Indicators: []indicator.IndicatorConfig{{Type: "SMA", Period: 3}}}}
}

func candle(i int) model.TFCandle {
	return model.TFCandle{
		Token: "99926000", Exchange: "NSE", TF: 60,
		TS:   time.Unix(1_700_000_000+int64(i)*60, 0).UTC(),
		Open: int64(100 + i), High: int64(101 + i), Low: int64(99 + i), Close: int64(100 + i),
		StreamID: fmt.Sprintf("%d-0", 1_700_000_000_000+int64(i)*60_000),
	}
}

// Snapshots and reloads run on the owner while candles stream in. Under
// -race this fails if anything touches the engine off the owner goroutine.
func TestEngineOwner_SnapshotAndReloadWhileProcessing(t *testing.T) {
	svc := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	go svc.processLoop(ctx)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			svc.tfCandleCh <- candle(i)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			var err error
			if oerr := svc.onEngine(ctx, func() { _, err = svc.snapshotLocked() }); oerr != nil || err != nil {
				t.Errorf("snapshot: %v %v", oerr, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := svc.onEngine(ctx, func() { svc.reloadLocked(ctx, testConfigs()) }); err != nil {
				t.Errorf("reload: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	// Drain: the last candle's checkpoint must be recorded.
	want := candle(299).StreamID
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got string
		_ = svc.onEngine(ctx, func() { got = svc.lastProcessed["candle:60s:NSE:99926000"] })
		if got == want || time.Now().After(deadline) {
			if got != want {
				t.Fatalf("checkpoint %q, want %q", got, want)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-svc.loopDone
	if err := svc.onEngine(context.Background(), func() {}); err != errEngineStopped {
		t.Fatalf("onEngine after stop = %v, want errEngineStopped", err)
	}
}

// The checkpoint is the last entry processed, and a redelivered older entry
// (PEL reclaim) never moves it back.
func TestCheckpoint_LastProcessedNeverRegresses(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.apply(ctx, candle(0))
	svc.apply(ctx, candle(1))
	svc.apply(ctx, candle(0)) // redelivered

	snap, err := svc.snapshotLocked()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snap.StreamIDs["candle:60s:NSE:99926000"], candle(1).StreamID; got != want {
		t.Fatalf("checkpoint %q, want %q", got, want)
	}
	if !hasReplayCheckpoint(snap) {
		t.Fatal("snapshot with processed IDs must count as a replay checkpoint")
	}
}

func TestStreamIDLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"", "1-0", true}, {"1-0", "", false}, {"1-0", "1-1", true}, {"2-0", "10-0", true},
		{"10-0", "2-0", false}, {"5-3", "5-3", false},
	} {
		if got := streamIDLess(c.a, c.b); got != c.want {
			t.Errorf("streamIDLess(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
