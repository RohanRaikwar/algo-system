package indicator

import (
	"math"
	"trading-systemv1/internal/model"
)

// Bollinger calculates Bollinger Bands (SMA ± K × σ) and provides
// squeeze detection for sideways market identification.
//
// A "squeeze" occurs when bandwidth drops below a threshold, indicating
// consolidation. A "squeeze release" (bandwidth expanding) signals a
// potential breakout opportunity.
type Bollinger struct {
	period     int
	multiplier float64 // K (typically 2.0)

	// Rolling window for SMA + StdDev
	buf   []float64
	idx   int
	count int
	sum   float64

	// Computed values
	middle    float64 // SMA (middle band)
	upper     float64 // SMA + K*σ
	lower     float64 // SMA - K*σ
	bandwidth float64 // (upper - lower) / middle × 100

	// Squeeze tracking
	prevBandwidth float64
}

// NewBollinger creates a new Bollinger Bands indicator.
func NewBollinger(period int, multiplier float64) *Bollinger {
	return &Bollinger{
		period:     period,
		multiplier: multiplier,
		buf:        make([]float64, period),
	}
}

func (b *Bollinger) Name() string { return "BB" }

func (b *Bollinger) Update(candle model.Candle) {
	price := float64(candle.Close)
	b.count++

	// Ring buffer insert
	b.sum -= b.buf[b.idx]
	b.buf[b.idx] = price
	b.sum += price
	b.idx = (b.idx + 1) % b.period

	if b.count < b.period {
		return
	}

	// SMA
	b.middle = b.sum / float64(b.period)

	// Standard deviation
	var variance float64
	for _, v := range b.buf {
		d := v - b.middle
		variance += d * d
	}
	variance /= float64(b.period)
	stdDev := math.Sqrt(variance)

	b.upper = b.middle + b.multiplier*stdDev
	b.lower = b.middle - b.multiplier*stdDev

	// Store prev bandwidth for squeeze detection
	b.prevBandwidth = b.bandwidth

	if b.middle != 0 {
		b.bandwidth = (b.upper - b.lower) / b.middle * 100.0
	}
}

func (b *Bollinger) Value() float64 { return b.middle }
func (b *Bollinger) Ready() bool    { return b.count >= b.period }

// Upper returns the upper Bollinger Band value.
func (b *Bollinger) Upper() float64 { return b.upper }

// Lower returns the lower Bollinger Band value.
func (b *Bollinger) Lower() float64 { return b.lower }

// Bandwidth returns the current bandwidth percentage: (upper-lower)/middle × 100.
func (b *Bollinger) Bandwidth() float64 { return b.bandwidth }

// IsSqueezing returns true when bandwidth is below the given threshold (e.g., 1.0).
func (b *Bollinger) IsSqueezing(threshold float64) bool {
	return b.Ready() && b.bandwidth < threshold
}

// IsSqueezeReleasing returns true when bandwidth is expanding after a squeeze.
func (b *Bollinger) IsSqueezeReleasing(threshold float64) bool {
	return b.Ready() && b.prevBandwidth < threshold && b.bandwidth >= threshold
}

// BandwidthExpanding returns true when bandwidth is increasing.
func (b *Bollinger) BandwidthExpanding() bool {
	return b.Ready() && b.bandwidth > b.prevBandwidth
}

// Peek returns current middle band value (no forming-candle calc for BB).
func (b *Bollinger) Peek(closePaise int64) float64 {
	return b.middle
}

// Reset clears Bollinger state.
func (b *Bollinger) Reset() {
	b.buf = make([]float64, b.period)
	b.idx = 0
	b.count = 0
	b.sum = 0
	b.middle = 0
	b.upper = 0
	b.lower = 0
	b.bandwidth = 0
	b.prevBandwidth = 0
}

// Snapshot serializes Bollinger state.
func (b *Bollinger) Snapshot() IndicatorSnapshot {
	return IndicatorSnapshot{
		Type:    "BB",
		Period:  b.period,
		Current: b.middle,
		Count:   b.count,
		Sum:     b.sum,
	}
}

// RestoreFromSnapshot restores Bollinger state (partial — loses ring buffer).
func (b *Bollinger) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	b.period = snap.Period
	b.middle = snap.Current
	b.count = snap.Count
	b.sum = snap.Sum
	b.buf = make([]float64, b.period)
	return nil
}
