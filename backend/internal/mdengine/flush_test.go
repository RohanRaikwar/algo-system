package mdengine

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/marketdata/agg"
	"trading-systemv1/internal/marketdata/bus"
	"trading-systemv1/internal/marketdata/tfbuilder"
	"trading-systemv1/internal/model"
)

// newFlushTestPipeline wires aggregator -> fan-out -> TF builder like
// setupPipeline, without Redis/SQLite writers.
func newFlushTestPipeline(ctx context.Context) (*Service, chan model.Tick) {
	s, _ := newTestService(Config{})
	s.candleCh = make(chan model.Candle, 100)
	s.tfCandleCh = make(chan model.TFCandle, 100)
	s.tfFlushReq = make(chan chan struct{})
	s.aggregator = agg.New()
	s.tfBuilder = tfbuilder.New([]int{60})
	s.fanout = bus.New(100)
	in := s.fanout.Subscribe()
	go s.fanout.Run(ctx, s.candleCh)
	go s.runTFBuilderInput(ctx, in, &fakeObserver{})
	ticks := make(chan model.Tick, 100)
	go s.aggregator.Run(ctx, ticks, s.candleCh)
	return s, ticks
}

// The market-close flush must finalize the closing TF candle only after the
// aggregator's last 1s candles reached the TF builder: one final candle that
// includes the tail seconds, and no tail-only forming state afterwards.
func TestFlushSession_FinalTFCandleIncludesTail(t *testing.T) {
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		s, ticks := newFlushTestPipeline(ctx)
		base := time.Date(2026, 9, 23, 9, 59, 0, 0, time.UTC) // 60s bucket start
		for sec := 0; sec < 6; sec++ {
			ts := base.Add(time.Duration(sec) * time.Second)
			ticks <- model.Tick{Token: "A", Exchange: "NSE", Price: int64(100 + sec), Qty: 1, TickTS: ts, EventTS: ts}
		}
		// Seconds 0..4 are finalized by rollover; second 5 is still forming.
		for n := 0; n < 5; n++ {
			select {
			case <-s.tfCandleCh:
			case <-time.After(2 * time.Second):
				t.Fatalf("iter %d: only %d forming TF snapshots before flush", i, n)
			}
		}

		s.flushSession(ctx)

		var finals []model.TFCandle
		var formingAfter int
		deadline := time.After(50 * time.Millisecond)
	collect:
		for {
			select {
			case c := <-s.tfCandleCh:
				if c.Forming {
					if len(finals) > 0 {
						formingAfter++
					}
				} else {
					finals = append(finals, c)
				}
			case <-deadline:
				break collect
			}
		}
		cancel()

		if len(finals) != 1 {
			t.Fatalf("iter %d: %d final TF candles, want 1: %+v", i, len(finals), finals)
		}
		if f := finals[0]; f.Close != 105 || f.Count != 6 {
			t.Fatalf("iter %d: final TF candle close=%d count=%d, want close=105 count=6 (tail second missing)", i, f.Close, f.Count)
		}
		if formingAfter != 0 {
			t.Fatalf("iter %d: %d forming snapshots after the final candle (tail re-opened the bucket)", i, formingAfter)
		}
	}
}
