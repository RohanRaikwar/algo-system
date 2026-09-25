package agg

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func volTick(sec int64, price, qty, dayVol int64) model.Tick {
	ts := time.Unix(1_700_000_000+sec, 0).UTC()
	return model.Tick{Token: "OPT1", Exchange: "NFO", Price: price, Qty: qty, DayVolume: dayVol, TickTS: ts, EventTS: ts}
}

func drain(ch chan model.Candle) []model.Candle {
	var out []model.Candle
	for {
		select {
		case c := <-ch:
			out = append(out, c)
		default:
			return out
		}
	}
}

// Candle volume comes from the change in cumulative day volume, so trades
// between conflated Quote packets are counted, not just each packet's LTQ.
func TestVolume_FromCumulativeDayVolumeDelta(t *testing.T) {
	a := New()
	ch := make(chan model.Candle, 10)
	a.processTick(volTick(0, 100, 75, 10_000), ch) // baseline: volume before we connected is not ours
	a.processTick(volTick(0, 101, 75, 10_300), ch) // +300 in second 0 (LTQ says only 75)
	a.processTick(volTick(1, 102, 75, 10_450), ch) // +150 in second 1, rolls second 0 over
	a.processTick(volTick(2, 102, 75, 10_450), ch) // rolls second 1 over
	got := drain(ch)
	if len(got) != 2 {
		t.Fatalf("want 2 candles, got %d", len(got))
	}
	if got[0].Volume != 300 || got[1].Volume != 150 {
		t.Fatalf("volumes %d/%d, want 300/150", got[0].Volume, got[1].Volume)
	}
}

// A packet whose cumulative volume is lower (reordered) adds nothing and
// must not lower the baseline, or the next packet would double count.
func TestVolume_LowerCumulativeIgnoredBaselineKept(t *testing.T) {
	a := New()
	ch := make(chan model.Candle, 10)
	a.processTick(volTick(0, 100, 0, 10_000), ch)
	a.processTick(volTick(0, 100, 0, 10_500), ch) // +500
	a.processTick(volTick(0, 100, 0, 10_200), ch) // stale packet: +0
	a.processTick(volTick(0, 100, 0, 10_600), ch) // +100 (vs 10_500, not 10_200)
	a.processTick(volTick(1, 100, 0, 10_600), ch)
	got := drain(ch)
	if len(got) != 1 || got[0].Volume != 600 {
		t.Fatalf("want one candle with volume 600, got %+v", got)
	}
}

// Without a cumulative figure (LTP mode, staging sim) fall back to LTQ.
func TestVolume_FallsBackToLastTradedQtyWithoutDayVolume(t *testing.T) {
	a := New()
	ch := make(chan model.Candle, 10)
	a.processTick(volTick(0, 100, 10, 0), ch)
	a.processTick(volTick(0, 100, 20, 0), ch)
	a.processTick(volTick(1, 100, 5, 0), ch)
	got := drain(ch)
	if len(got) != 1 || got[0].Volume != 30 {
		t.Fatalf("want LTQ sum 30, got %+v", got)
	}
}

// A new session starts a fresh baseline (day volume restarts from 0).
func TestVolume_BaselineResetsOnFlushSession(t *testing.T) {
	a := New()
	ch := make(chan model.Candle, 10)
	a.processTick(volTick(0, 100, 0, 50_000), ch)
	a.processTick(volTick(0, 100, 0, 50_100), ch)
	a.FlushSession(ch)
	drain(ch)
	a.processTick(volTick(100_000, 100, 0, 200), ch) // next day: baseline, not negative
	a.processTick(volTick(100_000, 100, 0, 260), ch) // +60
	a.processTick(volTick(100_001, 100, 0, 260), ch)
	got := drain(ch)
	if len(got) != 1 || got[0].Volume != 60 {
		t.Fatalf("want 60 after session reset, got %+v", got)
	}
}
