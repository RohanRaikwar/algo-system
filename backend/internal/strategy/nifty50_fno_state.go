package strategy

import (
	"encoding/json"
	"time"

	"trading-systemv1/internal/indicator"
)

// Nifty50FnOConfig holds all configurable parameters for the NIFTY50 F&O strategy.
type Nifty50FnOConfig struct {
	// ── Indicator periods ──
	EMA6Period  int // 6  (fast EMA — "White line")
	EMA9Period  int // 9  (medium EMA — "Red line")
	SMA21Period int // 21 (slow MA — "Green line")

	// ── Prefetch buffer ──
	BufferSize int // 10 — rolling buffer of closed candle indicator values

	// ── Cooldown ──
	CooldownCandles int // candles to skip after exit before re-entering (0 = disabled)

	// ── Re-entry separation guard ──
	// ── Re-entry control ──
	// ReEntryEnabled: when true, EMA6/EMA9 crossovers trigger re-entries
	// after exit. When false, only initial EMA6/SMA21 crossovers fire.
	ReEntryEnabled bool

	// MinReEntrySeparationPct: re-entry is only allowed when EMA6 is at least
	// this % of SMA21 away from SMA21. Prevents re-entries when lines are
	// clustered near SMA21 (choppy / indecisive zone). 0 = disabled.
	// Example: 0.05 means EMA6 must be ≥0.05% away from SMA21.
	MinReEntrySeparationPct float64

	// ── Min EMA6-EMA9 diff guard ──
	// MinEMA6EMA9DiffPct: entry is only allowed when abs(EMA6 - EMA9) / EMA9
	// is at least this %. Prevents entries when the two EMAs are nearly touching
	// (indecisive zone). 0 = disabled.
	// Example: 0.02 means EMA6 must be ≥0.02% away from EMA9.
	MinEMA6EMA9DiffPct float64

	// ── Sideways market filter ──
	// Filter activates if 2 out of 3 checks are true:
	//   1. Cross count: EMA6×EMA9 crossed ≥ MaxChopCrosses times in last N candles
	//   2. MA21 flat: MA21 slope < MA21FlatPct % of its value
	//   3. Narrow range: high-low range of last N candles < NarrowRangePct % of close
	// Set SidewaysEnabled=false to disable entirely.
	SidewaysEnabled bool
	MaxChopCrosses  int     // EMA6×EMA9 crossovers in last 10 candles to be "choppy" (default: 3)
	MA21FlatPct     float64 // MA21 max-min / MA21 < this % → flat (default: 0.05%)
	NarrowRangePct  float64 // (high-low)/close < this % → narrow (default: 0.30%)

	// ── Momentum bypass ──
	// If abs(close - MA21) / MA21 > this %, skip the sideways filter entirely.
	// Catches breakouts that the stale buffer would otherwise block.
	// 0 = disabled.
	MomentumBypassPct float64 // default: 0.15%

	// ── Time-of-day filter ──
	SkipFirstMinutes int // skip entries in first N minutes after 9:15
	SkipLastMinutes  int // skip entries in last N minutes before 15:30

	// ── Lunch-hour sideways override ──
	// During LunchStart–LunchEnd IST, the sideways filter is stricter:
	// only 1 out of 3 checks needs to fire (vs the normal 2/3 requirement).
	LunchFilterEnabled bool   // true = stricter sideways during lunch
	LunchStartHHMM     string // "1200" (inclusive)
	LunchEndHHMM       string // "1330" (exclusive)

	// ── Trend direction filter ──
	TrendEnabled  bool
	TrendLookback int     // candles to look back for MA21 slope (default: 50 = 50 min)
	TrendSlopePct float64 // MA21 must move > this % to be "trending" (default: 0.05%)

	// ── Hard Stop Loss (index price) ──
	HardSLPct      float64 // hard SL as % of entry price (e.g. 0.15 = 0.15%) — 0 = disabled
	ReEntryAfterSL bool    // if true, allow re-entry after SL hit via momentum bypass

	// ── Trailing Stop Loss (index price) ──
	TrailSLPct    float64 // trailing SL distance as % of best price (0 = disabled)
	TrailStartPct float64 // start trailing only after trade is this % in profit (0 = trail immediately)

	// ── INDEX dual-layer SL ──
	IndexHardSLPct     float64 // hard SL as % of index entry price (0 = disabled)
	IndexTrailSLPct    float64 // trailing SL distance as % of index best price (0 = disabled)
	IndexTrailStartPct float64 // start trailing after index is this % in profit

	// ── FNO dual-layer SL ──
	FNOHardSLPct     float64 // hard SL as % of FNO entry price (0 = disabled)
	FNOTrailSLPct    float64 // trailing SL distance as % of FNO best price (0 = disabled)
	FNOTrailStartPct float64 // start trailing after FNO is this % in profit

	// ── FNO Target Profit ──
	// Exit when FNO premium gains this % of entry price. 1.5 = 1.5%.
	// Dynamic target based on entry price instead of fixed points.
	FNOTargetProfitPct float64

	// ── FNO Fixed Stop Loss ──
	// Hard SL at FNO entry premium minus this many paise. 1000 = ₹10.
	// Enforced both at exchange via paired GTT and in-process via OnTick.
	// 0 = disabled.
	FNOStopLossPaise int64

	// ── FNO token identifiers ──
	FNOCallToken string // FNO CALL option token for SL tracking
	FNOPutToken  string // FNO PUT option token for SL tracking

	// ── Consecutive candle exit ──
	ConsecCandleExit bool

	// ── Candle filter ──
	IndexToken string // e.g. "NSE:99926000"

	// ── Target profit exit (index points, paise) ──
	// If > 0, exit when the trade is this many paise in profit on the index.
	// Example: 400 = 4 NIFTY points.
	TargetProfitPts int64

	// ── Trend/Value Filters ──
	VwapEnabled  bool
	AdxThreshold int

	// ── Name override ──
	// If set, strategy.Name() returns this instead of the default.
	NameOverride string
}

// DefaultNifty50FnOConfig returns the current live defaults.
// These defaults bias toward holding winners longer:
// 1. Consecutive-candle exits are disabled because they were cutting trades early.
// 2. Trailing SL is disabled by default for the same reason.
// 3. Late-entry filtering remains configurable, but is off by default.
func DefaultNifty50FnOConfig() Nifty50FnOConfig {
	return Nifty50FnOConfig{
		EMA6Period:      6,
		EMA9Period:      9,
		SMA21Period:     21,
		BufferSize:      10,
		CooldownCandles: 0,
		// Re-entry — disabled
		ReEntryEnabled:          false,
		MinReEntrySeparationPct: 0,     // disabled
		MinEMA6EMA9DiffPct:      0.005, // 0.005% minimum diff
		// Sideways filter — disabled
		SidewaysEnabled:   false,
		MaxChopCrosses:    3,
		MA21FlatPct:       0.05,
		NarrowRangePct:    0.30,
		MomentumBypassPct: 0, // disabled — no momentum bypass
		// Time-of-day filter — disabled
		SkipFirstMinutes: 0,
		SkipLastMinutes:  0,
		// Lunch-hour filter — disabled
		LunchFilterEnabled: false,
		LunchStartHHMM:     "1200",
		LunchEndHHMM:       "1330",
		// Trend direction filter — disabled
		TrendEnabled:  false,
		TrendLookback: 50,
		TrendSlopePct: 0.05,
		// Hard stop loss — disabled (crossover-only exit)
		HardSLPct:      0,
		ReEntryAfterSL: false,
		// Trailing stop loss — disabled
		TrailSLPct:    0,
		TrailStartPct: 0,
		// INDEX dual SL — disabled
		IndexHardSLPct:     0,
		IndexTrailSLPct:    0,
		IndexTrailStartPct: 0,
		// FNO dual SL — disabled (crossover-only exit)
		FNOHardSLPct:     0,
		FNOTrailSLPct:    0,
		FNOTrailStartPct: 0,
		// FNO Target Profit — 1.5% of entry price
		FNOTargetProfitPct: 1.5,
		// FNO Fixed Stop Loss — ₹10 (1000 paise) below FNO entry price
		FNOStopLossPaise: 1000,
		// FNO tokens
		FNOCallToken: "",
		FNOPutToken:  "",
		// Consecutive candle exit — disabled
		ConsecCandleExit: false,
		// Candle filter — only generate signals from index candle
		IndexToken:   "NSE:99926000",
		VwapEnabled:  false, // disabled
		AdxThreshold: 0,     // disabled
	}
}

// ── Prefetch buffer entry ──

// nifty50FnOBufferEntry stores indicator values for one closed candle.
type nifty50FnOBufferEntry struct {
	Time  time.Time
	Open  float64 // candle open (paise)
	High  float64 // candle high (paise)
	Low   float64 // candle low  (paise)
	Close float64 // candle close (paise)
	EMA6  float64
	EMA9  float64
	MA21  float64
}

// upperBody returns max(open, close) — the upper edge of the candle body.
func (e nifty50FnOBufferEntry) upperBody() float64 {
	if e.Close > e.Open {
		return e.Close
	}
	return e.Open
}

// lowerBody returns min(open, close) — the lower edge of the candle body.
func (e nifty50FnOBufferEntry) lowerBody() float64 {
	if e.Close < e.Open {
		return e.Close
	}
	return e.Open
}

// ── Per-instrument state ──

// nifty50FnOState holds isolated indicator + position state for one instrument.
type nifty50FnOState struct {
	// ── Indicators ──
	EMA6  *indicator.EMA
	EMA9  *indicator.EMA
	SMA21 *indicator.SMA
	VWAP  *indicator.VWAP
	ADX   *indicator.ADX

	// ── 10-value prefetch buffer (ring buffer) ──
	Buffer      []nifty50FnOBufferEntry
	BufferIdx   int // next write position
	BufferCount int // total values written (capped at BufferSize for readiness)

	// ── Position state machine ──
	Side PositionSide // NONE | CALL | PUT

	// ── Setup tracking (for sequential crossover + re-entry logic) ──
	CallSetupActive bool // true when both EMA6 & EMA9 are above SMA21
	PutSetupActive  bool // true when both EMA6 & EMA9 are below SMA21

	// ── Position details (index price) ──
	EntryPrice      int64
	BestPrice       int64 // best index price for trailing SL
	IndexEntryPrice int64 // alias for index entry (dual-SL layer)
	IndexBestPrice  int64 // best index price for index trail SL

	// ── FNO price tracking (for FNO-based SL) ──
	FNOEntryPrice int64 // FNO option premium at entry
	FNOBestPrice  int64 // best FNO premium

	// ── Cooldown tracking ──
	CandlesSinceExit    int
	InCooldown          bool
	PendingEntry        PositionSide // queued crossover during cooldown (NONE = no pending)
	PendingEMADiffEntry PositionSide // queued entry waiting for EMA6-EMA9 diff to widen (NONE = no pending)

	// ── Stop Loss tracking ──
	SLHitSide PositionSide // side that was stopped out (for re-entry gating)

	// ── Crossover consumption ──
	// After an exit, we record BufferIdx here so lookback won't re-trigger
	CrossConsumedAtBufIdx int

	// ── Dedup ──
	LastCloseTS time.Time
}

// newNifty50FnOState creates a fresh state with the given config.
func newNifty50FnOState(cfg Nifty50FnOConfig) *nifty50FnOState {
	// Auto-expand buffer if trend filter needs more lookback than the base size
	bufSize := cfg.BufferSize
	if cfg.TrendEnabled && cfg.TrendLookback+1 > bufSize {
		bufSize = cfg.TrendLookback + 1
	}
	if bufSize <= 0 {
		bufSize = 10
	}
	return &nifty50FnOState{
		EMA6:   indicator.NewEMA(cfg.EMA6Period),
		EMA9:   indicator.NewEMA(cfg.EMA9Period),
		SMA21:  indicator.NewSMA(cfg.SMA21Period),
		VWAP:                  indicator.NewVWAP(),
		ADX:                   indicator.NewADX(14),
		Buffer:                make([]nifty50FnOBufferEntry, bufSize),
		Side:                  SideNone,
		CrossConsumedAtBufIdx: -1,
	}
}

// bufferReady returns true when at least 2 points exist for crossover detection.
func (st *nifty50FnOState) bufferReady() bool {
	return st.BufferCount >= 2
}

// bufferReady3 returns true when at least 3 candles exist for the 3-candle exit pattern.
func (st *nifty50FnOState) bufferReady3() bool {
	return st.BufferCount >= 3
}

// pushBuffer appends a new entry to the ring buffer.
func (st *nifty50FnOState) pushBuffer(entry nifty50FnOBufferEntry) {
	st.Buffer[st.BufferIdx] = entry
	st.BufferIdx = (st.BufferIdx + 1) % len(st.Buffer)
	if st.BufferCount < len(st.Buffer) {
		st.BufferCount++
	}
}

// prevAndCurr returns the previous (n-2) and current (n-1) buffer entries.
// Caller must ensure bufferReady() is true.
func (st *nifty50FnOState) prevAndCurr() (prev, curr nifty50FnOBufferEntry) {
	size := len(st.Buffer)
	currIdx := (st.BufferIdx - 1 + size) % size
	prevIdx := (st.BufferIdx - 2 + size) % size
	return st.Buffer[prevIdx], st.Buffer[currIdx]
}

// threeBack returns the last 3 buffer entries: candle3 (oldest), candle2, candle1 (newest/live).
// Caller must ensure bufferReady3() is true.
func (st *nifty50FnOState) threeBack() (candle3, candle2, candle1 nifty50FnOBufferEntry) {
	size := len(st.Buffer)
	idx1 := (st.BufferIdx - 1 + size) % size
	idx2 := (st.BufferIdx - 2 + size) % size
	idx3 := (st.BufferIdx - 3 + size) % size
	return st.Buffer[idx3], st.Buffer[idx2], st.Buffer[idx1]
}

// ── Crossover helpers (10-candle buffer lookback) ──

func (st *nifty50FnOState) crossedAboveInBuf(
	getA, getB func(nifty50FnOBufferEntry) float64,
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
		if st.CrossConsumedAtBufIdx >= 0 && idx == st.CrossConsumedAtBufIdx {
			return false
		}
	}
	return false
}

func (st *nifty50FnOState) crossedBelowInBuf(
	getA, getB func(nifty50FnOBufferEntry) float64,
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
		if st.CrossConsumedAtBufIdx >= 0 && idx == st.CrossConsumedAtBufIdx {
			return false
		}
	}
	return false
}

// ema6CrossedAboveMA21: EMA6 crossed above SMA21
func (st *nifty50FnOState) ema6CrossedAboveMA21() bool {
	return st.crossedAboveInBuf(
		func(e nifty50FnOBufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOBufferEntry) float64 { return e.MA21 },
	)
}

// ema6CrossedBelowMA21: EMA6 crossed below SMA21
func (st *nifty50FnOState) ema6CrossedBelowMA21() bool {
	return st.crossedBelowInBuf(
		func(e nifty50FnOBufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOBufferEntry) float64 { return e.MA21 },
	)
}

// ema6CrossedAboveEMA9: EMA6 crossed above EMA9
func (st *nifty50FnOState) ema6CrossedAboveEMA9() bool {
	return st.crossedAboveInBuf(
		func(e nifty50FnOBufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOBufferEntry) float64 { return e.EMA9 },
	)
}

// ema6CrossedBelowEMA9: EMA6 crossed below EMA9
func (st *nifty50FnOState) ema6CrossedBelowEMA9() bool {
	return st.crossedBelowInBuf(
		func(e nifty50FnOBufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOBufferEntry) float64 { return e.EMA9 },
	)
}

// isSidewaysResult holds which checks fired for logging.
type isSidewaysResult struct {
	Sideways   bool
	ChopFired  bool
	FlatFired  bool
	RangeFired bool
	Score      int // how many checks fired (0=clean trend, 3=deep chop)
}

// isSideways runs the 3-check sideways filter against the recent buffer.
// Returns true (with reason flags) if 2 or more of the 3 checks are active.
//
//  1. Cross count  — EMA6 flipped over EMA9 ≥ MaxChopCrosses times in last N candles
//  2. MA21 flat    — MA21 range/MA21 < MA21FlatPct %
//  3. Narrow range — (high-low)/close < NarrowRangePct %
func (st *nifty50FnOState) isSideways(cfg Nifty50FnOConfig, candleTime time.Time) isSidewaysResult {
	n := st.BufferCount
	if n < 3 {
		return isSidewaysResult{} // not enough data yet
	}
	size := len(st.Buffer)
	// Collect ordered entries (oldest → newest), up to BufferSize
	count := n
	if count > size {
		count = size
	}
	entries := make([]nifty50FnOBufferEntry, count)
	for i := 0; i < count; i++ {
		idx := (st.BufferIdx - count + i + size*10) % size
		entries[i] = st.Buffer[idx]
	}

	// ── Check 1: EMA6×EMA9 cross count ──
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

	// ── Check 2: MA21 flat ──
	ma21Max, ma21Min := entries[0].MA21, entries[0].MA21
	for _, e := range entries[1:] {
		if e.MA21 > ma21Max {
			ma21Max = e.MA21
		}
		if e.MA21 < ma21Min {
			ma21Min = e.MA21
		}
	}
	flatFired := false
	if ma21Min > 0 {
		ma21Range := (ma21Max - ma21Min) / ma21Min * 100
		flatFired = ma21Range < cfg.MA21FlatPct
	}

	// ── Check 3: Narrow price range ──
	rangeHigh, rangeLow := entries[0].High, entries[0].Low
	lastClose := entries[len(entries)-1].EMA6 // EMA6 ≈ close price
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

	// 2 out of 3 = sideways (1 out of 3 during lunch window)
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
		minChecks = 1 // stricter: any single check blocks entry
	}
	return isSidewaysResult{
		Sideways:   score >= minChecks,
		ChopFired:  chopFired,
		FlatFired:  flatFired,
		RangeFired: rangeFired,
		Score:      score,
	}
}

// parseHHMM converts "1200" → 1200 (int). Returns 0 on failure.
func parseHHMM(s string) int {
	v := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		v = v*10 + int(c-'0')
	}
	return v
}

// isInLunchWindow checks whether a time (already in IST) falls within the
// configured lunch window [LunchStartHHMM, LunchEndHHMM).
func isInLunchWindow(t time.Time, startHHMM, endHHMM string) bool {
	hhmm := t.Hour()*100 + t.Minute()
	return hhmm >= parseHHMM(startHHMM) && hhmm < parseHHMM(endHHMM)
}

// EntryMarketState is the live market regime seen by the strategy at entry time.
type EntryMarketState string

const (
	EntryMarketStateTrending EntryMarketState = "trending"
	EntryMarketStateSideways EntryMarketState = "sideways"
	EntryMarketStateChoppy   EntryMarketState = "choppy"
	EntryMarketStateRange    EntryMarketState = "range" // BB+RSI mean-reversion sub-strategy
)

// EntryStrength is the normalized strength bucket used for option automation.
type EntryStrength string

const (
	EntryStrengthLow    EntryStrength = "low"
	EntryStrengthMedium EntryStrength = "medium"
	EntryStrengthHigh   EntryStrength = "high"
)

// EntryAutomationContext exposes the strategy's live market context for
// downstream expiry/strike selection.
type EntryAutomationContext struct {
	Available        bool
	MarketState      EntryMarketState
	TrendStrength    EntryStrength
	MomentumStrength EntryStrength
	TrendSlopePct    float64
	MomentumPct      float64
}

func (st *nifty50FnOState) latestBufferEntry() (nifty50FnOBufferEntry, bool) {
	if st.BufferCount == 0 || len(st.Buffer) == 0 {
		return nifty50FnOBufferEntry{}, false
	}
	size := len(st.Buffer)
	idx := (st.BufferIdx - 1 + size) % size
	return st.Buffer[idx], true
}

func (st *nifty50FnOState) trendSlopePct(cfg Nifty50FnOConfig) (float64, bool) {
	if st.BufferCount < 2 || len(st.Buffer) < 2 {
		return 0, false
	}

	lookback := cfg.TrendLookback
	if lookback <= 0 {
		lookback = len(st.Buffer) - 1
	}

	maxLookback := st.BufferCount - 1
	if maxLookback > len(st.Buffer)-1 {
		maxLookback = len(st.Buffer) - 1
	}
	if maxLookback <= 0 {
		return 0, false
	}
	if lookback > maxLookback {
		lookback = maxLookback
	}
	if lookback <= 0 {
		return 0, false
	}

	size := len(st.Buffer)
	latestIdx := (st.BufferIdx - 1 + size) % size
	oldIdx := (st.BufferIdx - lookback - 1 + size*10) % size

	latestMA21 := st.Buffer[latestIdx].MA21
	oldMA21 := st.Buffer[oldIdx].MA21
	if latestMA21 == 0 || oldMA21 == 0 {
		return 0, false
	}

	return (latestMA21 - oldMA21) / oldMA21 * 100, true
}

func (st *nifty50FnOState) momentumDistancePct() (float64, bool) {
	latest, ok := st.latestBufferEntry()
	if !ok || latest.MA21 <= 0 {
		return 0, false
	}
	dist := (latest.Close - latest.MA21) / latest.MA21 * 100
	if dist < 0 {
		dist = -dist
	}
	return dist, true
}

func (st *nifty50FnOState) entryAutomationContext(cfg Nifty50FnOConfig) EntryAutomationContext {
	latest, ok := st.latestBufferEntry()
	if !ok || st.BufferCount < 3 || latest.Time.IsZero() {
		return EntryAutomationContext{}
	}

	sw := st.isSideways(cfg, latest.Time)
	marketState := EntryMarketStateTrending
	switch {
	case sw.Sideways && sw.ChopFired:
		marketState = EntryMarketStateChoppy
	case sw.Sideways:
		marketState = EntryMarketStateSideways
	}

	trendSlopePct, trendOK := st.trendSlopePct(cfg)
	momentumPct, momentumOK := st.momentumDistancePct()
	if !trendOK {
		trendSlopePct = 0
	}
	if !momentumOK {
		momentumPct = 0
	}

	trendThreshold := cfg.TrendSlopePct
	if trendThreshold <= 0 {
		trendThreshold = 0.05
	}
	momentumThreshold := cfg.MomentumBypassPct
	if momentumThreshold <= 0 {
		momentumThreshold = 0.10
	}

	return EntryAutomationContext{
		Available:        true,
		MarketState:      marketState,
		TrendStrength:    classifyEntryStrength(trendSlopePct, trendThreshold),
		MomentumStrength: classifyEntryStrength(momentumPct, momentumThreshold),
		TrendSlopePct:    trendSlopePct,
		MomentumPct:      momentumPct,
	}
}

func classifyEntryStrength(value, mediumThreshold float64) EntryStrength {
	if value < 0 {
		value = -value
	}
	if mediumThreshold <= 0 {
		mediumThreshold = 0.10
	}
	switch {
	case value >= mediumThreshold*2:
		return EntryStrengthHigh
	case value >= mediumThreshold:
		return EntryStrengthMedium
	default:
		return EntryStrengthLow
	}
}

// ── Trend direction filter ──

// TrendDirectionBias is the detected intraday trend direction.
type TrendDirectionBias int

const (
	TrendNeutral TrendDirectionBias = iota // MA21 flat — allow CALL and PUT
	TrendBullish                           // MA21 rising — CALL only, block PUT
	TrendBearish                           // MA21 falling — PUT only, block CALL
)

// trendBias calculates the current trend direction by comparing the latest
// MA21 with the MA21 from TrendLookback candles ago.
// Returns TrendBullish / TrendBearish / TrendNeutral.
func (st *nifty50FnOState) trendBias(cfg Nifty50FnOConfig) TrendDirectionBias {
	if !cfg.TrendEnabled || cfg.TrendLookback <= 0 {
		return TrendNeutral
	}
	n := st.BufferCount
	if n < cfg.TrendLookback+1 {
		return TrendNeutral // not enough data yet — don't restrict
	}
	size := len(st.Buffer)

	// Latest MA21 — most recently written entry
	latestIdx := (st.BufferIdx - 1 + size) % size
	latestMA21 := st.Buffer[latestIdx].MA21

	// MA21 from TrendLookback candles ago
	oldIdx := (st.BufferIdx - cfg.TrendLookback - 1 + size*10) % size
	oldMA21 := st.Buffer[oldIdx].MA21

	if latestMA21 == 0 || oldMA21 == 0 {
		return TrendNeutral
	}

	slopePct := (latestMA21 - oldMA21) / oldMA21 * 100
	threshold := cfg.TrendSlopePct

	if slopePct > threshold {
		return TrendBullish // MA21 rose >threshold% → uptrend
	}
	if slopePct < -threshold {
		return TrendBearish // MA21 fell >threshold% → downtrend
	}
	return TrendNeutral // flat → no bias
}

// ── Snapshot / Restore for crash recovery ──

type nifty50FnOBufferEntrySnapshot struct {
	Time  time.Time `json:"time"`
	Open  float64   `json:"open"`
	High  float64   `json:"high"`
	Low   float64   `json:"low"`
	Close float64   `json:"close"`
	EMA6  float64   `json:"ema6"`
	EMA9  float64   `json:"ema9"`
	MA21  float64   `json:"ma21"`
}

type nifty50FnOSnapshot struct {
	Key string `json:"key"`

	EMA6Snap  indicator.IndicatorSnapshot `json:"ema6"`
	EMA9Snap  indicator.IndicatorSnapshot `json:"ema9"`
	SMA21Snap indicator.IndicatorSnapshot `json:"sma21"`
	VWAPSnap  indicator.IndicatorSnapshot `json:"vwap"`
	ADXSnap   indicator.IndicatorSnapshot `json:"adx"`

	Buffer      []nifty50FnOBufferEntrySnapshot `json:"buffer"`
	BufferIdx   int                             `json:"buffer_idx"`
	BufferCount int                             `json:"buffer_count"`

	Side                PositionSide `json:"side"`
	CallSetupActive     bool         `json:"call_setup_active"`
	PutSetupActive      bool         `json:"put_setup_active"`
	EntryPrice          int64        `json:"entry_price"`
	BestPrice           int64        `json:"best_price"`
	IndexEntryPrice     int64        `json:"index_entry_price"`
	IndexBestPrice      int64        `json:"index_best_price"`
	FNOEntryPrice       int64        `json:"fno_entry_price"`
	FNOBestPrice        int64        `json:"fno_best_price"`
	SLHitSide           PositionSide `json:"sl_hit_side"`
	CandlesSinceExit      int          `json:"candles_since_exit"`
	InCooldown            bool         `json:"in_cooldown"`
	PendingEntry          PositionSide `json:"pending_entry"`
	PendingEMADiffEntry   PositionSide `json:"pending_ema_diff_entry"`
	CrossConsumedAtBufIdx int          `json:"cross_consumed_at_buf_idx"`
	LastCloseTS           time.Time    `json:"last_close_ts"`
}

type nifty50FnOStrategySnapshot struct {
	Version     int                  `json:"version"`
	Strategy    string               `json:"strategy"`
	Instruments []nifty50FnOSnapshot `json:"instruments"`
}

func snapshotNifty50FnO(key string, s *nifty50FnOState) nifty50FnOSnapshot {
	bufSnap := make([]nifty50FnOBufferEntrySnapshot, len(s.Buffer))
	for i, b := range s.Buffer {
		bufSnap[i] = nifty50FnOBufferEntrySnapshot{
			Time:  b.Time,
			Open:  b.Open,
			High:  b.High,
			Low:   b.Low,
			Close: b.Close,
			EMA6:  b.EMA6,
			EMA9:  b.EMA9,
			MA21:  b.MA21,
		}
	}
	return nifty50FnOSnapshot{
		Key:                 key,
		EMA6Snap:            s.EMA6.Snapshot(),
		EMA9Snap:            s.EMA9.Snapshot(),
		SMA21Snap:           s.SMA21.Snapshot(),
		VWAPSnap:            s.VWAP.Snapshot(),
		ADXSnap:             s.ADX.Snapshot(),
		Buffer:              bufSnap,
		BufferIdx:           s.BufferIdx,
		BufferCount:         s.BufferCount,
		Side:                s.Side,
		CallSetupActive:     s.CallSetupActive,
		PutSetupActive:      s.PutSetupActive,
		EntryPrice:          s.EntryPrice,
		BestPrice:           s.BestPrice,
		IndexEntryPrice:     s.IndexEntryPrice,
		IndexBestPrice:      s.IndexBestPrice,
		FNOEntryPrice:       s.FNOEntryPrice,
		FNOBestPrice:        s.FNOBestPrice,
		SLHitSide:           s.SLHitSide,
		CandlesSinceExit:      s.CandlesSinceExit,
		InCooldown:            s.InCooldown,
		PendingEntry:          s.PendingEntry,
		PendingEMADiffEntry:   s.PendingEMADiffEntry,
		CrossConsumedAtBufIdx: s.CrossConsumedAtBufIdx,
		LastCloseTS:           s.LastCloseTS,
	}
}

func restoreNifty50FnO(snap nifty50FnOSnapshot) *nifty50FnOState {
	s := newNifty50FnOState(DefaultNifty50FnOConfig())
	_ = s.EMA6.RestoreFromSnapshot(snap.EMA6Snap)
	_ = s.EMA9.RestoreFromSnapshot(snap.EMA9Snap)
	_ = s.SMA21.RestoreFromSnapshot(snap.SMA21Snap)
	_ = s.VWAP.RestoreFromSnapshot(snap.VWAPSnap)
	_ = s.ADX.RestoreFromSnapshot(snap.ADXSnap)

	// Restore buffer
	if len(snap.Buffer) > 0 {
		s.Buffer = make([]nifty50FnOBufferEntry, len(snap.Buffer))
		for i, b := range snap.Buffer {
			s.Buffer[i] = nifty50FnOBufferEntry{
				Time:  b.Time,
				Open:  b.Open,
				High:  b.High,
				Low:   b.Low,
				Close: b.Close,
				EMA6:  b.EMA6,
				EMA9:  b.EMA9,
				MA21:  b.MA21,
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
	s.BestPrice = snap.BestPrice
	s.IndexEntryPrice = snap.IndexEntryPrice
	s.IndexBestPrice = snap.IndexBestPrice
	s.FNOEntryPrice = snap.FNOEntryPrice
	s.FNOBestPrice = snap.FNOBestPrice
	s.SLHitSide = snap.SLHitSide
	s.CandlesSinceExit = snap.CandlesSinceExit
	s.InCooldown = snap.InCooldown
	s.PendingEntry = snap.PendingEntry
	s.PendingEMADiffEntry = snap.PendingEMADiffEntry
	s.CrossConsumedAtBufIdx = snap.CrossConsumedAtBufIdx
	if s.CrossConsumedAtBufIdx == 0 && snap.Side == SideNone {
		s.CrossConsumedAtBufIdx = -1
	}
	s.LastCloseTS = snap.LastCloseTS
	return s
}

func marshalNifty50FnOSnapshot(name string, instruments map[string]*nifty50FnOState) ([]byte, error) {
	snap := nifty50FnOStrategySnapshot{
		Version:  1,
		Strategy: name,
	}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty50FnO(k, v))
	}
	return json.Marshal(snap)
}

func unmarshalNifty50FnOSnapshot(data []byte) (*nifty50FnOStrategySnapshot, error) {
	var snap nifty50FnOStrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func restoreNifty50FnOInstruments(snap *nifty50FnOStrategySnapshot) map[string]*nifty50FnOState {
	m := make(map[string]*nifty50FnOState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty50FnO(is)
	}
	return m
}
