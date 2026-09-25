package tfbuilder

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// laggedClock simulates a feed whose 1s candles arrive `lag` after their event time.
type laggedClock struct{ wall time.Time }

func (c *laggedClock) now() time.Time { return c.wall }

func finals(outCh chan model.TFCandle) []model.TFCandle {
	var out []model.TFCandle
	for len(outCh) > 0 {
		if c := <-outCh; !c.Forming {
			out = append(out, c)
		}
	}
	return out
}

// With a feed lag larger than FlushGrace, the TF candle must still include every
// 1s candle of its bucket: finalization follows event time, not the wall clock.
func TestBuilder_FeedLag_FinalCandleIncludesLastSecond(t *testing.T) {
	const lag = 5 * time.Second
	b := New([]int{60})
	clk := &laggedClock{}
	b.now = clk.now
	outCh := make(chan model.TFCandle, 5000)

	base := int64(1700000000)
	base -= base % 60
	for i := int64(0); i <= 60; i++ { // seconds 0..59 of bucket, then first of next
		clk.wall = time.Unix(base+i+1, 0).Add(lag) // candle for second i is complete at i+1
		b.flushExpired(outCh)                      // timer fires just before arrival
		b.process(makeCandle("SBIN", base+i, 500+i, 510+i, 490+i, 505+i, 1), outCh)
	}

	got := finals(outCh)
	if len(got) != 1 {
		t.Fatalf("finalized %d candles, want 1: %+v", len(got), got)
	}
	if got[0].Count != 60 || got[0].Close != 505+59 || got[0].Volume != 60 {
		t.Fatalf("final = count %d close %d vol %d, want 60/%d/60", got[0].Count, got[0].Close, got[0].Volume, 505+59)
	}
}

// An idle feed still finalizes: the timer advances event time by elapsed wall
// time since the last candle, and finalizes at bucket end + grace.
func TestBuilder_IdleFeed_TimerFinalizesAtEventEndPlusGrace(t *testing.T) {
	const lag = 5 * time.Second
	b := New([]int{60})
	clk := &laggedClock{}
	b.now = clk.now
	outCh := make(chan model.TFCandle, 100)

	base := int64(1700000000)
	base -= base % 60
	clk.wall = time.Unix(base+31, 0).Add(lag)
	b.process(makeCandle("SBIN", base+30, 1, 1, 1, 1, 1), outCh)

	// Second k arrives at wall base+k+1+lag, so the event clock reaches
	// base+60+grace at wall base+60+grace+1+lag. One second before: not yet.
	clk.wall = time.Unix(base+60+b.FlushGrace, 0).Add(lag)
	b.flushExpired(outCh)
	if got := finals(outCh); len(got) != 0 {
		t.Fatalf("finalized early: %+v", got)
	}
	clk.wall = time.Unix(base+60+b.FlushGrace+1, 0).Add(lag)
	b.flushExpired(outCh)
	if got := finals(outCh); len(got) != 1 {
		t.Fatalf("finalized %d, want 1 once event clock passes end+grace", len(got))
	}
}

// A 1s candle for a bucket that was already finalized is skipped and counted.
func TestBuilder_LateCandleAfterFinalize_Counted(t *testing.T) {
	b := New([]int{60})
	clk := &laggedClock{}
	b.now = clk.now
	outCh := make(chan model.TFCandle, 100)
	late := 0
	b.OnLateCandle = func() { late++ }

	base := int64(1700000000)
	base -= base % 60
	clk.wall = time.Unix(base+1, 0)
	b.process(makeCandle("A", base, 1, 1, 1, 1, 1), outCh)
	// A's own clock advances with wall time well past its bucket end + grace;
	// the timer finalizes it (another token's candle does not).
	clk.wall = time.Unix(base+66, 0)
	b.process(makeCandle("B", base+65, 1, 1, 1, 1, 1), outCh)
	if got := finals(outCh); len(got) != 0 {
		t.Fatalf("another token's clock finalized A: %+v", got)
	}
	b.flushExpired(outCh)
	if got := finals(outCh); len(got) != 1 || got[0].Token != "A" {
		t.Fatalf("A's bucket not finalized by event time: %+v", got)
	}

	b.process(makeCandle("A", base+59, 2, 2, 2, 2, 1), outCh) // too late
	if late != 1 {
		t.Fatalf("late candles = %d, want 1", late)
	}
	for len(outCh) > 0 {
		if c := <-outCh; c.Token == "A" {
			t.Fatalf("late candle produced output: %+v", c)
		}
	}
}

// A token whose 1s candles run 3s ahead of another's must not finalize the
// lagging token's TF bucket early: the event clock is per token.
func TestBuilder_TokenAheadDoesNotFinalizeLaggingToken(t *testing.T) {
	b := New([]int{60})
	clk := &laggedClock{}
	b.now = clk.now
	outCh := make(chan model.TFCandle, 5000)
	late := 0
	b.OnLateCandle = func() { late++ }

	base := int64(1700000000)
	base -= base % 60
	for i := int64(0); i < 60; i++ {
		clk.wall = time.Unix(base+i+4, 0)
		b.flushExpired(outCh)
		b.process(makeCandle("AHEAD", base+i+3, 1, 1, 1, 1, 1), outCh)
		b.process(makeCandle("LAG", base+i, 1, 1, 1, 1, 1), outCh)
	}

	for _, c := range finals(outCh) {
		if c.Token == "LAG" {
			t.Fatalf("lagging token's bucket finalized early with count %d", c.Count)
		}
	}
	b.FlushSession(outCh)
	for _, c := range finals(outCh) {
		if c.Token == "LAG" && c.Count != 60 {
			t.Fatalf("lagging token final count = %d, want 60", c.Count)
		}
	}
	if late != 0 {
		t.Fatalf("late candles = %d", late)
	}
}
