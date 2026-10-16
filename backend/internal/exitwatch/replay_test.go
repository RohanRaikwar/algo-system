package exitwatch

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestPreRejectReplayExitsOnBarRejection(t *testing.T) {
	r := NewPreRejectReplay(DefaultParams())
	r.Book().SetOurSR([]Level{OurSRLevel(2402000, "SWING", 2)})
	at := func(i int) time.Time { return t0.Add(time.Duration(i) * time.Minute) }
	c := func(i int, o, h, l, cl int64) model.TFCandle {
		return model.TFCandle{Token: "99926000", TF: 60, TS: at(i), Open: o * 100, High: h * 100, Low: l * 100, Close: cl * 100}
	}
	// Warm ATR with quiet bars, then enter at 24000 with target +40.
	for i := 0; i < 15; i++ {
		r.OnBar(c(i-15, 23998, 24002, 23996, 24000))
	}
	r.Open("CALL", 2400000, 4000)
	bars := []model.TFCandle{
		c(0, 24000, 24012, 23999, 24011),
		c(1, 24011, 24021, 24010, 24018), // touches 24020
		c(2, 24018, 24019, 24010, 24011), // pulls back 10, closes below 24017
	}
	for i, b := range bars {
		exit, text := r.Step(b)
		if exit != (i == 2) {
			t.Fatalf("bar %d: exit=%v (%s)", i, exit, text)
		}
		if exit && !strings.Contains(text, "24020.00") {
			t.Fatalf("text should name the level: %q", text)
		}
		r.OnBar(b)
	}
}

func TestPreRejectReplayAggregatesHTF(t *testing.T) {
	r := NewPreRejectReplay(DefaultParams())
	start := time.Date(2026, 10, 5, 9, 15, 0, 0, ist)
	for i := 0; i < 40; i++ {
		p := int64(24000 + (i/5%3)*10)
		r.OnBar(model.TFCandle{Token: "99926000", TF: 60, TS: start.Add(time.Duration(i) * time.Minute), Open: p * 100, High: p * 100, Low: p * 100, Close: p * 100})
	}
	if s := r.Book().htf[300]; s == nil || len(s.bars) != 7 {
		t.Fatalf("want 7 closed 5m bars from 40 1m bars, got %+v", s)
	}
}
