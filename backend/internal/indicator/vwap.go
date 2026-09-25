package indicator

import (
	"trading-systemv1/internal/model"
)

// VWAP calculates Volume-Weighted Average Price.
// Resets daily — call Reset() at the start of each new trading day.
//
// VWAP = Σ(Price × Volume) / Σ(Volume)
//
// Usage:
//   - CALL filter: price > VWAP indicates buying above fair value (strong demand)
//   - PUT filter: price < VWAP indicates selling below fair value (strong supply)
type VWAP struct {
	cumPriceVolume float64 // Σ(typical_price × volume)
	cumVolume      float64 // Σ(volume)
	current        float64 // current VWAP value (in paise, as float64)
	count          int
}

// NewVWAP creates a new VWAP indicator.
func NewVWAP() *VWAP {
	return &VWAP{}
}

func (v *VWAP) Name() string { return "VWAP" }

func (v *VWAP) Update(candle model.Candle) {
	v.count++

	// Typical price = (H + L + C) / 3
	typicalPrice := float64(candle.High+candle.Low+candle.Close) / 3.0
	vol := float64(candle.Volume)
	if vol == 0 {
		vol = 1 // prevent division by zero; treat as 1 unit
	}

	v.cumPriceVolume += typicalPrice * vol
	v.cumVolume += vol

	if v.cumVolume > 0 {
		v.current = v.cumPriceVolume / v.cumVolume
	}
}

func (v *VWAP) Value() float64 { return v.current }
func (v *VWAP) Ready() bool    { return v.count >= 1 }

// Peek returns current VWAP (no forming-candle estimate).
func (v *VWAP) Peek(closePaise int64) float64 {
	return v.current
}

// Reset clears VWAP for a new trading day.
func (v *VWAP) Reset() {
	v.cumPriceVolume = 0
	v.cumVolume = 0
	v.current = 0
	v.count = 0
}

// Snapshot serializes VWAP state.
func (v *VWAP) Snapshot() IndicatorSnapshot {
	return IndicatorSnapshot{
		Type:    "VWAP",
		Period:  0,
		Current: v.current,
		Count:   v.count,
		Sum:     v.cumPriceVolume,
	}
}

// RestoreFromSnapshot restores VWAP state.
func (v *VWAP) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	v.current = snap.Current
	v.count = snap.Count
	v.cumPriceVolume = snap.Sum
	return nil
}
