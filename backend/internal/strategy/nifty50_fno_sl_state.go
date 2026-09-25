package strategy

import (
	"encoding/json"
	"log"
	"time"

	"trading-systemv1/internal/indicator"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 FnO SL — State & Configuration
//
//  Entry:  EMA6 × EMA9 crossover
//            EMA6 > EMA9 → BUY CALL
//            EMA9 > EMA6 → BUY PUT
//  Exit:   Opposite EMA6 × EMA9 crossover (or FNO SL)
//  SL:     FNO Trail SL + FNO Hard SL only (index SL disabled)
// ════════════════════════════════════════════════════════════════════

// Nifty50FnOSLConfig holds all configurable parameters for the NIFTY50_FNO_SL strategy.
type Nifty50FnOSLConfig struct {
	// ── Indicator periods ──
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
	// Entry deferred when abs(EMA6-EMA9)/EMA9 < this %. 0 = disabled.
	MinEMA6EMA9DiffPct float64

	// ── Sideways market filter ──
	SidewaysEnabled bool
	MaxChopCrosses  int
	MA21FlatPct     float64
	NarrowRangePct  float64

	// ── Momentum bypass ──
	// If abs(close - EMA9) / EMA9 > this %, skip sideways filter.
	// 0 = disabled.
	MomentumBypassPct float64

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

	// ── INDEX Stop Loss — disabled (set to 0) ──
	IndexHardSLPct     float64
	IndexTrailSLPct    float64
	IndexTrailStartPct float64

	// ── FNO Stop Loss (SL on FNO option premium price) ──
	FNOHardSLPct     float64 // hard SL as % of FNO entry price (0 = disabled)
	FNOTrailSLPct    float64 // trailing SL distance as % of FNO best price (0 = disabled)
	FNOTrailStartPct float64 // start trailing after FNO is this % in profit

	// ── FNO Target Profit ──
	FNOTargetProfitPct float64 // target profit as % of FNO entry price (0 = disabled)

	// ── Re-entry after SL ──
	ReEntryAfterSL bool

	// ── Resistance / Support filter ──
	ResistanceLevels      []float64
	SupportLevels         []float64
	ResistanceDistancePct float64
	SupportDistancePct    float64

	// ── FNO token identifiers ──
	FNOCallToken string
	FNOPutToken  string

	// ── Candle filter ──
	IndexToken string // e.g. "NSE:99926000"
}

// DefaultNifty50FnOSLConfig returns sensible defaults.
func DefaultNifty50FnOSLConfig() Nifty50FnOSLConfig {
	return Nifty50FnOSLConfig{
		EMA6Period: 6,
		EMA9Period: 9,
		BufferSize: 10,
		// Cooldown
		CooldownCandles: 0,
		// Re-entry — disabled
		ReEntryEnabled:          true,
		MinReEntrySeparationPct: 0,      // disabled
		MinEMA6EMA9DiffPct:      0.0001, // 0.0001% minimum diff (very tight)
		// Sideways filter — disabled
		SidewaysEnabled: false,
		MaxChopCrosses:  3,
		MA21FlatPct:     0.05,
		NarrowRangePct:  0.30,
		// Momentum bypass — disabled
		MomentumBypassPct: 0,
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
		// INDEX SL — disabled (FNO SL only)
		IndexHardSLPct:     0,
		IndexTrailSLPct:    0,
		IndexTrailStartPct: 0,
		// FNO SL — active
		FNOHardSLPct:     8.0,
		FNOTrailSLPct:    8,
		FNOTrailStartPct: 5, // start trailing after 5% profit
		// FNO Target Profit — 1.5% of entry price
		FNOTargetProfitPct: 1.5,
		// Re-entry after SL
		ReEntryAfterSL: true,
		// Resistance / Support filter — disabled (no levels)
		ResistanceLevels:      nil,
		SupportLevels:         nil,
		ResistanceDistancePct: 0.10,
		SupportDistancePct:    0.10,
		// FNO tokens — set at runtime
		FNOCallToken: "",
		FNOPutToken:  "",
		// Index filter
		IndexToken: "NSE:99926000",
	}
}

// ── Buffer entry ──

type nifty50FnOSLBufferEntry struct {
	Time  time.Time
	Open  float64
	High  float64
	Low   float64
	Close float64
	EMA6  float64 // fast EMA (period 6)
	EMA9  float64 // slow EMA (period 9)
}

// ── Per-instrument state ──

type nifty50FnOSLState struct {
	// ── Indicators ──
	EMA6 *indicator.EMA // fast EMA6
	EMA9 *indicator.EMA // slow EMA9

	// ── Ring buffer ──
	Buffer      []nifty50FnOSLBufferEntry
	BufferIdx   int
	BufferCount int

	// ── Position state machine ──
	Side PositionSide

	// ── Setup tracking ──
	CallSetupActive bool
	PutSetupActive  bool

	// ── Index price tracking ──
	IndexEntryPrice int64
	IndexBestPrice  int64

	// ── FNO price tracking ──
	FNOEntryPrice int64
	FNOBestPrice  int64

	// ── Cooldown tracking ──
	CandlesSinceExit    int
	InCooldown          bool
	PendingEntry        PositionSide
	PendingEMADiffEntry PositionSide

	// ── Stop Loss tracking ──
	SLHitSide PositionSide

	// ── Crossover consumption ──
	// After an SL/trail exit, we record BufferIdx here so the lookback
	// helpers refuse to detect the same crossover twice. -1 = no limit.
	CrossConsumedAtBufIdx int

	// ── Dynamic Targets ──
	DynamicIndexTarget float64

	// ── Fresh-day cross guard ──
	LastTradingDate time.Time

	// ── Dedup ──
	LastCloseTS time.Time
}

func newNifty50FnOSLState(cfg Nifty50FnOSLConfig) *nifty50FnOSLState {
	bufSize := cfg.BufferSize
	if cfg.TrendEnabled && cfg.TrendLookback+1 > bufSize {
		bufSize = cfg.TrendLookback + 1
	}
	if bufSize <= 0 {
		bufSize = 10
	}
	return &nifty50FnOSLState{
		EMA6:                  indicator.NewEMA(cfg.EMA6Period),
		EMA9:                  indicator.NewEMA(cfg.EMA9Period),
		Buffer:                make([]nifty50FnOSLBufferEntry, bufSize),
		Side:                  SideNone,
		SLHitSide:             SideNone,
		CrossConsumedAtBufIdx: -1,
	}
}

func (st *nifty50FnOSLState) bufferReady() bool {
	return st.BufferCount >= 2
}

func (st *nifty50FnOSLState) bufferReady3() bool {
	return st.BufferCount >= 3
}

func (st *nifty50FnOSLState) pushBuffer(entry nifty50FnOSLBufferEntry) {
	st.Buffer[st.BufferIdx] = entry
	st.BufferIdx = (st.BufferIdx + 1) % len(st.Buffer)
	if st.BufferCount < len(st.Buffer) {
		st.BufferCount++
	}
}

func (st *nifty50FnOSLState) prevAndCurr() (prev, curr nifty50FnOSLBufferEntry) {
	size := len(st.Buffer)
	currIdx := (st.BufferIdx - 1 + size) % size
	prevIdx := (st.BufferIdx - 2 + size) % size
	return st.Buffer[prevIdx], st.Buffer[currIdx]
}

func (st *nifty50FnOSLState) threeBack() (candle3, candle2, candle1 nifty50FnOSLBufferEntry) {
	size := len(st.Buffer)
	idx1 := (st.BufferIdx - 1 + size) % size
	idx2 := (st.BufferIdx - 2 + size) % size
	idx3 := (st.BufferIdx - 3 + size) % size
	return st.Buffer[idx3], st.Buffer[idx2], st.Buffer[idx1]
}

// ── 10-candle crossover helpers ──

func (st *nifty50FnOSLState) crossedAboveInBuf(
	getA, getB func(nifty50FnOSLBufferEntry) float64,
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

func (st *nifty50FnOSLState) crossedBelowInBuf(
	getA, getB func(nifty50FnOSLBufferEntry) float64,
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

// ── EMA6 × EMA9 crossover helpers ──

// ema6CrossedAboveEMA9: EMA6 crossed above EMA9 (CALL entry / PUT exit)
func (st *nifty50FnOSLState) ema6CrossedAboveEMA9() bool {
	return st.crossedAboveInBuf(
		func(e nifty50FnOSLBufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOSLBufferEntry) float64 { return e.EMA9 },
	)
}

// ema6CrossedBelowEMA9: EMA6 crossed below EMA9 (PUT entry / CALL exit)
func (st *nifty50FnOSLState) ema6CrossedBelowEMA9() bool {
	return st.crossedBelowInBuf(
		func(e nifty50FnOSLBufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOSLBufferEntry) float64 { return e.EMA9 },
	)
}

// Legacy aliases kept so existing callers compile (now both check vs EMA9)
func (st *nifty50FnOSLState) ema6CrossedAboveMA21() bool { return st.ema6CrossedAboveEMA9() }
func (st *nifty50FnOSLState) ema9CrossedAboveMA21() bool { return st.ema6CrossedAboveEMA9() }
func (st *nifty50FnOSLState) ema6CrossedBelowMA21() bool { return st.ema6CrossedBelowEMA9() }
func (st *nifty50FnOSLState) ema9CrossedBelowMA21() bool { return st.ema6CrossedBelowEMA9() }

// ── Sideways detection ──

func (st *nifty50FnOSLState) isSideways(cfg Nifty50FnOSLConfig, candleTime time.Time) isSidewaysResult {
	n := st.BufferCount
	if n < 3 {
		return isSidewaysResult{}
	}
	size := len(st.Buffer)
	count := n
	if count > size {
		count = size
	}
	entries := make([]nifty50FnOSLBufferEntry, count)
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

	// Check 2: EMA9 flat (used as baseline MA)
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
	}
}

// ── Trend direction filter ──

func (st *nifty50FnOSLState) trendBias(cfg Nifty50FnOSLConfig) TrendDirectionBias {
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

// ── Resistance / Support proximity checks ──

// isNearResistance returns true if the index close is within ResistanceDistancePct%
// of any configured resistance level (blocks CALL entries).
func isNearResistance(closePrice float64, levels []float64, distancePct float64) bool {
	if len(levels) == 0 || distancePct <= 0 {
		return false
	}
	for _, level := range levels {
		if level <= 0 {
			continue
		}
		dist := (level - closePrice) / closePrice * 100
		if dist < 0 {
			dist = -dist
		}
		if dist <= distancePct {
			return true
		}
		if closePrice >= level && (closePrice-level)/level*100 <= distancePct {
			return true
		}
	}
	return false
}

// isNearSupport returns true if the index close is within SupportDistancePct%
// of any configured support level (blocks PUT entries).
func isNearSupport(closePrice float64, levels []float64, distancePct float64) bool {
	if len(levels) == 0 || distancePct <= 0 {
		return false
	}
	for _, level := range levels {
		if level <= 0 {
			continue
		}
		dist := (closePrice - level) / closePrice * 100
		if dist < 0 {
			dist = -dist
		}
		if dist <= distancePct {
			return true
		}
	}
	return false
}

// nearestResistance returns the lowest resistance level that is at least minDistance
// above closePrice. This prevents setting a target that is too close to entry.
func nearestResistance(closePrice float64, levels []float64, minDistance float64) float64 {
	var nearest float64
	for _, level := range levels {
		if level > closePrice+minDistance && (nearest == 0 || level < nearest) {
			nearest = level
		}
	}
	return nearest
}

// nearestSupport returns the highest support level that is at least minDistance
// below closePrice. This prevents setting a target that is too close to entry.
func nearestSupport(closePrice float64, levels []float64, minDistance float64) float64 {
	var nearest float64
	for _, level := range levels {
		if level < closePrice-minDistance && level > nearest {
			nearest = level
		}
	}
	return nearest
}

// ── Snapshot / Restore ──

type nifty50FnOSLBufferEntrySnapshot struct {
	Time  time.Time `json:"time"`
	Open  float64   `json:"open"`
	High  float64   `json:"high"`
	Low   float64   `json:"low"`
	Close float64   `json:"close"`
	EMA6  float64   `json:"ema6"`
	EMA9  float64   `json:"ema9"`
}

type nifty50FnOSLSnapshot struct {
	Key string `json:"key"`

	EMA6Snap indicator.IndicatorSnapshot `json:"ema6"`
	EMA9Snap indicator.IndicatorSnapshot `json:"ema9"`

	Buffer      []nifty50FnOSLBufferEntrySnapshot `json:"buffer"`
	BufferIdx   int                               `json:"buffer_idx"`
	BufferCount int                               `json:"buffer_count"`

	Side            PositionSide `json:"side"`
	CallSetupActive bool         `json:"call_setup_active"`
	PutSetupActive  bool         `json:"put_setup_active"`

	IndexEntryPrice int64 `json:"index_entry_price"`
	IndexBestPrice  int64 `json:"index_best_price"`
	FNOEntryPrice   int64 `json:"fno_entry_price"`
	FNOBestPrice    int64 `json:"fno_best_price"`

	CandlesSinceExit      int          `json:"candles_since_exit"`
	InCooldown            bool         `json:"in_cooldown"`
	PendingEntry          PositionSide `json:"pending_entry"`
	PendingEMADiffEntry   PositionSide `json:"pending_ema_diff_entry"`
	CrossConsumedAtBufIdx int          `json:"cross_consumed_at_buf_idx"`
	DynamicIndexTarget    float64      `json:"dynamic_index_target"`
	LastTradingDate       time.Time    `json:"last_trading_date"`
	LastCloseTS           time.Time    `json:"last_close_ts"`
}

type nifty50FnOSLStrategySnapshot struct {
	Version     int                    `json:"version"`
	Strategy    string                 `json:"strategy"`
	Instruments []nifty50FnOSLSnapshot `json:"instruments"`
}

func snapshotNifty50FnOSL(key string, s *nifty50FnOSLState) nifty50FnOSLSnapshot {
	bufSnap := make([]nifty50FnOSLBufferEntrySnapshot, len(s.Buffer))
	for i, b := range s.Buffer {
		bufSnap[i] = nifty50FnOSLBufferEntrySnapshot{
			Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
			EMA6: b.EMA6, EMA9: b.EMA9,
		}
	}
	return nifty50FnOSLSnapshot{
		Key:                   key,
		EMA6Snap:              s.EMA6.Snapshot(),
		EMA9Snap:              s.EMA9.Snapshot(),
		Buffer:                bufSnap,
		BufferIdx:             s.BufferIdx,
		BufferCount:           s.BufferCount,
		Side:                  s.Side,
		CallSetupActive:       s.CallSetupActive,
		PutSetupActive:        s.PutSetupActive,
		IndexEntryPrice:       s.IndexEntryPrice,
		IndexBestPrice:        s.IndexBestPrice,
		FNOEntryPrice:         s.FNOEntryPrice,
		FNOBestPrice:          s.FNOBestPrice,
		CandlesSinceExit:      s.CandlesSinceExit,
		InCooldown:            s.InCooldown,
		PendingEntry:          s.PendingEntry,
		PendingEMADiffEntry:   s.PendingEMADiffEntry,
		CrossConsumedAtBufIdx: s.CrossConsumedAtBufIdx,
		DynamicIndexTarget:    s.DynamicIndexTarget,
		LastTradingDate:       s.LastTradingDate,
		LastCloseTS:           s.LastCloseTS,
	}
}

func restoreNifty50FnOSL(snap nifty50FnOSLSnapshot) *nifty50FnOSLState {
	s := newNifty50FnOSLState(DefaultNifty50FnOSLConfig())
	_ = s.EMA6.RestoreFromSnapshot(snap.EMA6Snap)
	_ = s.EMA9.RestoreFromSnapshot(snap.EMA9Snap)

	if len(snap.Buffer) > 0 {
		s.Buffer = make([]nifty50FnOSLBufferEntry, len(snap.Buffer))
		for i, b := range snap.Buffer {
			s.Buffer[i] = nifty50FnOSLBufferEntry{
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
	s.IndexEntryPrice = snap.IndexEntryPrice
	s.IndexBestPrice = snap.IndexBestPrice
	s.FNOEntryPrice = snap.FNOEntryPrice
	s.FNOBestPrice = snap.FNOBestPrice
	s.CandlesSinceExit = snap.CandlesSinceExit
	s.InCooldown = snap.InCooldown
	s.PendingEntry = snap.PendingEntry
	s.PendingEMADiffEntry = snap.PendingEMADiffEntry
	s.SLHitSide = SideNone
	// Buffer idx 0 is valid; preserve verbatim. Newer snapshots use -1 for unset.
	s.CrossConsumedAtBufIdx = snap.CrossConsumedAtBufIdx
	s.DynamicIndexTarget = snap.DynamicIndexTarget
	s.LastTradingDate = snap.LastTradingDate
	s.LastCloseTS = snap.LastCloseTS
	return s
}

func marshalNifty50FnOSLSnapshot(name string, instruments map[string]*nifty50FnOSLState) ([]byte, error) {
	snap := nifty50FnOSLStrategySnapshot{
		Version:  1,
		Strategy: name,
	}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty50FnOSL(k, v))
	}
	return json.Marshal(snap)
}

func unmarshalNifty50FnOSLSnapshot(data []byte) (*nifty50FnOSLStrategySnapshot, error) {
	var snap nifty50FnOSLStrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	// If snapshot has no valid EMA periods (old format), discard — cold start.
	for i := range snap.Instruments {
		if snap.Instruments[i].EMA9Snap.Period == 0 {
			log.Printf("[strategy] NIFTY50_FNO_SL: snapshot instrument %q has no EMA9 data (old format) — cold starting",
				snap.Instruments[i].Key)
			snap.Instruments[i] = nifty50FnOSLSnapshot{Key: snap.Instruments[i].Key}
		}
	}
	return &snap, nil
}

func restoreNifty50FnOSLInstruments(snap *nifty50FnOSLStrategySnapshot) map[string]*nifty50FnOSLState {
	m := make(map[string]*nifty50FnOSLState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty50FnOSL(is)
	}
	return m
}
