package analyst

import (
	"math"
	"time"

	"trading-systemv1/internal/model"
)

const (
	// srTolerance is the price tolerance for matching a level (in paise).
	// Prices within this range are considered "at" the same level.
	srTolerance int64 = 500 // 5 INR tolerance

	// srMaxLevels is the maximum number of active levels tracked.
	srMaxLevels = 50

	// srStaleBarThreshold: remove levels not tested in this many bars.
	srStaleBarThreshold = 30

	// srMinTouches is the minimum touches for a level to be valid.
	srMinTouches = 2

	// srVolumeWindow is the lookback for volume average.
	srVolumeWindow = 10

	// srSwingWindow is the number of bars on each side for swing detection.
	srSwingWindow = 1

	// srCandleHistorySize is the number of recent candles to retain.
	srCandleHistorySize = 50

	// roundNumberInterval100 is the interval for round number detection (100 INR in paise).
	roundNumberInterval100 int64 = 10000
	// roundNumberInterval50 is the interval for 50-point round numbers (50 INR in paise).
	roundNumberInterval50 int64 = 5000
)

// SRDetector detects support and resistance levels from 1m candle data.
// Implements the logic from support_resistance_detector.md:
//   - Swing high/low detection
//   - Multi-touch tracking
//   - Volume spike confirmation
//   - Level strength classification
//   - Level invalidation (clean break, staleness)
type SRDetector struct {
	levels  []SRLevel
	candles []model.TFCandle // sliding window of recent candles
	barIdx  int             // monotonically increasing bar counter
	volumes []int64         // sliding window for volume average
}

// NewSRDetector creates a new S/R detector.
func NewSRDetector() *SRDetector {
	return &SRDetector{
		levels:  make([]SRLevel, 0, srMaxLevels),
		candles: make([]model.TFCandle, 0, srCandleHistorySize),
		volumes: make([]int64, 0, srVolumeWindow),
	}
}

// Update processes a new finalized 1m candle and updates S/R levels.
func (d *SRDetector) Update(tfc model.TFCandle) {
	d.barIdx++

	// Append candle to history
	d.candles = append(d.candles, tfc)
	if len(d.candles) > srCandleHistorySize {
		d.candles = d.candles[1:]
	}

	// Track volume
	d.volumes = append(d.volumes, tfc.Volume)
	if len(d.volumes) > srVolumeWindow {
		d.volumes = d.volumes[1:]
	}

	// Detect swing highs and lows (need at least 3 candles: 1 on each side)
	if len(d.candles) >= 2*srSwingWindow+1 {
		mid := len(d.candles) - 1 - srSwingWindow
		d.detectSwingHigh(mid)
		d.detectSwingLow(mid)
	}

	// Check existing levels for touches and breaks
	d.checkTouches(tfc)

	// Invalidate stale levels
	d.invalidateLevels()

	// Reclassify strength
	d.classifyStrengths()
}

// ActiveLevels returns the currently active S/R levels sorted by price.
func (d *SRDetector) ActiveLevels() []SRLevel {
	result := make([]SRLevel, len(d.levels))
	copy(result, d.levels)
	return result
}

// BarIndex returns the current bar counter.
func (d *SRDetector) BarIndex() int {
	return d.barIdx
}

// ── Detection ──

// detectSwingHigh checks if candle[mid] is a swing high (lower highs on both sides).
func (d *SRDetector) detectSwingHigh(mid int) {
	if mid < srSwingWindow || mid >= len(d.candles)-srSwingWindow {
		return
	}
	midHigh := d.candles[mid].High
	for i := 1; i <= srSwingWindow; i++ {
		if d.candles[mid-i].High >= midHigh || d.candles[mid+i].High >= midHigh {
			return
		}
	}
	// It's a swing high — mark the wick tip as resistance
	d.addOrUpdateLevel(midHigh, true, d.candles[mid].TS)
}

// detectSwingLow checks if candle[mid] is a swing low (higher lows on both sides).
func (d *SRDetector) detectSwingLow(mid int) {
	if mid < srSwingWindow || mid >= len(d.candles)-srSwingWindow {
		return
	}
	midLow := d.candles[mid].Low
	for i := 1; i <= srSwingWindow; i++ {
		if d.candles[mid-i].Low <= midLow || d.candles[mid+i].Low <= midLow {
			return
		}
	}
	// It's a swing low — mark the wick tip as support
	d.addOrUpdateLevel(midLow, false, d.candles[mid].TS)
}

// addOrUpdateLevel either creates a new level or increments touch count on an existing one.
func (d *SRDetector) addOrUpdateLevel(price int64, isResistance bool, ts time.Time) {
	for i := range d.levels {
		if abs64(d.levels[i].Price-price) <= srTolerance {
			d.levels[i].TouchCount++
			d.levels[i].LastTouchBar = d.barIdx
			return
		}
	}

	// New level
	level := SRLevel{
		Price:         price,
		Type:          LevelTypeHorizontal,
		Strength:      LevelStrengthWeak,
		TouchCount:    1,
		IsResistance:  isResistance,
		IsRoundNumber: isRoundNumber(price),
		LastTouchBar:  d.barIdx,
		CreatedAt:     ts,
	}

	if len(d.levels) >= srMaxLevels {
		// Remove weakest level
		weakIdx := d.findWeakestLevel()
		d.levels[weakIdx] = level
	} else {
		d.levels = append(d.levels, level)
	}
}

// checkTouches checks if the current candle touches or breaks any existing levels.
// Checks clean breaks first (before touches) to avoid marking a touch on a broken level.
func (d *SRDetector) checkTouches(tfc model.TFCandle) {
	volSpike := d.isVolumeSpike(tfc.Volume)
	bodyHigh := maxI64(tfc.Open, tfc.Close)
	bodyLow := minI64(tfc.Open, tfc.Close)

	// Iterate backwards so removals don't shift remaining indices
	for i := len(d.levels) - 1; i >= 0; i-- {
		level := &d.levels[i]

		// ── Check clean break FIRST ──
		removed := false
		if level.IsResistance {
			// Resistance broken: full candle body above level
			if bodyLow > level.Price+srTolerance {
				d.removeLevel(i)
				removed = true
			}
		} else {
			// Support broken: full candle body below level
			if bodyHigh < level.Price-srTolerance {
				d.removeLevel(i)
				removed = true
			}
		}
		if removed {
			continue
		}

		// ── Touch detection (only if not broken) ──
		if tfc.High >= level.Price-srTolerance && tfc.Low <= level.Price+srTolerance {
			level.TouchCount++
			level.LastTouchBar = d.barIdx
			if volSpike {
				level.HasVolSpike = true
			}
		}
	}
}

// invalidateLevels removes levels not tested in srStaleBarThreshold bars.
func (d *SRDetector) invalidateLevels() {
	kept := d.levels[:0]
	for _, level := range d.levels {
		if d.barIdx-level.LastTouchBar <= srStaleBarThreshold {
			kept = append(kept, level)
		}
	}
	d.levels = kept
}

// classifyStrengths assigns strength based on touches, HTF, round numbers, volume.
func (d *SRDetector) classifyStrengths() {
	for i := range d.levels {
		score := 0
		if d.levels[i].TouchCount >= 3 {
			score += 2
		} else if d.levels[i].TouchCount >= 2 {
			score++
		}
		if d.levels[i].HasHTF {
			score++
		}
		if d.levels[i].IsRoundNumber {
			score++
		}
		if d.levels[i].HasVolSpike {
			score++
		}

		switch {
		case score >= 3:
			d.levels[i].Strength = LevelStrengthStrong
		case score >= 2:
			d.levels[i].Strength = LevelStrengthMedium
		default:
			d.levels[i].Strength = LevelStrengthWeak
		}
	}
}

// ── Helpers ──

func (d *SRDetector) isVolumeSpike(vol int64) bool {
	if len(d.volumes) < 2 {
		return false
	}
	avg := d.avgVolume()
	return avg > 0 && float64(vol) > avg*1.5
}

func (d *SRDetector) avgVolume() float64 {
	if len(d.volumes) == 0 {
		return 0
	}
	var sum int64
	for _, v := range d.volumes {
		sum += v
	}
	return float64(sum) / float64(len(d.volumes))
}

func (d *SRDetector) findWeakestLevel() int {
	weakIdx := 0
	weakScore := math.MaxInt32
	for i, level := range d.levels {
		score := level.TouchCount
		if level.HasHTF {
			score += 10
		}
		if level.IsRoundNumber {
			score += 5
		}
		if score < weakScore {
			weakScore = score
			weakIdx = i
		}
	}
	return weakIdx
}

func (d *SRDetector) removeLevel(idx int) {
	if idx < 0 || idx >= len(d.levels) {
		return
	}
	d.levels = append(d.levels[:idx], d.levels[idx+1:]...)
}

func isRoundNumber(price int64) bool {
	// Check if price is a multiple of 100 or 50 INR (in paise)
	return price%roundNumberInterval100 == 0 || price%roundNumberInterval50 == 0
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
