package stratengine

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

type fakeCandleReader struct {
	gotAfter int64
	candles  []model.TFCandle
}

func (f *fakeCandleReader) ReadTFCandles(exchange, token string, tf int, afterTS int64) ([]model.TFCandle, error) {
	if exchange != "NSE" || token != "99926000" || tf != 60 {
		return nil, nil
	}
	f.gotAfter = afterTS
	return f.candles, nil
}
func (f *fakeCandleReader) ReadAllTFCandles(int, int64) ([]model.TFCandle, error) { return nil, nil }
func (f *fakeCandleReader) Close() error                                          { return nil }

type countingWarmer struct{ got int }

func (c *countingWarmer) Name() string { return "W" }
func (c *countingWarmer) Warmup(cs []model.TFCandle) int {
	c.got = len(cs)
	return len(cs)
}

func TestWarmRangeReadsNiftyHistory(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	r := &fakeCandleReader{candles: make([]model.TFCandle, 7)}
	w1, w2 := &countingWarmer{}, &countingWarmer{}
	warmRange(r, []rangeWarmer{w1, w2}, 3, now)
	if r.gotAfter != now.AddDate(0, 0, -3).Unix() {
		t.Errorf("afterTS = %d", r.gotAfter)
	}
	if w1.got != 7 || w2.got != 7 {
		t.Errorf("warmers got %d/%d candles", w1.got, w2.got)
	}
}
