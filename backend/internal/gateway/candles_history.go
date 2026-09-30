package gateway

import (
	"math"
	"time"

	"trading-systemv1/internal/model"
)

// candleBeforeReader pages back through one instrument's TF candles.
// sqlite.Reader satisfies it.
type candleBeforeReader interface {
	ReadTFCandlesBefore(exchange, token string, tf int, beforeTS int64, limit int) ([]model.TFCandle, error)
}

// readCandlesBefore returns the latest limit candles with ts before `before`
// (all candles when it is zero), oldest first. Readers without the paged
// query fall back to a full read, filtered and trimmed here.
func readCandlesBefore(r model.CandleReader, exchange, token string, tf int, before time.Time, limit int) ([]model.TFCandle, error) {
	beforeTS := int64(math.MaxInt64)
	if !before.IsZero() {
		beforeTS = before.Unix()
		if before.Nanosecond() > 0 {
			beforeTS++ // ts is whole seconds: a candle at floor(before) is still before it
		}
	}
	if br, ok := r.(candleBeforeReader); ok {
		return br.ReadTFCandlesBefore(exchange, token, tf, beforeTS, limit)
	}
	all, err := r.ReadTFCandles(exchange, token, tf, 0)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, c := range all {
		if c.TS.Unix() < beforeTS {
			out = append(out, c)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
