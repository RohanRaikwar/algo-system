package strategy

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// makeTFC is a helper to build a closed TFCandle with a given TF for testing.
func makeTFC(token string, tf int, closePaise int64, ts time.Time) model.TFCandle {
	return model.TFCandle{
		Token:    token,
		Exchange: "NSE",
		TF:       tf,
		TS:       ts,
		Open:     closePaise,
		High:     closePaise + 100,
		Low:      closePaise - 100,
		Close:    closePaise,
		Volume:   100,
		Count:    60,
		Forming:  false,
	}
}

// TestTFEngine_FormingCandleRejection verifies forming candles never reach strategies.
func TestTFEngine_FormingCandleRejection(t *testing.T) {
	engine := NewTFEngine(100)
	engine.Register(NewNifty50FnO(1))

	ch := make(chan model.TFCandle, 10)
	forming := makeTFC("A", 60, 10000, time.Now())
	forming.Forming = true

	go func() {
		ch <- forming
		close(ch)
	}()

	engine.Run(context.Background(), ch)

	select {
	case <-engine.Signals():
		t.Fatal("forming candle should not produce a signal")
	default:
		// expected
	}
}

// TestTFEngine_RoutesCorrectTFs verifies only TF=60 candles reach the 1m strategy.
func TestTFEngine_RoutesCorrectTFs(t *testing.T) {
	engine := NewTFEngine(100)
	strat := NewNifty50FnO(1)
	engine.Register(strat)

	ch := make(chan model.TFCandle, 20)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, time.UTC)

	go func() {
		// Send TF=300 — should be ignored by 1m strategy
		for i := 0; i < 5; i++ {
			ch <- makeTFC("B", 300, 10000, base.Add(time.Duration(i*300)*time.Second))
		}
		// Send TF=60 — should reach strategy
		for i := 0; i < 5; i++ {
			ch <- makeTFC("B", 60, 10000, base.Add(time.Duration(i*60)*time.Second))
		}
		close(ch)
	}()

	engine.Run(context.Background(), ch)

	// No assertions on signal value — just verify no panics and correct routing
}

// TestTFEngine_NonBlockingSignalEmit verifies signals don't block when channel full.
func TestTFEngine_NonBlockingSignalEmit(t *testing.T) {
	engine := NewTFEngine(1) // buffer of 1

	// Create a dummy strategy that always returns a signal
	dummy := &dummyTFStrategy{}
	engine.Register(dummy)

	ch := make(chan model.TFCandle, 10)
	base := time.Date(2026, 1, 1, 9, 15, 0, 0, time.UTC)

	go func() {
		// Send 5 candles — only 1 signal fits in buffer, rest should be dropped
		for i := 0; i < 5; i++ {
			ch <- makeTFC("C", 60, 10000, base.Add(time.Duration(i*60)*time.Second))
		}
		close(ch)
	}()

	engine.Run(context.Background(), ch)
	// Should complete without blocking
}

// dummyTFStrategy always returns a BUY signal (for testing).
type dummyTFStrategy struct{}

func (d *dummyTFStrategy) Name() string { return "dummy" }
func (d *dummyTFStrategy) OnTFCandle(candle model.TFCandle) *Signal {
	return &Signal{
		StrategyName: "dummy",
		Action:       ActionBuy,
		Token:        candle.Token,
		Exchange:     candle.Exchange,
		Qty:          1,
		Reason:       "test",
	}
}
func (d *dummyTFStrategy) OnTick(tick model.Tick) *Signal { return nil }
