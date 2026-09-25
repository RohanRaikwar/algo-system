package metrics

import (
	"context"
	"encoding/json"
	"log"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Each process keeps its own Prometheus registry, so the gateway cannot read
// mdengine's or stratengine's counters from memory. Instead each process
// writes a small JSON snapshot to Redis on an interval, with a TTL a few
// intervals long: a missing key means the process is not reporting.
const (
	SnapshotKeyMDEngine    = "metrics:snapshot:mdengine"
	SnapshotKeyIndEngine   = "metrics:snapshot:indengine"
	SnapshotKeyStratEngine = "metrics:snapshot:stratengine"
)

// PipelineSnapshot is mdengine's market-data pipeline state.
type PipelineSnapshot struct {
	TicksTotal           float64 `json:"ticks_total"`
	CandlesTotal         float64 `json:"candles_total"`
	DroppedTicks         float64 `json:"dropped_ticks"`
	LateTicks            float64 `json:"late_ticks"`
	StaleCandlesRejected float64 `json:"stale_candles_rejected"`
	WSReconnects         float64 `json:"ws_reconnects"`
	FeedSeqGaps          float64 `json:"feed_seq_gaps"`
	FeedStaleAlerts      float64 `json:"feed_stale_alerts"`
	CandleLagSec         float64 `json:"candle_lag_sec"`
	WatermarkDelaySec    float64 `json:"watermark_delay_sec"`
	MarketOpen           bool    `json:"market_open"`
	UpdatedAt            string  `json:"updated_at"`
}

// IndicatorSnapshot is indengine's state.
type IndicatorSnapshot struct {
	IndicatorsTotal float64 `json:"indicators_total"`
	UpdatedAt       string  `json:"updated_at"`
}

// OrderSnapshot is stratengine's order-path guard state.
type OrderSnapshot struct {
	CBState    float64 `json:"cb_state"`    // 0=closed, 1=open, 2=half-open
	CBFailures float64 `json:"cb_failures"` // consecutive failures
	RLCount    float64 `json:"rl_count"`    // orders in the current window
	RLMax      float64 `json:"rl_max"`      // allowed orders per window
	UpdatedAt  string  `json:"updated_at"`
}

// Value reads the current value of a Counter or Gauge. It returns 0 for a nil
// collector or one that yields no sample.
func Value(col prometheus.Collector) float64 {
	if col == nil {
		return 0
	}
	ch := make(chan prometheus.Metric, 1)
	col.Collect(ch)
	select {
	case m := <-ch:
		var d dto.Metric
		if err := m.Write(&d); err != nil {
			return 0
		}
		if d.Counter != nil {
			return d.Counter.GetValue()
		}
		if d.Gauge != nil {
			return d.Gauge.GetValue()
		}
	default:
	}
	return 0
}

// PipelineSnapshot reads mdengine's counters. The caller fills
// WatermarkDelaySec, which lives on the aggregator rather than a gauge.
func (m *Metrics) PipelineSnapshot() PipelineSnapshot {
	return PipelineSnapshot{
		TicksTotal:           Value(m.TicksTotal),
		CandlesTotal:         Value(m.CandlesTotal),
		DroppedTicks:         Value(m.DroppedTicks),
		LateTicks:            Value(m.LateTicks),
		StaleCandlesRejected: Value(m.StaleCandlesRejected),
		WSReconnects:         Value(m.WSReconnects),
		FeedSeqGaps:          Value(m.FeedSeqGaps),
		FeedStaleAlerts:      Value(m.FeedStaleAlerts),
		CandleLagSec:         Value(m.CandleLag),
		MarketOpen:           Value(m.MarketState) == 1,
	}
}

// IndicatorSnapshot reads indengine's counters.
func (m *Metrics) IndicatorSnapshot() IndicatorSnapshot {
	return IndicatorSnapshot{IndicatorsTotal: Value(m.IndicatorsTotal)}
}

// OrderSnapshot reads the order executor's breaker and limiter gauges.
func (m *Metrics) OrderSnapshot() OrderSnapshot {
	return OrderSnapshot{
		CBState:    Value(m.OrderCBState),
		CBFailures: Value(m.OrderCBFailures),
		RLCount:    Value(m.OrderRLCount),
		RLMax:      Value(m.OrderRLMax),
	}
}

// SnapshotTTL is how long a published snapshot stays readable.
func SnapshotTTL(interval time.Duration) time.Duration { return 3*interval + time.Second }

// RunSnapshotPublisher writes build() as JSON to key every interval until ctx
// is done. Off the hot path: it only reads counters. Write errors are logged
// once per failure streak and never stop the loop.
func RunSnapshotPublisher(ctx context.Context, rdb *goredis.Client, key string, interval time.Duration, build func() any) {
	if rdb == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		data, err := json.Marshal(build())
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, time.Second)
		err = rdb.Set(cctx, key, data, SnapshotTTL(interval)).Err()
		cancel()
		if err != nil && !failing {
			log.Printf("[metrics] snapshot %s write failed: %v", key, err)
		}
		failing = err != nil
	}
}

// Stamp returns the current time in the snapshot UpdatedAt format.
func Stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
