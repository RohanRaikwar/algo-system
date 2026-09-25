package strategy

import (
	"encoding/json"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 10PTS — State & Configuration
//
//  Same EMA6/EMA9 entry/exit as NIFTY50_FNO_SL, with one addition:
//    Exit when FNO option premium gains FNOTargetProfitPts paise (10 pts).
//    After exit, re-enter immediately on next momentum signal.
// ════════════════════════════════════════════════════════════════════

// Nifty5010PtsConfig holds all configurable parameters.
type Nifty5010PtsConfig struct {
	// ── Indicators ──
	EMA6Period int // 6  (fast EMA)
	EMA9Period int // 9  (slow EMA)

	// ── Prefetch buffer ──
	BufferSize int

	// ── Cooldown ──
	CooldownCandles int

	// ── Re-entry control ──
	ReEntryEnabled bool

	// ── Re-entry separation guard ──
	MinReEntrySeparationPct float64

	// ── Min EMA6-EMA9 diff guard ──
	MinEMA6EMA9DiffPct float64

	// ── Sideways market filter ──
	SidewaysEnabled bool
	MaxChopCrosses  int
	MA21FlatPct     float64
	NarrowRangePct  float64

	// ── Momentum bypass ──
	// If abs(close - EMA9) / EMA9 > this %, force entry. 0 = disabled.
	MomentumBypassPct float64

	// ── Big Move Filter ──
	// Skip entry if last candle moved more than this many paise.
	// Example: 500 = 5 NIFTY points. Prevents chasing after big moves.
	BigMoveThresholdPts int64
	BigMoveEnabled      bool

	// ── Pullback Detection ──
	// Wait for price to pull back near EMA6/9 before entry.
	// Prevents entering at extremes after a strong move.
	PullbackEnabled         bool
	PullbackMaxDistancePct  float64 // Max distance from EMA average to consider "pullback" (e.g., 0.15%)
	PullbackCandlesRequired int     // How many small candles needed to confirm pullback (e.g., 2-3)
	PullbackTimeoutCandles  int     // Max candles to wait for pullback before giving up (e.g., 5-10)

	// ── Pullback Conditions ──
	// Only wait for pullback if:
	// 1. Big move is 1-2 candles only (quick spike), OR
	// 2. Price has retraced 50%+ of the big move
	// Otherwise, skip pullback requirement (medium sustained moves)
	PullbackOnlyFor1_2Candles  bool    // Only require pullback for 1-2 candle spikes
	PullbackOr50PctRetracement bool    // OR if price retraced 50%+ of big move
	PullbackRetracementPct     float64 // Retracement threshold (e.g., 0.50 = 50%)

	// ── Confirmation Candle ──
	// Require confirmation candle after crossover (green for CALL, red for PUT)
	ConfirmationCandleEnabled bool // Require confirmation candle
	SkipCrossoverCandle       bool // Do NOT enter on crossover candle itself

	// ── Close vs EMA9 Exit ──
	// Exit CALL if close < EMA9; exit PUT if close > EMA9 (more aggressive than crossover exit)
	CloseVsEMA9ExitEnabled bool

	// ── VWAP Distance Check ──
	VWAPMaxDistancePct float64 // Max distance from VWAP to enter (e.g., 0.5%), 0 = disabled

	// ── Step Trailing ──
	StepTrailEnabled  bool  // Enable step trailing profit management
	StepTrail1Pts     int64 // First step: move SL to cost (e.g., 300 = 3 points)
	StepTrail2Pts     int64 // Second step: book 50% (e.g., 400 = 4 points)
	TrailingBufferPts int64 // Trailing buffer (e.g., 200 = 2 points)

	// ── Strong Opposite Candle Detection ──
	StrongOppositeCandleEnabled bool    // Enable strong opposite candle exit
	StrongOppositeCandlePts     int64   // Min candle size to consider "strong" (e.g., 300 = 3 points)
	StrongOppositeCandleRatio   float64 // Min body/range ratio (e.g., 0.7 = 70% body)

	// ── Time-of-day filter ──
	SkipFirstMinutes int
	SkipLastMinutes  int

	// ── Lunch-hour sideways override ──
	LunchFilterEnabled bool
	LunchStartHHMM     string
	LunchEndHHMM       string

	// ── Trend direction filter ──
	TrendEnabled  bool
	TrendLookback int
	TrendSlopePct float64

	// ── INDEX SL — disabled ──
	IndexHardSLPct     float64
	IndexTrailSLPct    float64
	IndexTrailStartPct float64

	// ── FNO SL ──
	FNOHardSLPct     float64
	FNOTrailSLPct    float64
	FNOTrailStartPct float64

	// ── FNO Target Profit (key feature) ──
	// Exit when FNO premium gains this % of entry price. 1.5 = 1.5%.
	// Dynamic target based on entry price instead of fixed points.
	// After exit, re-enter on next momentum signal immediately.
	FNOTargetProfitPct float64

	// ── Sideways regime: reduced FNO Target Profit ──
	// When sideways (score=2), use this instead of FNOTargetProfitPct.
	// 1.0 = 1.0% gain. Choppy (score≥3) blocks entry entirely.
	SidewaysFNOTargetProfitPct float64

	// ── FNO token identifiers ──
	FNOCallToken string
	FNOPutToken  string

	// ── Candle filter ──
	IndexToken string // e.g. "NSE:99926000"

	// ── Name override ──
	NameOverride string
}

// DefaultNifty5010PtsConfig returns sensible defaults.
func DefaultNifty5010PtsConfig() Nifty5010PtsConfig {
	return Nifty5010PtsConfig{
		EMA6Period: 6,
		EMA9Period: 9,
		BufferSize: 10,
		// Cooldown — disabled for fast scalping
		CooldownCandles: 0,
		// Re-entry — disabled (momentum re-entries handle it)
		ReEntryEnabled:          false,
		MinReEntrySeparationPct: 0,
		MinEMA6EMA9DiffPct:      0.001, // require 0.001% gap between EMAs
		// Sideways filter — disabled
		SidewaysEnabled: false,
		MaxChopCrosses:  3,
		MA21FlatPct:     0.05,
		NarrowRangePct:  0.30,
		// Momentum bypass — disabled
		MomentumBypassPct: 0,
		// Big Move Filter — DISABLED
		BigMoveEnabled:      false,
		BigMoveThresholdPts: 1000, // 10 NIFTY points = 1000 paise
		// Pullback Detection — DISABLED
		PullbackEnabled:         false,
		PullbackMaxDistancePct:  0.12,
		PullbackCandlesRequired: 1,
		PullbackTimeoutCandles:  5,
		// Pullback Conditions — DISABLED
		PullbackOnlyFor1_2Candles:  true,
		PullbackOr50PctRetracement: true,
		PullbackRetracementPct:     0.382,
		// Confirmation Candle — disabled for speed
		ConfirmationCandleEnabled: false, // Skip confirmation for faster entries
		SkipCrossoverCandle:       false, // Enter on crossover candle
		// Close vs EMA9 exit — disabled (use EMA crossover exit only)
		CloseVsEMA9ExitEnabled: false,
		// VWAP Distance Check — disabled for speed
		VWAPMaxDistancePct: 0, // Disabled (was 0.5%)
		// Step Trailing — optimized for 3-4 point scalps
		StepTrailEnabled:  true,
		StepTrail1Pts:     200, // 2 points → move SL to cost (was 3)
		StepTrail2Pts:     300, // 3 points → activate trailing (was 4)
		TrailingBufferPts: 100, // 1 point trailing buffer (was 2)
		// Strong Opposite Candle Detection — tighter for scalping
		StrongOppositeCandleEnabled: true,
		StrongOppositeCandlePts:     200,  // 2 points minimum (was 3)
		StrongOppositeCandleRatio:   0.65, // 65% body ratio (was 70%)
		// Time-of-day filter — start trading from 9:16 AM
		SkipFirstMinutes: 0,
		SkipLastMinutes:  0,
		// Lunch-hour filter — disabled
		LunchFilterEnabled: false,
		LunchStartHHMM:     "1200",
		LunchEndHHMM:       "1330",
		// Trend direction filter — disabled for scalping
		TrendEnabled:  false,
		TrendLookback: 50,
		TrendSlopePct: 0.05,
		// INDEX SL — disabled
		IndexHardSLPct:     0,
		IndexTrailSLPct:    0,
		IndexTrailStartPct: 0,
		// FNO SL — adjusted for better protection
		FNOHardSLPct:     8.0, // 3% hard SL on FNO premium
		FNOTrailSLPct:    5.0, // 5% trail SL (was 4%)
		FNOTrailStartPct: 6.0, // Start trailing at 3% profit (was 2%)
		// FNO Target: 1.2% for scalping (was 1.5%)
		FNOTargetProfitPct: 1.5, // ~3-4 points on typical option premium
		// FNO Target in sideways: 0.8% (was 1.0%)
		SidewaysFNOTargetProfitPct: 1,
		// FNO tokens — set at runtime
		FNOCallToken: "",
		FNOPutToken:  "",
		// Index filter
		IndexToken:   "NSE:99926000",
		NameOverride: "NIFTY50_10PTS",
	}
}

// ── Buffer entry ──

type nifty5010PtsBufferEntry struct {
	Time  time.Time
	Open  float64
	High  float64
	Low   float64
	Close float64
	EMA6  float64
	EMA9  float64
}

// upperBody returns max(open, close) — the upper edge of the candle body.
func (e nifty5010PtsBufferEntry) upperBody() float64 {
	if e.Close > e.Open {
		return e.Close
	}
	return e.Open
}

// lowerBody returns min(open, close) — the lower edge of the candle body.
func (e nifty5010PtsBufferEntry) lowerBody() float64 {
	if e.Close < e.Open {
		return e.Close
	}
	return e.Open
}

// ── Per-instrument state ──

type nifty5010PtsState struct {
	// ── Indicators ──
	EMA6 *indicator.EMA
	EMA9 *indicator.EMA

	// ── Ring buffer ──
	Buffer      []nifty5010PtsBufferEntry
	BufferIdx   int
	BufferCount int

	// ── Position state machine ──
	Side PositionSide

	// ── Setup tracking ──
	CallSetupActive bool
	PutSetupActive  bool

	// ── Position details ──
	EntryPrice      int64
	BestPrice       int64
	IndexEntryPrice int64
	IndexBestPrice  int64

	// ── FNO price tracking ──
	FNOEntryPrice int64
	FNOBestPrice  int64

	// ── Active FNO target (set at entry time based on market regime) ──
	ActiveFNOTargetProfitPct float64

	// ── Cooldown tracking ──
	CandlesSinceExit    int
	InCooldown          bool
	PendingEntry        PositionSide
	PendingEMADiffEntry PositionSide

	// ── Stop Loss tracking ──
	SLHitSide PositionSide

	// ── Pullback tracking ──
	LastBigMoveTime       time.Time // When last big move occurred
	LastBigMoveHigh       float64   // High of the big move candle
	LastBigMoveLow        float64   // Low of the big move candle
	BigMoveCandleCount    int       // How many consecutive big move candles
	PullbackInProgress    bool      // Waiting for pullback
	SmallCandleCount      int       // Count of small candles since big move
	PullbackCandlesWaited int       // How many candles we've waited for pullback

	// ── Confirmation tracking ──
	ConfirmationPending PositionSide // Waiting for confirmation candle (CALL or PUT)

	// ── Step trailing tracking ──
	Step1Activated bool  // SL moved to cost at +3 points
	Step2Activated bool  // Trailing activated at +4 points
	TrailingSL     int64 // Current trailing SL price

	// ── Crossover consumption ──
	// After an SL/trail exit, we record BufferIdx here so the lookback
	// helpers refuse to detect the same crossover twice. -1 = no limit.
	CrossConsumedAtBufIdx int

	// ── Dedup ──
	LastCloseTS time.Time
}

// newNifty5010PtsState creates a fresh state with the given config.
func newNifty5010PtsState(cfg Nifty5010PtsConfig) *nifty5010PtsState {
	bufSize := cfg.BufferSize
	if cfg.TrendEnabled && cfg.TrendLookback+1 > bufSize {
		bufSize = cfg.TrendLookback + 1
	}
	if bufSize <= 0 {
		bufSize = 10
	}
	return &nifty5010PtsState{
		EMA6:                  indicator.NewEMA(cfg.EMA6Period),
		EMA9:                  indicator.NewEMA(cfg.EMA9Period),
		Buffer:                make([]nifty5010PtsBufferEntry, bufSize),
		Side:                  SideNone,
		SLHitSide:             SideNone,
		PendingEntry:          SideNone,
		ConfirmationPending:   SideNone,
		CrossConsumedAtBufIdx: -1,
	}
}

// bufferReady returns true when at least 2 points exist for crossover detection.
func (st *nifty5010PtsState) bufferReady() bool {
	return st.BufferCount >= 2
}

// bufferReady3 returns true when at least 3 candles exist.
func (st *nifty5010PtsState) bufferReady3() bool {
	return st.BufferCount >= 3
}

// pushBuffer appends a new entry to the ring buffer.
func (st *nifty5010PtsState) pushBuffer(entry nifty5010PtsBufferEntry) {
	st.Buffer[st.BufferIdx] = entry
	st.BufferIdx = (st.BufferIdx + 1) % len(st.Buffer)
	if st.BufferCount < len(st.Buffer) {
		st.BufferCount++
	}
}

// prevAndCurr returns the previous and current buffer entries.
func (st *nifty5010PtsState) prevAndCurr() (prev, curr nifty5010PtsBufferEntry) {
	size := len(st.Buffer)
	currIdx := (st.BufferIdx - 1 + size) % size
	prevIdx := (st.BufferIdx - 2 + size) % size
	return st.Buffer[prevIdx], st.Buffer[currIdx]
}

// threeBack returns the last 3 buffer entries: oldest, middle, newest.
func (st *nifty5010PtsState) threeBack() (candle3, candle2, candle1 nifty5010PtsBufferEntry) {
	size := len(st.Buffer)
	idx1 := (st.BufferIdx - 1 + size) % size
	idx2 := (st.BufferIdx - 2 + size) % size
	idx3 := (st.BufferIdx - 3 + size) % size
	return st.Buffer[idx3], st.Buffer[idx2], st.Buffer[idx1]
}

// latestBufferEntry returns the most recent buffer entry.
func (st *nifty5010PtsState) latestBufferEntry() (nifty5010PtsBufferEntry, bool) {
	if st.BufferCount == 0 || len(st.Buffer) == 0 {
		return nifty5010PtsBufferEntry{}, false
	}
	size := len(st.Buffer)
	idx := (st.BufferIdx - 1 + size) % size
	return st.Buffer[idx], true
}

// isBigMove checks if the last candle moved more than the threshold.
// Returns true if candle range (high - low) exceeds BigMoveThresholdPts.
func (st *nifty5010PtsState) isBigMove(cfg Nifty5010PtsConfig) bool {
	if !cfg.BigMoveEnabled || cfg.BigMoveThresholdPts <= 0 {
		return false
	}

	latest, ok := st.latestBufferEntry()
	if !ok {
		return false
	}

	candleRange := int64(latest.High - latest.Low)
	return candleRange >= cfg.BigMoveThresholdPts
}

// isPullbackNearEMA checks if price has pulled back near EMA6/9 average.
// Returns true if:
// 1. Price is within PullbackMaxDistancePct of EMA average
// 2. We've seen PullbackCandlesRequired small candles (confirming consolidation)
func (st *nifty5010PtsState) isPullbackNearEMA(cfg Nifty5010PtsConfig) bool {
	if !cfg.PullbackEnabled {
		return true // Skip check if disabled
	}

	latest, ok := st.latestBufferEntry()
	if !ok {
		return false
	}

	// Calculate EMA average (midpoint between EMA6 and EMA9)
	emaAvg := (latest.EMA6 + latest.EMA9) / 2
	if emaAvg <= 0 {
		return false
	}

	// Check distance from EMA average
	distancePct := (latest.Close - emaAvg) / emaAvg * 100
	if distancePct < 0 {
		distancePct = -distancePct
	}

	isNearEMA := distancePct <= cfg.PullbackMaxDistancePct

	// If we're near EMA and have enough small candles, pullback is confirmed
	if isNearEMA && st.SmallCandleCount >= cfg.PullbackCandlesRequired {
		return true
	}

	return false
}

// isSmallCandle checks if current candle is "small" (not a big move).
// Used to count consolidation candles during pullback.
func (st *nifty5010PtsState) isSmallCandle(cfg Nifty5010PtsConfig) bool {
	if cfg.BigMoveThresholdPts <= 0 {
		return true
	}

	latest, ok := st.latestBufferEntry()
	if !ok {
		return false
	}

	candleRange := int64(latest.High - latest.Low)
	// Small candle = less than half the big move threshold
	return candleRange < (cfg.BigMoveThresholdPts / 2)
}

// isConfirmationCandle checks if the current candle confirms the entry direction.
// CALL: green candle (close > open)
// PUT: red candle (close < open)
func (st *nifty5010PtsState) isConfirmationCandle(side PositionSide) bool {
	latest, ok := st.latestBufferEntry()
	if !ok {
		return false
	}

	switch side {
	case SideCall:
		// Green candle: close > open
		return latest.Close > latest.Open
	case SidePut:
		// Red candle: close < open
		return latest.Close < latest.Open
	}
	return false
}

// isNearVWAP checks if price is within acceptable distance from VWAP.
// Returns true if VWAP check is disabled or price is within threshold.
func (st *nifty5010PtsState) isNearVWAP(cfg Nifty5010PtsConfig, vwap float64) bool {
	if cfg.VWAPMaxDistancePct <= 0 {
		return true // Check disabled
	}

	if vwap <= 0 {
		return true // VWAP not available, skip check
	}

	latest, ok := st.latestBufferEntry()
	if !ok {
		return false
	}

	distancePct := (latest.Close - vwap) / vwap * 100
	if distancePct < 0 {
		distancePct = -distancePct
	}

	return distancePct <= cfg.VWAPMaxDistancePct
}

// isStrongOppositeCandle detects a strong candle in the opposite direction.
// Used to exit positions when momentum reverses strongly.
func (st *nifty5010PtsState) isStrongOppositeCandle(cfg Nifty5010PtsConfig, currentSide PositionSide) bool {
	if !cfg.StrongOppositeCandleEnabled || cfg.StrongOppositeCandlePts <= 0 {
		return false
	}

	latest, ok := st.latestBufferEntry()
	if !ok {
		return false
	}

	candleRange := int64(latest.High - latest.Low)
	if candleRange < cfg.StrongOppositeCandlePts {
		return false // Not big enough
	}

	// Calculate body size and ratio
	body := latest.Close - latest.Open
	if body < 0 {
		body = -body
	}
	bodyRatio := body / (latest.High - latest.Low)

	if bodyRatio < cfg.StrongOppositeCandleRatio {
		return false // Not strong enough (too much wick)
	}

	// Check if candle is opposite to current position
	switch currentSide {
	case SideCall:
		// Strong RED candle (close < open)
		return latest.Close < latest.Open
	case SidePut:
		// Strong GREEN candle (close > open)
		return latest.Close > latest.Open
	}

	return false
}

// getDistanceFromEMA returns the distance from EMA average as a decimal (e.g., 0.0015 = 0.15%).
func (st *nifty5010PtsState) getDistanceFromEMA(cfg Nifty5010PtsConfig) float64 {
	latest, ok := st.latestBufferEntry()
	if !ok {
		return 999.0 // Very far if no data
	}

	emaAvg := (latest.EMA6 + latest.EMA9) / 2
	if emaAvg <= 0 {
		return 999.0
	}

	distancePct := (latest.Close - emaAvg) / emaAvg
	if distancePct < 0 {
		distancePct = -distancePct
	}

	return distancePct
}

// shouldWaitForPullback determines if we should wait for pullback based on:
// 1. Big move is only 1-2 candles (quick spike), OR
// 2. Price has retraced 50%+ of the big move
// Returns true if pullback is required, false if we can skip it.
func (st *nifty5010PtsState) shouldWaitForPullback(cfg Nifty5010PtsConfig) bool {
	if !cfg.PullbackEnabled {
		return false
	}

	// If not in pullback mode, no need to wait
	if !st.PullbackInProgress {
		return false
	}

	// Check condition 1: Is it a 1-2 candle spike?
	if cfg.PullbackOnlyFor1_2Candles && st.BigMoveCandleCount <= 2 {
		return true // Yes, wait for pullback
	}

	// Check condition 2: Has price retraced 50%+ of big move?
	if cfg.PullbackOr50PctRetracement && st.hasRetraced50Pct(cfg) {
		return true // Yes, wait for pullback
	}

	// Neither condition met - skip pullback requirement
	return false
}

// hasRetraced50Pct checks if current price has retraced 50%+ of the big move.
func (st *nifty5010PtsState) hasRetraced50Pct(cfg Nifty5010PtsConfig) bool {
	if st.LastBigMoveHigh <= 0 || st.LastBigMoveLow <= 0 {
		return false
	}

	latest, ok := st.latestBufferEntry()
	if !ok {
		return false
	}

	// Calculate big move range
	bigMoveRange := st.LastBigMoveHigh - st.LastBigMoveLow
	if bigMoveRange <= 0 {
		return false
	}

	// Calculate retracement from high
	retracement := st.LastBigMoveHigh - latest.Close
	if retracement < 0 {
		retracement = 0 // Price went higher, no retracement
	}

	// Calculate retracement percentage
	retracementPct := retracement / bigMoveRange

	return retracementPct >= cfg.PullbackRetracementPct
}

// ── 10-candle crossover helpers ──

func (st *nifty5010PtsState) crossedAboveInBuf(
	getA, getB func(nifty5010PtsBufferEntry) float64,
) bool {
	size := len(st.Buffer)
	n := st.BufferCount
	if n < 2 {
		return false
	}
	currIdx := (st.BufferIdx - 1 + size) % size
	if getA(st.Buffer[currIdx]) <= getB(st.Buffer[currIdx]) {
		return false
	}
	lookback := n - 1
	if lookback > size-1 {
		lookback = size - 1
	}
	for i := 1; i <= lookback; i++ {
		idx := (st.BufferIdx - 1 - i + size*10) % size
		e := st.Buffer[idx]
		if getA(e) <= getB(e) {
			return true
		}
		// Skip entries at or before the consumed crossover index.
		if st.CrossConsumedAtBufIdx >= 0 && idx == st.CrossConsumedAtBufIdx {
			return false
		}
	}
	return false
}

func (st *nifty5010PtsState) crossedBelowInBuf(
	getA, getB func(nifty5010PtsBufferEntry) float64,
) bool {
	size := len(st.Buffer)
	n := st.BufferCount
	if n < 2 {
		return false
	}
	currIdx := (st.BufferIdx - 1 + size) % size
	if getA(st.Buffer[currIdx]) >= getB(st.Buffer[currIdx]) {
		return false
	}
	lookback := n - 1
	if lookback > size-1 {
		lookback = size - 1
	}
	for i := 1; i <= lookback; i++ {
		idx := (st.BufferIdx - 1 - i + size*10) % size
		e := st.Buffer[idx]
		if getA(e) >= getB(e) {
			return true
		}
		// Skip entries at or before the consumed crossover index.
		if st.CrossConsumedAtBufIdx >= 0 && idx == st.CrossConsumedAtBufIdx {
			return false
		}
	}
	return false
}

// ema6CrossedAboveEMA9: EMA6 > EMA9 (CALL entry / PUT exit)
func (st *nifty5010PtsState) ema6CrossedAboveEMA9() bool {
	return st.crossedAboveInBuf(
		func(e nifty5010PtsBufferEntry) float64 { return e.EMA6 },
		func(e nifty5010PtsBufferEntry) float64 { return e.EMA9 },
	)
}

// ema6CrossedBelowEMA9: EMA6 < EMA9 (PUT entry / CALL exit)
func (st *nifty5010PtsState) ema6CrossedBelowEMA9() bool {
	return st.crossedBelowInBuf(
		func(e nifty5010PtsBufferEntry) float64 { return e.EMA6 },
		func(e nifty5010PtsBufferEntry) float64 { return e.EMA9 },
	)
}

// ── Trend direction filter ──

func (st *nifty5010PtsState) trendBias(cfg Nifty5010PtsConfig) TrendDirectionBias {
	if !cfg.TrendEnabled || cfg.TrendLookback <= 0 {
		return TrendNeutral
	}
	n := st.BufferCount
	if n < cfg.TrendLookback+1 {
		return TrendNeutral
	}
	size := len(st.Buffer)

	latestIdx := (st.BufferIdx - 1 + size) % size
	latestMA := st.Buffer[latestIdx].EMA9

	oldIdx := (st.BufferIdx - cfg.TrendLookback - 1 + size*10) % size
	oldMA := st.Buffer[oldIdx].EMA9

	if latestMA == 0 || oldMA == 0 {
		return TrendNeutral
	}

	slopePct := (latestMA - oldMA) / oldMA * 100
	threshold := cfg.TrendSlopePct

	if slopePct > threshold {
		return TrendBullish
	}
	if slopePct < -threshold {
		return TrendBearish
	}
	return TrendNeutral
}

// ── Sideways detection ──

func (st *nifty5010PtsState) isSideways(cfg Nifty5010PtsConfig, candleTime time.Time) isSidewaysResult {
	n := st.BufferCount
	if n < 3 {
		return isSidewaysResult{}
	}
	size := len(st.Buffer)
	count := n
	if count > size {
		count = size
	}
	entries := make([]nifty5010PtsBufferEntry, count)
	for i := 0; i < count; i++ {
		idx := (st.BufferIdx - count + i + size*10) % size
		entries[i] = st.Buffer[idx]
	}

	// Check 1: EMA6×EMA9 cross count
	crossCount := 0
	for i := 1; i < len(entries); i++ {
		prev := entries[i-1]
		curr := entries[i]
		if (prev.EMA6 >= prev.EMA9 && curr.EMA6 < curr.EMA9) ||
			(prev.EMA6 <= prev.EMA9 && curr.EMA6 > curr.EMA9) {
			crossCount++
		}
	}
	chopFired := crossCount >= cfg.MaxChopCrosses

	// Check 2: EMA9 flat
	ma21Max, ma21Min := entries[0].EMA9, entries[0].EMA9
	for _, e := range entries[1:] {
		if e.EMA9 > ma21Max {
			ma21Max = e.EMA9
		}
		if e.EMA9 < ma21Min {
			ma21Min = e.EMA9
		}
	}
	flatFired := false
	if ma21Min > 0 {
		ma21Range := (ma21Max - ma21Min) / ma21Min * 100
		flatFired = ma21Range < cfg.MA21FlatPct
	}

	// Check 3: Narrow price range
	rangeHigh, rangeLow := entries[0].High, entries[0].Low
	lastClose := entries[len(entries)-1].EMA6
	for _, e := range entries[1:] {
		if e.High > rangeHigh {
			rangeHigh = e.High
		}
		if e.Low < rangeLow {
			rangeLow = e.Low
		}
	}
	rangeFired := false
	if lastClose > 0 && rangeHigh > 0 && rangeLow > 0 {
		priceRange := (rangeHigh - rangeLow) / lastClose * 100
		rangeFired = priceRange < cfg.NarrowRangePct
	}

	score := 0
	if chopFired {
		score++
	}
	if flatFired {
		score++
	}
	if rangeFired {
		score++
	}
	minChecks := 2
	if cfg.LunchFilterEnabled && isInLunchWindow(candleTime, cfg.LunchStartHHMM, cfg.LunchEndHHMM) {
		minChecks = 1
	}
	return isSidewaysResult{
		Sideways:   score >= minChecks,
		ChopFired:  chopFired,
		FlatFired:  flatFired,
		RangeFired: rangeFired,
		Score:      score,
	}
}

// updateEMA feeds indicators and pushes to buffer.
func (st *nifty5010PtsState) updateEMA(candle model.Candle) {
	st.EMA6.Update(candle)
	st.EMA9.Update(candle)

	if st.EMA6.Ready() && st.EMA9.Ready() {
		st.pushBuffer(nifty5010PtsBufferEntry{
			Time:  candle.TS,
			Open:  float64(candle.Open),
			High:  float64(candle.High),
			Low:   float64(candle.Low),
			Close: float64(candle.Close),
			EMA6:  st.EMA6.Value(),
			EMA9:  st.EMA9.Value(),
		})
	}
}

// ── Snapshot / Restore ──

type nifty5010PtsBufferEntrySnapshot struct {
	Time  time.Time `json:"time"`
	Open  float64   `json:"open"`
	High  float64   `json:"high"`
	Low   float64   `json:"low"`
	Close float64   `json:"close"`
	EMA6  float64   `json:"ema6"`
	EMA9  float64   `json:"ema9"`
}

type nifty5010PtsSnapshot struct {
	Key string `json:"key"`

	EMA6Snap indicator.IndicatorSnapshot `json:"ema6"`
	EMA9Snap indicator.IndicatorSnapshot `json:"ema9"`

	Buffer      []nifty5010PtsBufferEntrySnapshot `json:"buffer"`
	BufferIdx   int                               `json:"buffer_idx"`
	BufferCount int                               `json:"buffer_count"`

	Side                  PositionSide `json:"side"`
	CallSetupActive       bool         `json:"call_setup_active"`
	PutSetupActive        bool         `json:"put_setup_active"`
	EntryPrice            int64        `json:"entry_price"`
	IndexEntryPrice       int64        `json:"index_entry_price"`
	IndexBestPrice        int64        `json:"index_best_price"`
	FNOEntryPrice         int64        `json:"fno_entry_price"`
	FNOBestPrice          int64        `json:"fno_best_price"`
	ActiveFNOTarget       float64      `json:"active_fno_target"`
	CandlesSinceExit      int          `json:"candles_since_exit"`
	InCooldown            bool         `json:"in_cooldown"`
	PendingEntry          PositionSide `json:"pending_entry"`
	PendingEMADiffEntry   PositionSide `json:"pending_ema_diff_entry"`
	CrossConsumedAtBufIdx int          `json:"cross_consumed_at_buf_idx"`
	LastCloseTS           time.Time    `json:"last_close_ts"`
	ConfirmationPending   PositionSide `json:"confirmation_pending"`
	Step1Activated        bool         `json:"step1_activated"`
	Step2Activated        bool         `json:"step2_activated"`
	TrailingSL            int64        `json:"trailing_sl"`
	PullbackCandlesWaited int          `json:"pullback_candles_waited"`
	LastBigMoveHigh       float64      `json:"last_big_move_high"`
	LastBigMoveLow        float64      `json:"last_big_move_low"`
	BigMoveCandleCount    int          `json:"big_move_candle_count"`
	BestPrice             int64        `json:"best_price"`
	LastBigMoveTime       time.Time    `json:"last_big_move_time"`
	PullbackInProgress    bool         `json:"pullback_in_progress"`
	SmallCandleCount      int          `json:"small_candle_count"`
}

type nifty5010PtsStrategySnapshot struct {
	Version     int                    `json:"version"`
	Strategy    string                 `json:"strategy"`
	Instruments []nifty5010PtsSnapshot `json:"instruments"`
}

func snapshotNifty5010Pts(key string, s *nifty5010PtsState) nifty5010PtsSnapshot {
	bufSnap := make([]nifty5010PtsBufferEntrySnapshot, len(s.Buffer))
	for i, b := range s.Buffer {
		bufSnap[i] = nifty5010PtsBufferEntrySnapshot{
			Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
			EMA6: b.EMA6, EMA9: b.EMA9,
		}
	}
	return nifty5010PtsSnapshot{
		Key:                   key,
		EMA6Snap:              s.EMA6.Snapshot(),
		EMA9Snap:              s.EMA9.Snapshot(),
		Buffer:                bufSnap,
		BufferIdx:             s.BufferIdx,
		BufferCount:           s.BufferCount,
		Side:                  s.Side,
		CallSetupActive:       s.CallSetupActive,
		PutSetupActive:        s.PutSetupActive,
		EntryPrice:            s.EntryPrice,
		IndexEntryPrice:       s.IndexEntryPrice,
		IndexBestPrice:        s.IndexBestPrice,
		FNOEntryPrice:         s.FNOEntryPrice,
		FNOBestPrice:          s.FNOBestPrice,
		ActiveFNOTarget:       s.ActiveFNOTargetProfitPct,
		CandlesSinceExit:      s.CandlesSinceExit,
		InCooldown:            s.InCooldown,
		PendingEntry:          s.PendingEntry,
		PendingEMADiffEntry:   s.PendingEMADiffEntry,
		CrossConsumedAtBufIdx: s.CrossConsumedAtBufIdx,
		LastCloseTS:           s.LastCloseTS,
		ConfirmationPending:   s.ConfirmationPending,
		Step1Activated:        s.Step1Activated,
		Step2Activated:        s.Step2Activated,
		TrailingSL:            s.TrailingSL,
		PullbackCandlesWaited: s.PullbackCandlesWaited,
		LastBigMoveHigh:       s.LastBigMoveHigh,
		LastBigMoveLow:        s.LastBigMoveLow,
		BigMoveCandleCount:    s.BigMoveCandleCount,
		BestPrice:             s.BestPrice,
		LastBigMoveTime:       s.LastBigMoveTime,
		PullbackInProgress:    s.PullbackInProgress,
		SmallCandleCount:      s.SmallCandleCount,
	}
}

func restoreNifty5010Pts(snap nifty5010PtsSnapshot) *nifty5010PtsState {
	s := newNifty5010PtsState(DefaultNifty5010PtsConfig())
	_ = s.EMA6.RestoreFromSnapshot(snap.EMA6Snap)
	_ = s.EMA9.RestoreFromSnapshot(snap.EMA9Snap)

	if len(snap.Buffer) > 0 {
		s.Buffer = make([]nifty5010PtsBufferEntry, len(snap.Buffer))
		for i, b := range snap.Buffer {
			s.Buffer[i] = nifty5010PtsBufferEntry{
				Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
				EMA6: b.EMA6, EMA9: b.EMA9,
			}
		}
	}
	s.BufferIdx = snap.BufferIdx
	s.BufferCount = snap.BufferCount
	s.Side = snap.Side
	if s.Side == "" {
		s.Side = SideNone
	}
	s.CallSetupActive = snap.CallSetupActive
	s.PutSetupActive = snap.PutSetupActive
	s.EntryPrice = snap.EntryPrice
	s.IndexEntryPrice = snap.IndexEntryPrice
	s.IndexBestPrice = snap.IndexBestPrice
	s.FNOEntryPrice = snap.FNOEntryPrice
	s.FNOBestPrice = snap.FNOBestPrice
	s.ActiveFNOTargetProfitPct = snap.ActiveFNOTarget
	s.CandlesSinceExit = snap.CandlesSinceExit
	s.InCooldown = snap.InCooldown
	s.PendingEntry = snap.PendingEntry
	if s.PendingEntry == "" {
		s.PendingEntry = SideNone
	}
	s.PendingEMADiffEntry = snap.PendingEMADiffEntry
	if s.PendingEMADiffEntry == "" {
		s.PendingEMADiffEntry = SideNone
	}
	s.SLHitSide = SideNone
	// Buffer idx 0 is valid; preserve verbatim. Newer snapshots use -1 for unset.
	s.CrossConsumedAtBufIdx = snap.CrossConsumedAtBufIdx
	s.LastCloseTS = snap.LastCloseTS
	s.ConfirmationPending = snap.ConfirmationPending
	if s.ConfirmationPending == "" {
		s.ConfirmationPending = SideNone
	}
	s.Step1Activated = snap.Step1Activated
	s.Step2Activated = snap.Step2Activated
	s.TrailingSL = snap.TrailingSL
	s.PullbackCandlesWaited = snap.PullbackCandlesWaited
	s.LastBigMoveHigh = snap.LastBigMoveHigh
	s.LastBigMoveLow = snap.LastBigMoveLow
	s.BigMoveCandleCount = snap.BigMoveCandleCount
	s.BestPrice = snap.BestPrice
	s.LastBigMoveTime = snap.LastBigMoveTime
	s.PullbackInProgress = snap.PullbackInProgress
	s.SmallCandleCount = snap.SmallCandleCount
	return s
}

func marshalNifty5010PtsSnapshot(name string, instruments map[string]*nifty5010PtsState) ([]byte, error) {
	snap := nifty5010PtsStrategySnapshot{
		Version:  1,
		Strategy: name,
	}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty5010Pts(k, v))
	}
	return json.Marshal(snap)
}

func unmarshalNifty5010PtsSnapshot(data []byte) (*nifty5010PtsStrategySnapshot, error) {
	var snap nifty5010PtsStrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	// Discard any instrument with no valid EMA data (old format) — cold start fresh.
	for i := range snap.Instruments {
		if snap.Instruments[i].EMA6Snap.Period == 0 || snap.Instruments[i].EMA9Snap.Period == 0 {
			snap.Instruments[i] = nifty5010PtsSnapshot{Key: snap.Instruments[i].Key}
		}
	}
	return &snap, nil
}

func restoreNifty5010PtsInstruments(snap *nifty5010PtsStrategySnapshot) map[string]*nifty5010PtsState {
	m := make(map[string]*nifty5010PtsState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty5010Pts(is)
	}
	return m
}
