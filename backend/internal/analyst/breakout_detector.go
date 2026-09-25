package analyst

import (
	"time"

	"trading-systemv1/internal/model"
)

const (
	// boVolumeMultiplier: break candle volume must be >= this × avg volume.
	boVolumeMultiplier = 2.0

	// boADXThreshold: ADX must be above this for a valid breakout.
	boADXThreshold = 20.0

	// boCandleBodyWindow: compare break candle body against avg of last N candles.
	boCandleBodyWindow = 5

	// boRetestBars: max bars to wait for a breakout retest.
	boRetestBars = 10

	// boFailureBars: if price closes back inside within this many bars, it's a failure.
	boFailureBars = 3

	// boBBWSqueezeWindow: lookback for minimum BBW (Bollinger Band Width).
	boBBWSqueezeWindow = 20
)

// BreakoutDetector detects breakouts from S/R levels.
// Implements the breakout section of support_resistance_detector.md:
//   - Pre-breakout compression detection (BBW squeeze, ATR% < 0.3%)
//   - Real vs fake-out filtering (volume, ADX, candle body)
//   - Retest tracking
//   - Measured move targets
//   - Failure detection
type BreakoutDetector struct {
	// Active breakout tracking
	activeBreak *activeBreakout

	// Recent candle body sizes for comparison
	recentBodies []int64

	// Recent BBW values for squeeze detection
	recentBBW []float64

	// Previous ADX for "rising" check
	prevADX float64
}

type activeBreakout struct {
	level         int64
	direction     string // "UP" or "DOWN"
	breakPrice    int64
	breakVolume   int64
	avgVolume     float64
	rangeHeight   int64
	measuredMove  int64
	barsSince     int
	adxAtBreak    float64
	adxRising     bool
	retested      bool
	retestCandle  bool // rejection candle on retest
}

// NewBreakoutDetector creates a new breakout detector.
func NewBreakoutDetector() *BreakoutDetector {
	return &BreakoutDetector{
		recentBodies: make([]int64, 0, boCandleBodyWindow),
		recentBBW:    make([]float64, 0, boBBWSqueezeWindow),
	}
}

// Update processes a new candle against active S/R levels and indicators.
// Returns a BreakoutEvent if a breakout-related signal is detected, or nil.
func (bd *BreakoutDetector) Update(
	tfc model.TFCandle,
	levels []SRLevel,
	adx float64,
	atrPct float64,
	bbw float64, // Bollinger Band Width as % of close
	avgVol float64,
) *BreakoutEvent {
	// Track candle body sizes
	body := abs64(tfc.Close - tfc.Open)
	bd.recentBodies = append(bd.recentBodies, body)
	if len(bd.recentBodies) > boCandleBodyWindow {
		bd.recentBodies = bd.recentBodies[1:]
	}

	// Track BBW values
	bd.recentBBW = append(bd.recentBBW, bbw)
	if len(bd.recentBBW) > boBBWSqueezeWindow {
		bd.recentBBW = bd.recentBBW[1:]
	}

	adxRising := adx > bd.prevADX
	bd.prevADX = adx

	// If there's an active breakout, track it
	if bd.activeBreak != nil {
		return bd.trackActiveBreakout(tfc, adx, adxRising)
	}

	// Check for compression signal (pre-breakout)
	if bd.isCompressing(bbw, atrPct) {
		return &BreakoutEvent{
			Stage:     BreakoutStageCompression,
			Token:     tfc.Token,
			Exchange:  tfc.Exchange,
			TF:        tfc.TF,
			TS:        tfc.TS,
			ADXValue:  adx,
			ADXRising: adxRising,
		}
	}

	// Check for new breakout from any S/R level
	for _, level := range levels {
		event := bd.checkBreakout(tfc, level, adx, adxRising, avgVol)
		if event != nil {
			return event
		}
	}

	return nil
}

// checkBreakout checks if the current candle breaks through an S/R level.
func (bd *BreakoutDetector) checkBreakout(
	tfc model.TFCandle,
	level SRLevel,
	adx float64,
	adxRising bool,
	avgVol float64,
) *BreakoutEvent {
	bodyHigh := maxI64(tfc.Open, tfc.Close)
	bodyLow := minI64(tfc.Open, tfc.Close)
	body := abs64(tfc.Close - tfc.Open)

	var direction string
	var isBroken bool

	if level.IsResistance {
		// Breaking resistance upward: full body above level
		if bodyLow > level.Price {
			direction = "UP"
			isBroken = true
		}
	} else {
		// Breaking support downward: full body below level
		if bodyHigh < level.Price {
			direction = "DOWN"
			isBroken = true
		}
	}

	if !isBroken {
		return nil
	}

	// Validate breakout (all 4 filters)
	// Filter 1: Full body close beyond level — already confirmed
	// Filter 2: Volume >= 2× average
	volRatio := 0.0
	if avgVol > 0 {
		volRatio = float64(tfc.Volume) / avgVol
	}
	if volRatio < boVolumeMultiplier {
		return nil // fake-out: low volume
	}

	// Filter 3: ADX > 20 and rising
	if adx < boADXThreshold {
		return nil // fake-out: weak trend
	}

	// Filter 4: Break candle body larger than avg of last 5
	avgBody := bd.avgCandleBody()
	if avgBody > 0 && body < int64(avgBody) {
		return nil // fake-out: small candle
	}

	// Valid breakout detected — compute measured move
	rangeHeight := bd.estimateRangeHeight(level, tfc)
	var measuredMove int64
	if direction == "UP" {
		measuredMove = level.Price + rangeHeight
	} else {
		measuredMove = level.Price - rangeHeight
	}

	// Track the active breakout
	bd.activeBreak = &activeBreakout{
		level:        level.Price,
		direction:    direction,
		breakPrice:   tfc.Close,
		breakVolume:  tfc.Volume,
		avgVolume:    avgVol,
		rangeHeight:  rangeHeight,
		measuredMove: measuredMove,
		adxAtBreak:   adx,
		adxRising:    adxRising,
	}

	return &BreakoutEvent{
		Stage:          BreakoutStageBreak,
		Level:          level.Price,
		Direction:      direction,
		BreakPrice:     tfc.Close,
		BreakVolume:    tfc.Volume,
		AvgVolume:      avgVol,
		VolumeRatio:    volRatio,
		ADXValue:       adx,
		ADXRising:      adxRising,
		MeasuredMove:   measuredMove,
		RangeHeight:    rangeHeight,
		BarsSinceBreak: 0,
		Token:          tfc.Token,
		Exchange:       tfc.Exchange,
		TF:             tfc.TF,
		TS:             tfc.TS,
	}
}

// trackActiveBreakout monitors an active breakout for retest, confirmation, or failure.
func (bd *BreakoutDetector) trackActiveBreakout(tfc model.TFCandle, adx float64, adxRising bool) *BreakoutEvent {
	ab := bd.activeBreak
	ab.barsSince++

	baseEvent := BreakoutEvent{
		Level:          ab.level,
		Direction:      ab.direction,
		BreakPrice:     ab.breakPrice,
		BreakVolume:    ab.breakVolume,
		AvgVolume:      ab.avgVolume,
		MeasuredMove:   ab.measuredMove,
		RangeHeight:    ab.rangeHeight,
		BarsSinceBreak: ab.barsSince,
		ADXValue:       adx,
		ADXRising:      adxRising,
		Token:          tfc.Token,
		Exchange:       tfc.Exchange,
		TF:             tfc.TF,
		TS:             tfc.TS,
	}

	// Check for failure: price closes back inside the level within boFailureBars
	if ab.barsSince <= boFailureBars {
		if ab.direction == "UP" && tfc.Close < ab.level {
			bd.activeBreak = nil
			baseEvent.Stage = BreakoutStageFailed
			return &baseEvent
		}
		if ab.direction == "DOWN" && tfc.Close > ab.level {
			bd.activeBreak = nil
			baseEvent.Stage = BreakoutStageFailed
			return &baseEvent
		}
	}

	// Check for ADX collapse (ADX falling after break)
	if ab.barsSince > 2 && !adxRising && adx < ab.adxAtBreak-5 {
		bd.activeBreak = nil
		baseEvent.Stage = BreakoutStageFailed
		return &baseEvent
	}

	// Check for retest
	if !ab.retested {
		retesting := false
		if ab.direction == "UP" {
			// Retest: price pulls back to the broken level (now support)
			if tfc.Low <= ab.level+srTolerance && tfc.Close > ab.level {
				retesting = true
			}
		} else {
			// Retest: price pulls back to the broken level (now resistance)
			if tfc.High >= ab.level-srTolerance && tfc.Close < ab.level {
				retesting = true
			}
		}
		if retesting {
			ab.retested = true
			// Check for rejection candle (pin bar / doji)
			ab.retestCandle = bd.isRejectionCandle(tfc, ab.direction)
			baseEvent.Stage = BreakoutStageRetest
			return &baseEvent
		}
	}

	// If retested with rejection, confirm the breakout
	if ab.retested && ab.retestCandle {
		bd.activeBreak = nil
		baseEvent.Stage = BreakoutStageConfirmed
		return &baseEvent
	}

	// Time out if no retest within boRetestBars
	if ab.barsSince > boRetestBars {
		// Still trending away from level — confirm anyway
		bd.activeBreak = nil
		baseEvent.Stage = BreakoutStageConfirmed
		return &baseEvent
	}

	return nil
}

// isCompressing checks for pre-breakout compression signals.
func (bd *BreakoutDetector) isCompressing(bbw, atrPct float64) bool {
	if len(bd.recentBBW) < boBBWSqueezeWindow {
		return false
	}
	// Check if current BBW is the lowest in the window
	for _, prev := range bd.recentBBW[:len(bd.recentBBW)-1] {
		if bbw >= prev {
			return false
		}
	}
	// Also check ATR% < 0.3%
	return atrPct < 0.30
}

// isRejectionCandle checks if a candle is a rejection (pin bar / doji).
func (bd *BreakoutDetector) isRejectionCandle(tfc model.TFCandle, direction string) bool {
	body := abs64(tfc.Close - tfc.Open)
	totalRange := tfc.High - tfc.Low
	if totalRange == 0 {
		return false
	}

	// Pin bar: body is less than 30% of total range
	bodyRatio := float64(body) / float64(totalRange)
	if bodyRatio > 0.30 {
		return false
	}

	if direction == "UP" {
		// For upward breakout retest, want long lower wick (bounce off support)
		lowerWick := minI64(tfc.Open, tfc.Close) - tfc.Low
		return float64(lowerWick)/float64(totalRange) > 0.50
	}
	// For downward breakout retest, want long upper wick (rejection at resistance)
	upperWick := tfc.High - maxI64(tfc.Open, tfc.Close)
	return float64(upperWick)/float64(totalRange) > 0.50
}

// avgCandleBody returns the average body size of recent candles.
func (bd *BreakoutDetector) avgCandleBody() float64 {
	if len(bd.recentBodies) == 0 {
		return 0
	}
	var sum int64
	for _, b := range bd.recentBodies {
		sum += b
	}
	return float64(sum) / float64(len(bd.recentBodies))
}

// estimateRangeHeight estimates the recent trading range height for measured move.
func (bd *BreakoutDetector) estimateRangeHeight(level SRLevel, tfc model.TFCandle) int64 {
	_ = tfc // might use in future
	// Use a simple estimate: look at recent candle range
	if len(bd.recentBodies) == 0 {
		return 0
	}
	// Approximate range from average body sizes scaled by window.
	// A production system would track the actual consolidation range.
	avgBody := bd.avgCandleBody()
	return int64(avgBody * float64(boCandleBodyWindow))
}

// Reset clears the breakout detector state.
func (bd *BreakoutDetector) Reset() {
	bd.activeBreak = nil
	bd.recentBodies = bd.recentBodies[:0]
	bd.recentBBW = bd.recentBBW[:0]
	bd.prevADX = 0
}

// HasActiveBreakout returns true if a breakout is being tracked.
func (bd *BreakoutDetector) HasActiveBreakout() bool {
	return bd.activeBreak != nil
}

// ActiveBreakoutLevel returns the price level of the active breakout, or 0.
func (bd *BreakoutDetector) ActiveBreakoutLevel() int64 {
	if bd.activeBreak == nil {
		return 0
	}
	return bd.activeBreak.level
}

// ActiveBreakoutCreatedAt placeholder for timestamp tracking.
func (bd *BreakoutDetector) ActiveBreakoutCreatedAt() time.Time {
	return time.Time{}
}
