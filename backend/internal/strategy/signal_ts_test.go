package strategy

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

type stampProbe struct{}

func (stampProbe) Name() string { return "probe" }
func (stampProbe) OnTFCandle(model.TFCandle) *Signal {
	return &Signal{StrategyName: "probe", Action: ActionBuy, Side: SideCall}
}
func (stampProbe) OnTick(model.Tick) *Signal { return nil }

// The engine stamps a signal with its candle's close time, so the same
// candle always yields the same order identity.
func TestTFEngine_StampsSignalTSWithCandleClose(t *testing.T) {
	e := NewTFEngine(4)
	e.Register(stampProbe{})
	ch := make(chan model.TFCandle, 1)
	start := time.Date(2026, 10, 12, 4, 30, 0, 0, time.UTC)
	ch <- model.TFCandle{TS: start, TF: 60}
	close(ch)
	e.Run(context.Background(), ch)
	sig := <-e.Signals()
	if want := start.Add(time.Minute); !sig.SignalTS.Equal(want) {
		t.Fatalf("SignalTS %v, want candle close %v", sig.SignalTS, want)
	}
}
