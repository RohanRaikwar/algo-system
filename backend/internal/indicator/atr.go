package indicator

import (
	"math"
	"trading-systemv1/internal/model"
)

// ATR calculates the Average True Range indicator.
// Uses Wilder's smoothing method (SMMA / RMA) after an initial SMA seed.
//
//	TR = max(High-Low, |High-PrevClose|, |Low-PrevClose|)
//	ATR = SMMA(TR, period)
type ATR struct {
	period    int
	current   float64 // current ATR value (in paise, as float64)
	count     int     // total candles seen
	sum       float64 // accumulator for initial SMA seed
	prevClose float64 // previous candle's close (paise)
}

// NewATR creates a new ATR indicator with the given period.
func NewATR(period int) *ATR {
	return &ATR{period: period}
}

func (a *ATR) Name() string { return "ATR" }

func (a *ATR) Update(candle model.Candle) {
	h := float64(candle.High)
	l := float64(candle.Low)
	c := float64(candle.Close)

	a.count++

	if a.count == 1 {
		// First candle: TR = High - Low (no previous close)
		tr := h - l
		a.sum = tr
		a.prevClose = c
		return
	}

	// True Range
	tr := math.Max(h-l, math.Max(math.Abs(h-a.prevClose), math.Abs(l-a.prevClose)))
	a.prevClose = c

	if a.count <= a.period {
		// Accumulate for initial SMA seed
		a.sum += tr
		if a.count == a.period {
			a.current = a.sum / float64(a.period)
		}
		return
	}

	// Wilder's smoothing: ATR = (ATR_prev * (period-1) + TR) / period
	a.current = (a.current*float64(a.period-1) + tr) / float64(a.period)
}

func (a *ATR) Value() float64 { return a.current }
func (a *ATR) Ready() bool    { return a.count >= a.period }

// Peek is not typically used for ATR but provided for interface compliance.
func (a *ATR) Peek(closePaise int64) float64 {
	return a.current
}

// Reset clears ATR state for reuse.
func (a *ATR) Reset() {
	a.current = 0
	a.count = 0
	a.sum = 0
	a.prevClose = 0
}

// Snapshot serializes ATR state for checkpoint persistence.
func (a *ATR) Snapshot() IndicatorSnapshot {
	return IndicatorSnapshot{
		Type:      "ATR",
		Period:    a.period,
		Current:   a.current,
		Count:     a.count,
		Sum:       a.sum,
		PrevClose: a.prevClose,
	}
}

// RestoreFromSnapshot restores ATR state from a checkpoint.
func (a *ATR) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	a.period = snap.Period
	a.current = snap.Current
	a.count = snap.Count
	a.sum = snap.Sum
	a.prevClose = snap.PrevClose
	return nil
}
