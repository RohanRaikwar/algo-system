package indicator

import (
	"math"
	"trading-systemv1/internal/model"
)

// ADX calculates the Average Directional Index indicator.
// Uses Wilder's smoothing with DI+/DI- for trend strength measurement.
//
// ADX > 20-25 indicates a trending market.
// ADX < 20    indicates a choppy/sideways market.
type ADX struct {
	period int

	// Internal smoothed components
	smoothedDXPlus  float64
	smoothedDXMinus float64
	smoothedTR      float64

	// ADX smoothing
	adxSum   float64
	adxValue float64

	prevCandle model.Candle
	count      int
	dxCount    int // count of DX values accumulated
}

// NewADX creates a new ADX indicator with the given period (typically 14).
func NewADX(period int) *ADX {
	return &ADX{period: period}
}

func (a *ADX) Name() string { return "ADX" }

func (a *ADX) Update(candle model.Candle) {
	h := float64(candle.High)
	l := float64(candle.Low)

	a.count++

	if a.count == 1 {
		// First candle: just save for next iteration
		a.prevCandle = candle
		return
	}

	prevH := float64(a.prevCandle.High)
	prevL := float64(a.prevCandle.Low)
	prevC := float64(a.prevCandle.Close)
	a.prevCandle = candle

	// True Range
	tr := math.Max(h-l, math.Max(math.Abs(h-prevC), math.Abs(l-prevC)))

	// Directional Movement
	upMove := h - prevH
	downMove := prevL - l

	var dmPlus, dmMinus float64
	if upMove > downMove && upMove > 0 {
		dmPlus = upMove
	}
	if downMove > upMove && downMove > 0 {
		dmMinus = downMove
	}

	if a.count <= a.period+1 {
		// Accumulate for initial smoothing (first `period` intervals)
		a.smoothedTR += tr
		a.smoothedDXPlus += dmPlus
		a.smoothedDXMinus += dmMinus

		if a.count == a.period+1 {
			// First DX value
			if a.smoothedTR != 0 {
				diPlus := 100.0 * a.smoothedDXPlus / a.smoothedTR
				diMinus := 100.0 * a.smoothedDXMinus / a.smoothedTR
				diSum := diPlus + diMinus
				if diSum != 0 {
					dx := 100.0 * math.Abs(diPlus-diMinus) / diSum
					a.adxSum += dx
					a.dxCount++
				}
			}
		}
		return
	}

	// Wilder's smoothing for TR, DM+, DM-
	p := float64(a.period)
	a.smoothedTR = a.smoothedTR - (a.smoothedTR / p) + tr
	a.smoothedDXPlus = a.smoothedDXPlus - (a.smoothedDXPlus / p) + dmPlus
	a.smoothedDXMinus = a.smoothedDXMinus - (a.smoothedDXMinus / p) + dmMinus

	if a.smoothedTR == 0 {
		return
	}

	diPlus := 100.0 * a.smoothedDXPlus / a.smoothedTR
	diMinus := 100.0 * a.smoothedDXMinus / a.smoothedTR
	diSum := diPlus + diMinus
	if diSum == 0 {
		return
	}

	dx := 100.0 * math.Abs(diPlus-diMinus) / diSum
	a.dxCount++

	if a.dxCount <= a.period {
		// Accumulate DX for initial ADX SMA seed
		a.adxSum += dx
		if a.dxCount == a.period {
			a.adxValue = a.adxSum / p
		}
		return
	}

	// Wilder's smoothing for ADX
	a.adxValue = (a.adxValue*(p-1) + dx) / p
}

func (a *ADX) Value() float64 { return a.adxValue }
func (a *ADX) Ready() bool    { return a.dxCount >= a.period }

// Peek returns current ADX value (no forming-candle estimate for ADX).
func (a *ADX) Peek(closePaise int64) float64 {
	return a.adxValue
}

// Reset clears ADX state.
func (a *ADX) Reset() {
	a.smoothedDXPlus = 0
	a.smoothedDXMinus = 0
	a.smoothedTR = 0
	a.adxSum = 0
	a.adxValue = 0
	a.count = 0
	a.dxCount = 0
	a.prevCandle = model.Candle{}
}

// Snapshot serializes ADX state.
func (a *ADX) Snapshot() IndicatorSnapshot {
	return IndicatorSnapshot{
		Type:    "ADX",
		Period:  a.period,
		Current: a.adxValue,
		Count:   a.count,
		Sum:     a.adxSum,
	}
}

// RestoreFromSnapshot restores ADX state.
func (a *ADX) RestoreFromSnapshot(snap IndicatorSnapshot) error {
	a.period = snap.Period
	a.adxValue = snap.Current
	a.count = snap.Count
	a.adxSum = snap.Sum
	return nil
}
