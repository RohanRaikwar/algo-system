package gateway

import (
	"math"
	"sort"
	"time"

	"trading-systemv1/internal/model"
)

// candleBeforeReader pages back through one instrument's TF candles.
// sqlite.Reader satisfies it.
type candleBeforeReader interface {
	ReadTFCandlesBefore(exchange, token string, tf int, beforeTS int64, limit int) ([]model.TFCandle, error)
}

// ColdCandleReader pages back through archived TF candles older than the
// local SQLite window. archiver.ColdReader satisfies it.
type ColdCandleReader interface {
	ReadTFCandlesBefore(exchange, token string, tf int, beforeTS int64, limit int) ([]model.TFCandle, error)
}

// mergeTFCandles adds TF candles to out, skipping timestamps already
// present, and returns the latest limit candles in time order.
func mergeTFCandles(out []CandleOut, extra []model.TFCandle, exchange, token string, tf, limit int) []CandleOut {
	seen := make(map[string]bool, len(out))
	for _, c := range out {
		seen[c.TS] = true
	}
	for _, tc := range extra {
		ts := tc.TS.UTC().Format(time.RFC3339)
		if seen[ts] {
			continue
		}
		out = append(out, CandleOut{
			TS: ts, Open: float64(tc.Open), High: float64(tc.High),
			Low: float64(tc.Low), Close: float64(tc.Close),
			Volume: float64(tc.Volume), Count: float64(tc.Count),
			Token: token, Exchange: exchange, TF: tf,
		})
		seen[ts] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// coldBeforeTS is the bound for an archive read: older than every candle
// already found, else older than the request's before, else now.
func coldBeforeTS(candles []CandleOut, before time.Time) int64 {
	if len(candles) > 0 {
		if t, err := time.Parse(time.RFC3339Nano, candles[0].TS); err == nil {
			return t.Unix()
		}
	}
	if !before.IsZero() {
		ts := before.Unix()
		if before.Nanosecond() > 0 {
			ts++
		}
		return ts
	}
	return time.Now().Unix()
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
