package strategy

import (
	"encoding/json"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 FnO SL2 — State & Configuration
//
//  Ultra-fast indicator variant of the dual-SL strategy:
//    SMA2  (period 2)  — ultra-fast entry signal
//    EMA6  (period 6)  — medium EMA exit signal
//    SMA15 (period 15) — baseline MA crossover threshold
//
//  BUG FIX: Snapshot version bumped to 2. Old v1 snapshots are
//  discarded on restore (cold start) instead of partially restoring
//  with mismatched indicator state.
// ════════════════════════════════════════════════════════════════════

// Nifty50FnOSL2Config holds all configurable parameters for the NIFTY50_FNO_SL2 strategy.
type Nifty50FnOSL2Config struct {
	// ── Indicator periods ──
	SMA2Period int // 2  (ultra-fast SMA — entry signal)
	EMA6Period int // 6  (medium EMA  — exit signal)
	MA15Period int // 15 (baseline MA — crossover threshold)

	// ── Prefetch buffer ──
	BufferSize int

	// ── Cooldown ──
	CooldownCandles int

	// ── Re-entry control ──
	ReEntryEnabled bool

	// ── Re-entry separation guard ──
	MinReEntrySeparationPct float64

	// ── Min EMA6-SMA2 diff guard ──
	MinEMA6EMA9DiffPct float64

	// ── Sideways market filter ──
	SidewaysEnabled bool
	MaxChopCrosses  int
	MA21FlatPct     float64
	NarrowRangePct  float64

	// ── Momentum bypass ──
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

	// ── INDEX Stop Loss ──
	IndexHardSLPct     float64
	IndexTrailSLPct    float64
	IndexTrailStartPct float64

	// ── FNO Stop Loss ──
	FNOHardSLPct     float64
	FNOTrailSLPct    float64
	FNOTrailStartPct float64

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

// DefaultNifty50FnOSL2Config returns sensible defaults for the ultra-fast dual-SL strategy.
func DefaultNifty50FnOSL2Config() Nifty50FnOSL2Config {
	return Nifty50FnOSL2Config{
		SMA2Period: 2,
		EMA6Period: 6,
		MA15Period: 15,
		BufferSize: 10,
		// Cooldown
		CooldownCandles: 0,
		// Re-entry guard
		ReEntryEnabled:          false,
		MinReEntrySeparationPct: 0.15,
		MinEMA6EMA9DiffPct:      0.005,
		// Sideways filter
		SidewaysEnabled: false,
		MaxChopCrosses:  3,
		MA21FlatPct:     0.05,
		NarrowRangePct:  0.30,
		// Momentum bypass (disabled)
		MomentumBypassPct: 0,
		// Time-of-day filter
		SkipFirstMinutes: 30,
		SkipLastMinutes:  0,
		// Lunch-hour filter
		LunchFilterEnabled: true,
		LunchStartHHMM:     "1200",
		LunchEndHHMM:       "1330",
		// Trend direction filter
		TrendEnabled:  false,
		TrendLookback: 50,
		TrendSlopePct: 0.05,
		// Index SL
		IndexHardSLPct:     0.10,
		IndexTrailSLPct:    0, // disabled — only hard SL on index
		IndexTrailStartPct: 0.06,
		// FNO SL
		FNOHardSLPct:     7.0,
		FNOTrailSLPct:    5.0,
		FNOTrailStartPct: 5.0, // start trailing after 5% profit
		// Re-entry after SL
		ReEntryAfterSL: true,
		// Resistance / Support filter
		ResistanceLevels:      nil,
		SupportLevels:         nil,
		ResistanceDistancePct: 0.10,
		SupportDistancePct:    0.10,
		// FNO tokens
		FNOCallToken: "",
		FNOPutToken:  "",
		// Index filter
		IndexToken: "NSE:99926000",
	}
}

// ── Buffer entry ──

type nifty50FnOSL2BufferEntry struct {
	Time  time.Time
	Open  float64
	High  float64
	Low   float64
	Close float64
	SMA2  float64 // ultra-fast SMA (period 2)
	EMA6  float64 // medium EMA    (period 6)
	MA15  float64 // baseline MA   (period 15)
}

// ── Per-instrument state ──

type nifty50FnOSL2State struct {
	// ── Indicators ──
	SMA2 *indicator.SMA // ultra-fast SMA2 (entry)
	EMA6 *indicator.EMA // medium EMA6 (exit)
	MA15 *indicator.SMA // baseline MA15

	// ── Ring buffer ──
	Buffer      []nifty50FnOSL2BufferEntry
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

	// ── Dedup ──
	LastCloseTS time.Time
}

func newNifty50FnOSL2State(cfg Nifty50FnOSL2Config) *nifty50FnOSL2State {
	bufSize := cfg.BufferSize
	if cfg.TrendEnabled && cfg.TrendLookback+1 > bufSize {
		bufSize = cfg.TrendLookback + 1
	}
	if bufSize <= 0 {
		bufSize = 10
	}
	return &nifty50FnOSL2State{
		SMA2:   indicator.NewSMA(cfg.SMA2Period),
		EMA6:   indicator.NewEMA(cfg.EMA6Period),
		MA15:   indicator.NewSMA(cfg.MA15Period),
		Buffer: make([]nifty50FnOSL2BufferEntry, bufSize),
		Side:   SideNone,
		CrossConsumedAtBufIdx: -1,
	}
}

// updateIndicators feeds all indicators and pushes to ring buffer.
func (st *nifty50FnOSL2State) updateIndicators(candle model.Candle) {
	st.SMA2.Update(candle)
	st.EMA6.Update(candle)
	st.MA15.Update(candle)

	if st.SMA2.Ready() && st.EMA6.Ready() && st.MA15.Ready() {
		st.pushBuffer(nifty50FnOSL2BufferEntry{
			Time:  candle.TS,
			Open:  float64(candle.Open),
			High:  float64(candle.High),
			Low:   float64(candle.Low),
			Close: float64(candle.Close),
			SMA2:  st.SMA2.Value(),
			EMA6:  st.EMA6.Value(),
			MA15:  st.MA15.Value(),
		})
	}
}

func (st *nifty50FnOSL2State) bufferReady() bool {
	return st.BufferCount >= 2
}

// ── Crossover helpers (10-candle buffer lookback) ──

func (st *nifty50FnOSL2State) crossedAboveInBuf(
	getA, getB func(nifty50FnOSL2BufferEntry) float64,
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

func (st *nifty50FnOSL2State) crossedBelowInBuf(
	getA, getB func(nifty50FnOSL2BufferEntry) float64,
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

// ema6CrossedAboveMA15: EMA6 > MA15 → bullish setup
func (st *nifty50FnOSL2State) ema6CrossedAboveMA15() bool {
	return st.crossedAboveInBuf(
		func(e nifty50FnOSL2BufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOSL2BufferEntry) float64 { return e.MA15 },
	)
}

// sma2CrossedAboveMA15: SMA2 > MA15 → CALL entry signal
func (st *nifty50FnOSL2State) sma2CrossedAboveMA15() bool {
	return st.crossedAboveInBuf(
		func(e nifty50FnOSL2BufferEntry) float64 { return e.SMA2 },
		func(e nifty50FnOSL2BufferEntry) float64 { return e.MA15 },
	)
}

// ema6CrossedBelowMA15: EMA6 < MA15 → bearish setup
func (st *nifty50FnOSL2State) ema6CrossedBelowMA15() bool {
	return st.crossedBelowInBuf(
		func(e nifty50FnOSL2BufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOSL2BufferEntry) float64 { return e.MA15 },
	)
}

// sma2CrossedBelowMA15: SMA2 < MA15 → PUT entry signal
func (st *nifty50FnOSL2State) sma2CrossedBelowMA15() bool {
	return st.crossedBelowInBuf(
		func(e nifty50FnOSL2BufferEntry) float64 { return e.SMA2 },
		func(e nifty50FnOSL2BufferEntry) float64 { return e.MA15 },
	)
}

// ema6CrossedAboveSMA2: EMA6 > SMA2 → PUT exit / re-entry gate
func (st *nifty50FnOSL2State) ema6CrossedAboveSMA2() bool {
	return st.crossedAboveInBuf(
		func(e nifty50FnOSL2BufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOSL2BufferEntry) float64 { return e.SMA2 },
	)
}

// ema6CrossedBelowSMA2: EMA6 < SMA2 → CALL exit / re-entry gate
func (st *nifty50FnOSL2State) ema6CrossedBelowSMA2() bool {
	return st.crossedBelowInBuf(
		func(e nifty50FnOSL2BufferEntry) float64 { return e.EMA6 },
		func(e nifty50FnOSL2BufferEntry) float64 { return e.SMA2 },
	)
}

func (st *nifty50FnOSL2State) pushBuffer(entry nifty50FnOSL2BufferEntry) {
	st.Buffer[st.BufferIdx] = entry
	st.BufferIdx = (st.BufferIdx + 1) % len(st.Buffer)
	if st.BufferCount < len(st.Buffer) {
		st.BufferCount++
	}
}

func (st *nifty50FnOSL2State) prevAndCurr() (prev, curr nifty50FnOSL2BufferEntry) {
	size := len(st.Buffer)
	currIdx := (st.BufferIdx - 1 + size) % size
	prevIdx := (st.BufferIdx - 2 + size) % size
	return st.Buffer[prevIdx], st.Buffer[currIdx]
}

// ── Sideways detection ──

func (st *nifty50FnOSL2State) isSideways(cfg Nifty50FnOSL2Config, candleTime time.Time) isSidewaysResult {
	n := st.BufferCount
	if n < 3 {
		return isSidewaysResult{}
	}
	size := len(st.Buffer)
	count := n
	if count > size {
		count = size
	}
	entries := make([]nifty50FnOSL2BufferEntry, count)
	for i := 0; i < count; i++ {
		idx := (st.BufferIdx - count + i + size*10) % size
		entries[i] = st.Buffer[idx]
	}

	crossCount := 0
	for i := 1; i < len(entries); i++ {
		prev := entries[i-1]
		curr := entries[i]
		if (prev.EMA6 >= prev.SMA2 && curr.EMA6 < curr.SMA2) ||
			(prev.EMA6 <= prev.SMA2 && curr.EMA6 > curr.SMA2) {
			crossCount++
		}
	}
	chopFired := crossCount >= cfg.MaxChopCrosses

	ma15Max, ma15Min := entries[0].MA15, entries[0].MA15
	for _, e := range entries[1:] {
		if e.MA15 > ma15Max {
			ma15Max = e.MA15
		}
		if e.MA15 < ma15Min {
			ma15Min = e.MA15
		}
	}
	flatFired := false
	if ma15Min > 0 {
		ma15Range := (ma15Max - ma15Min) / ma15Min * 100
		flatFired = ma15Range < cfg.MA21FlatPct
	}

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

func (st *nifty50FnOSL2State) trendBias(cfg Nifty50FnOSL2Config) TrendDirectionBias {
	if !cfg.TrendEnabled || cfg.TrendLookback <= 0 {
		return TrendNeutral
	}
	n := st.BufferCount
	if n < cfg.TrendLookback+1 {
		return TrendNeutral
	}
	size := len(st.Buffer)

	latestIdx := (st.BufferIdx - 1 + size) % size
	latestMA15 := st.Buffer[latestIdx].MA15

	oldIdx := (st.BufferIdx - cfg.TrendLookback - 1 + size*10) % size
	oldMA15 := st.Buffer[oldIdx].MA15

	if latestMA15 == 0 || oldMA15 == 0 {
		return TrendNeutral
	}

	slopePct := (latestMA15 - oldMA15) / oldMA15 * 100
	threshold := cfg.TrendSlopePct

	if slopePct > threshold {
		return TrendBullish
	}
	if slopePct < -threshold {
		return TrendBearish
	}
	return TrendNeutral
}

// ══════════════════════════════════════════════════════════════
// Snapshot / Restore  (version 2 — SMA2/EMA6/MA15 typed correctly)
//
// Version history:
//   v1 — original; had type-mismatch bugs on restore
//   v2 — indicators snapshotted with their own typed Snapshot() calls;
//         old v1 snapshots are discarded on restore (cold start)
// ══════════════════════════════════════════════════════════════

type nifty50FnOSL2BufferEntrySnapshot struct {
	Time  time.Time `json:"time"`
	Open  float64   `json:"open"`
	High  float64   `json:"high"`
	Low   float64   `json:"low"`
	Close float64   `json:"close"`
	SMA2  float64   `json:"sma2"`
	EMA6  float64   `json:"ema6"`
	MA15  float64   `json:"ma15"`
}

type nifty50FnOSL2Snapshot struct {
	Key string `json:"key"`

	// Each indicator snapshotted with its own type (SMA or EMA)
	SMA2Snap indicator.IndicatorSnapshot `json:"sma2"`
	EMA6Snap indicator.IndicatorSnapshot `json:"ema6"`
	MA15Snap indicator.IndicatorSnapshot `json:"ma15"`

	Buffer      []nifty50FnOSL2BufferEntrySnapshot `json:"buffer"`
	BufferIdx   int                                `json:"buffer_idx"`
	BufferCount int                                `json:"buffer_count"`

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
	LastCloseTS           time.Time    `json:"last_close_ts"`
}

type nifty50FnOSL2StrategySnapshot struct {
	Version     int                     `json:"version"`
	Strategy    string                  `json:"strategy"`
	Instruments []nifty50FnOSL2Snapshot `json:"instruments"`
}

func snapshotNifty50FnOSL2(key string, s *nifty50FnOSL2State) nifty50FnOSL2Snapshot {
	bufSnap := make([]nifty50FnOSL2BufferEntrySnapshot, len(s.Buffer))
	for i, b := range s.Buffer {
		bufSnap[i] = nifty50FnOSL2BufferEntrySnapshot{
			Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
			SMA2: b.SMA2, EMA6: b.EMA6, MA15: b.MA15,
		}
	}
	return nifty50FnOSL2Snapshot{
		Key:                 key,
		SMA2Snap:            s.SMA2.Snapshot(), // indicator.SMA → Type:"SMA"
		EMA6Snap:            s.EMA6.Snapshot(), // indicator.EMA → Type:"EMA"
		MA15Snap:            s.MA15.Snapshot(), // indicator.SMA → Type:"SMA"
		Buffer:              bufSnap,
		BufferIdx:           s.BufferIdx,
		BufferCount:         s.BufferCount,
		Side:                s.Side,
		CallSetupActive:     s.CallSetupActive,
		PutSetupActive:      s.PutSetupActive,
		IndexEntryPrice:     s.IndexEntryPrice,
		IndexBestPrice:      s.IndexBestPrice,
		FNOEntryPrice:       s.FNOEntryPrice,
		FNOBestPrice:        s.FNOBestPrice,
		CandlesSinceExit:      s.CandlesSinceExit,
		InCooldown:            s.InCooldown,
		PendingEntry:          s.PendingEntry,
		PendingEMADiffEntry:   s.PendingEMADiffEntry,
		CrossConsumedAtBufIdx: s.CrossConsumedAtBufIdx,
		LastCloseTS:           s.LastCloseTS,
	}
}

func restoreNifty50FnOSL2(snap nifty50FnOSL2Snapshot) *nifty50FnOSL2State {
	s := newNifty50FnOSL2State(DefaultNifty50FnOSL2Config())
	// Each indicator restored from its own correctly-typed snapshot
	_ = s.SMA2.RestoreFromSnapshot(snap.SMA2Snap)
	_ = s.EMA6.RestoreFromSnapshot(snap.EMA6Snap)
	_ = s.MA15.RestoreFromSnapshot(snap.MA15Snap)

	if len(snap.Buffer) > 0 {
		s.Buffer = make([]nifty50FnOSL2BufferEntry, len(snap.Buffer))
		for i, b := range snap.Buffer {
			s.Buffer[i] = nifty50FnOSL2BufferEntry{
				Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
				SMA2: b.SMA2, EMA6: b.EMA6, MA15: b.MA15,
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
	// Buffer idx 0 is valid; preserve verbatim. Newer snapshots use -1 for unset.
	s.CrossConsumedAtBufIdx = snap.CrossConsumedAtBufIdx
	s.LastCloseTS = snap.LastCloseTS
	return s
}

func marshalNifty50FnOSL2Snapshot(name string, instruments map[string]*nifty50FnOSL2State) ([]byte, error) {
	snap := nifty50FnOSL2StrategySnapshot{
		Version:  2, // v2: correct typed snapshots per indicator
		Strategy: name,
	}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty50FnOSL2(k, v))
	}
	return json.Marshal(snap)
}

func unmarshalNifty50FnOSL2Snapshot(data []byte) (*nifty50FnOSL2StrategySnapshot, error) {
	var snap nifty50FnOSL2StrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	// v1 snapshots had type-mismatch bugs — discard entirely, cold start.
	if snap.Version < 2 {
		return &nifty50FnOSL2StrategySnapshot{Version: 2, Strategy: snap.Strategy}, nil
	}
	return &snap, nil
}

func restoreNifty50FnOSL2Instruments(snap *nifty50FnOSL2StrategySnapshot) map[string]*nifty50FnOSL2State {
	m := make(map[string]*nifty50FnOSL2State, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty50FnOSL2(is)
	}
	return m
}
