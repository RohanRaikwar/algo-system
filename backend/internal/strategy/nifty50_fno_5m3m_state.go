package strategy

import (
	"encoding/json"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 FnO 5M3M — State & Configuration
//
//  Multi-timeframe strategy:
//    5-minute TF: Entry signals (SMA21 × EMA6 crossover)
//    3-minute TF: Exit signals (SMA21 × EMA6 crossover)
//
//  Indicator mapping:
//    SMA21 (period 21) — baseline SMA signal
//    EMA6  (period 6)  — medium EMA confirmation
// ════════════════════════════════════════════════════════════════════

// Nifty50FnO5M3MConfig holds all configurable parameters.
type Nifty50FnO5M3MConfig struct {
	// ── Indicator periods ──
	SMA21Period int // 21 (baseline SMA)
	EMA6Period  int // 6  (medium EMA)

	// ── Prefetch buffer ──
	BufferSize int

	// ── Cooldown ──
	CooldownCandles int

	// ── Time-of-day filter ──
	SkipFirstMinutes int
	SkipLastMinutes  int

	// ── INDEX Stop Loss ──
	IndexHardSLPct     float64
	IndexTrailSLPct    float64
	IndexTrailStartPct float64

	// ── FNO Stop Loss ──
	FNOHardSLPct     float64
	FNOTrailSLPct    float64
	FNOTrailStartPct float64

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

// DefaultNifty50FnO5M3MConfig returns sensible defaults.
func DefaultNifty50FnO5M3MConfig() Nifty50FnO5M3MConfig {
	return Nifty50FnO5M3MConfig{
		SMA21Period: 21,
		EMA6Period:  6,
		BufferSize:  25, // Need at least 21 for SMA21
		// Cooldown
		CooldownCandles: 0,
		// Time-of-day filter
		SkipFirstMinutes: 30,
		SkipLastMinutes:  0,
		// Index SL
		IndexHardSLPct:     0.10,
		IndexTrailSLPct:    0,
		IndexTrailStartPct: 0.06,
		// FNO SL
		FNOHardSLPct:     7.0,
		FNOTrailSLPct:    5.0,
		FNOTrailStartPct: 5.0,
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

// ── Buffer entries (separate for 5m and 3m) ──

type nifty50FnO5M3MBufferEntry struct {
	Time  time.Time
	Open  float64
	High  float64
	Low   float64
	Close float64
	SMA21 float64
	EMA6  float64
}

// ── Per-instrument state ──

type nifty50FnO5M3MState struct {
	// ── 5-minute indicators (entry) ──
	SMA21_5m *indicator.SMA
	EMA6_5m  *indicator.EMA

	// ── 3-minute indicators (exit) ──
	SMA21_3m *indicator.SMA
	EMA6_3m  *indicator.EMA

	// ── Ring buffers (separate for each TF) ──
	Buffer5m      []nifty50FnO5M3MBufferEntry
	BufferIdx5m   int
	BufferCount5m int

	Buffer3m      []nifty50FnO5M3MBufferEntry
	BufferIdx3m   int
	BufferCount3m int

	// ── Position state machine ──
	Side PositionSide

	// ── Index price tracking ──
	IndexEntryPrice int64
	IndexBestPrice  int64

	// ── FNO price tracking ──
	FNOEntryPrice int64
	FNOBestPrice  int64

	// ── Cooldown tracking ──
	CandlesSinceExit int
	InCooldown       bool

	// ── Dedup per timeframe ──
	Last5mCloseTS time.Time
	Last3mCloseTS time.Time
}

func newNifty50FnO5M3MState(cfg Nifty50FnO5M3MConfig) *nifty50FnO5M3MState {
	bufSize := cfg.BufferSize
	if bufSize < 25 {
		bufSize = 25 // Need at least 21 for SMA21 + some lookback
	}
	return &nifty50FnO5M3MState{
		SMA21_5m: indicator.NewSMA(cfg.SMA21Period),
		EMA6_5m:  indicator.NewEMA(cfg.EMA6Period),
		SMA21_3m: indicator.NewSMA(cfg.SMA21Period),
		EMA6_3m:  indicator.NewEMA(cfg.EMA6Period),
		Buffer5m: make([]nifty50FnO5M3MBufferEntry, bufSize),
		Buffer3m: make([]nifty50FnO5M3MBufferEntry, bufSize),
		Side:     SideNone,
	}
}

// updateIndicators5m feeds 5-minute indicators and pushes to 5m ring buffer.
func (st *nifty50FnO5M3MState) updateIndicators5m(candle model.Candle) {
	st.SMA21_5m.Update(candle)
	st.EMA6_5m.Update(candle)

	if st.SMA21_5m.Ready() && st.EMA6_5m.Ready() {
		st.pushBuffer5m(nifty50FnO5M3MBufferEntry{
			Time:  candle.TS,
			Open:  float64(candle.Open),
			High:  float64(candle.High),
			Low:   float64(candle.Low),
			Close: float64(candle.Close),
			SMA21: st.SMA21_5m.Value(),
			EMA6:  st.EMA6_5m.Value(),
		})
	}
}

// updateIndicators3m feeds 3-minute indicators and pushes to 3m ring buffer.
func (st *nifty50FnO5M3MState) updateIndicators3m(candle model.Candle) {
	st.SMA21_3m.Update(candle)
	st.EMA6_3m.Update(candle)

	if st.SMA21_3m.Ready() && st.EMA6_3m.Ready() {
		st.pushBuffer3m(nifty50FnO5M3MBufferEntry{
			Time:  candle.TS,
			Open:  float64(candle.Open),
			High:  float64(candle.High),
			Low:   float64(candle.Low),
			Close: float64(candle.Close),
			SMA21: st.SMA21_3m.Value(),
			EMA6:  st.EMA6_3m.Value(),
		})
	}
}

func (st *nifty50FnO5M3MState) bufferReady5m() bool {
	return st.BufferCount5m >= 2
}

func (st *nifty50FnO5M3MState) bufferReady3m() bool {
	return st.BufferCount3m >= 2
}

// ── Crossover helpers for 5-minute TF ──

func (st *nifty50FnO5M3MState) crossedAboveInBuf5m(
	getA, getB func(nifty50FnO5M3MBufferEntry) float64,
) bool {
	size := len(st.Buffer5m)
	n := st.BufferCount5m
	if n < 2 {
		return false
	}
	currIdx := (st.BufferIdx5m - 1 + size) % size
	if getA(st.Buffer5m[currIdx]) <= getB(st.Buffer5m[currIdx]) {
		return false
	}
	lookback := n - 1
	if lookback > size-1 {
		lookback = size - 1
	}
	for i := 1; i <= lookback; i++ {
		idx := (st.BufferIdx5m - 1 - i + size*10) % size
		e := st.Buffer5m[idx]
		if getA(e) <= getB(e) {
			return true
		}
	}
	return false
}

func (st *nifty50FnO5M3MState) crossedBelowInBuf5m(
	getA, getB func(nifty50FnO5M3MBufferEntry) float64,
) bool {
	size := len(st.Buffer5m)
	n := st.BufferCount5m
	if n < 2 {
		return false
	}
	currIdx := (st.BufferIdx5m - 1 + size) % size
	if getA(st.Buffer5m[currIdx]) >= getB(st.Buffer5m[currIdx]) {
		return false
	}
	lookback := n - 1
	if lookback > size-1 {
		lookback = size - 1
	}
	for i := 1; i <= lookback; i++ {
		idx := (st.BufferIdx5m - 1 - i + size*10) % size
		e := st.Buffer5m[idx]
		if getA(e) >= getB(e) {
			return true
		}
	}
	return false
}

// sma21CrossedAboveEMA6_5m: SMA21 > EMA6 on 5m → CALL entry
func (st *nifty50FnO5M3MState) sma21CrossedAboveEMA6_5m() bool {
	return st.crossedAboveInBuf5m(
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.SMA21 },
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.EMA6 },
	)
}

// sma21CrossedBelowEMA6_5m: SMA21 < EMA6 on 5m → PUT entry
func (st *nifty50FnO5M3MState) sma21CrossedBelowEMA6_5m() bool {
	return st.crossedBelowInBuf5m(
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.SMA21 },
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.EMA6 },
	)
}

// ── Crossover helpers for 3-minute TF ──

func (st *nifty50FnO5M3MState) crossedAboveInBuf3m(
	getA, getB func(nifty50FnO5M3MBufferEntry) float64,
) bool {
	size := len(st.Buffer3m)
	n := st.BufferCount3m
	if n < 2 {
		return false
	}
	currIdx := (st.BufferIdx3m - 1 + size) % size
	if getA(st.Buffer3m[currIdx]) <= getB(st.Buffer3m[currIdx]) {
		return false
	}
	lookback := n - 1
	if lookback > size-1 {
		lookback = size - 1
	}
	for i := 1; i <= lookback; i++ {
		idx := (st.BufferIdx3m - 1 - i + size*10) % size
		e := st.Buffer3m[idx]
		if getA(e) <= getB(e) {
			return true
		}
	}
	return false
}

func (st *nifty50FnO5M3MState) crossedBelowInBuf3m(
	getA, getB func(nifty50FnO5M3MBufferEntry) float64,
) bool {
	size := len(st.Buffer3m)
	n := st.BufferCount3m
	if n < 2 {
		return false
	}
	currIdx := (st.BufferIdx3m - 1 + size) % size
	if getA(st.Buffer3m[currIdx]) >= getB(st.Buffer3m[currIdx]) {
		return false
	}
	lookback := n - 1
	if lookback > size-1 {
		lookback = size - 1
	}
	for i := 1; i <= lookback; i++ {
		idx := (st.BufferIdx3m - 1 - i + size*10) % size
		e := st.Buffer3m[idx]
		if getA(e) >= getB(e) {
			return true
		}
	}
	return false
}

// sma21CrossedAboveEMA6_3m: SMA21 > EMA6 on 3m → PUT exit
func (st *nifty50FnO5M3MState) sma21CrossedAboveEMA6_3m() bool {
	return st.crossedAboveInBuf3m(
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.SMA21 },
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.EMA6 },
	)
}

// sma21CrossedBelowEMA6_3m: SMA21 < EMA6 on 3m → CALL exit
func (st *nifty50FnO5M3MState) sma21CrossedBelowEMA6_3m() bool {
	return st.crossedBelowInBuf3m(
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.SMA21 },
		func(e nifty50FnO5M3MBufferEntry) float64 { return e.EMA6 },
	)
}

func (st *nifty50FnO5M3MState) pushBuffer5m(entry nifty50FnO5M3MBufferEntry) {
	st.Buffer5m[st.BufferIdx5m] = entry
	st.BufferIdx5m = (st.BufferIdx5m + 1) % len(st.Buffer5m)
	if st.BufferCount5m < len(st.Buffer5m) {
		st.BufferCount5m++
	}
}

func (st *nifty50FnO5M3MState) pushBuffer3m(entry nifty50FnO5M3MBufferEntry) {
	st.Buffer3m[st.BufferIdx3m] = entry
	st.BufferIdx3m = (st.BufferIdx3m + 1) % len(st.Buffer3m)
	if st.BufferCount3m < len(st.Buffer3m) {
		st.BufferCount3m++
	}
}

func (st *nifty50FnO5M3MState) prevAndCurr5m() (prev, curr nifty50FnO5M3MBufferEntry) {
	size := len(st.Buffer5m)
	currIdx := (st.BufferIdx5m - 1 + size) % size
	prevIdx := (st.BufferIdx5m - 2 + size) % size
	return st.Buffer5m[prevIdx], st.Buffer5m[currIdx]
}

func (st *nifty50FnO5M3MState) prevAndCurr3m() (prev, curr nifty50FnO5M3MBufferEntry) {
	size := len(st.Buffer3m)
	currIdx := (st.BufferIdx3m - 1 + size) % size
	prevIdx := (st.BufferIdx3m - 2 + size) % size
	return st.Buffer3m[prevIdx], st.Buffer3m[currIdx]
}

// ══════════════════════════════════════════════════════════════
// Snapshot / Restore
// ══════════════════════════════════════════════════════════════

type nifty50FnO5M3MBufferEntrySnapshot struct {
	Time  time.Time `json:"time"`
	Open  float64   `json:"open"`
	High  float64   `json:"high"`
	Low   float64   `json:"low"`
	Close float64   `json:"close"`
	SMA21 float64   `json:"sma21"`
	EMA6  float64   `json:"ema6"`
}

type nifty50FnO5M3MSnapshot struct {
	Key string `json:"key"`

	// 5-minute indicators
	SMA21_5mSnap indicator.IndicatorSnapshot `json:"sma21_5m"`
	EMA6_5mSnap  indicator.IndicatorSnapshot `json:"ema6_5m"`

	// 3-minute indicators
	SMA21_3mSnap indicator.IndicatorSnapshot `json:"sma21_3m"`
	EMA6_3mSnap  indicator.IndicatorSnapshot `json:"ema6_3m"`

	// 5-minute buffer
	Buffer5m      []nifty50FnO5M3MBufferEntrySnapshot `json:"buffer_5m"`
	BufferIdx5m   int                                 `json:"buffer_idx_5m"`
	BufferCount5m int                                 `json:"buffer_count_5m"`

	// 3-minute buffer
	Buffer3m      []nifty50FnO5M3MBufferEntrySnapshot `json:"buffer_3m"`
	BufferIdx3m   int                                 `json:"buffer_idx_3m"`
	BufferCount3m int                                 `json:"buffer_count_3m"`

	Side            PositionSide `json:"side"`
	IndexEntryPrice int64        `json:"index_entry_price"`
	IndexBestPrice  int64        `json:"index_best_price"`
	FNOEntryPrice   int64        `json:"fno_entry_price"`
	FNOBestPrice    int64        `json:"fno_best_price"`

	CandlesSinceExit int       `json:"candles_since_exit"`
	InCooldown       bool      `json:"in_cooldown"`
	Last5mCloseTS    time.Time `json:"last_5m_close_ts"`
	Last3mCloseTS    time.Time `json:"last_3m_close_ts"`
}

type nifty50FnO5M3MStrategySnapshot struct {
	Version     int                      `json:"version"`
	Strategy    string                   `json:"strategy"`
	Instruments []nifty50FnO5M3MSnapshot `json:"instruments"`
}

func snapshotNifty50FnO5M3M(key string, s *nifty50FnO5M3MState) nifty50FnO5M3MSnapshot {
	buf5mSnap := make([]nifty50FnO5M3MBufferEntrySnapshot, len(s.Buffer5m))
	for i, b := range s.Buffer5m {
		buf5mSnap[i] = nifty50FnO5M3MBufferEntrySnapshot{
			Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
			SMA21: b.SMA21, EMA6: b.EMA6,
		}
	}
	buf3mSnap := make([]nifty50FnO5M3MBufferEntrySnapshot, len(s.Buffer3m))
	for i, b := range s.Buffer3m {
		buf3mSnap[i] = nifty50FnO5M3MBufferEntrySnapshot{
			Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
			SMA21: b.SMA21, EMA6: b.EMA6,
		}
	}
	return nifty50FnO5M3MSnapshot{
		Key:          key,
		SMA21_5mSnap: s.SMA21_5m.Snapshot(),
		EMA6_5mSnap:  s.EMA6_5m.Snapshot(),
		SMA21_3mSnap: s.SMA21_3m.Snapshot(),
		EMA6_3mSnap:  s.EMA6_3m.Snapshot(),
		Buffer5m:     buf5mSnap,
		BufferIdx5m:  s.BufferIdx5m,
		BufferCount5m: s.BufferCount5m,
		Buffer3m:      buf3mSnap,
		BufferIdx3m:   s.BufferIdx3m,
		BufferCount3m: s.BufferCount3m,
		Side:            s.Side,
		IndexEntryPrice: s.IndexEntryPrice,
		IndexBestPrice:  s.IndexBestPrice,
		FNOEntryPrice:   s.FNOEntryPrice,
		FNOBestPrice:    s.FNOBestPrice,
		CandlesSinceExit: s.CandlesSinceExit,
		InCooldown:       s.InCooldown,
		Last5mCloseTS:    s.Last5mCloseTS,
		Last3mCloseTS:    s.Last3mCloseTS,
	}
}

func restoreNifty50FnO5M3M(snap nifty50FnO5M3MSnapshot) *nifty50FnO5M3MState {
	s := newNifty50FnO5M3MState(DefaultNifty50FnO5M3MConfig())
	_ = s.SMA21_5m.RestoreFromSnapshot(snap.SMA21_5mSnap)
	_ = s.EMA6_5m.RestoreFromSnapshot(snap.EMA6_5mSnap)
	_ = s.SMA21_3m.RestoreFromSnapshot(snap.SMA21_3mSnap)
	_ = s.EMA6_3m.RestoreFromSnapshot(snap.EMA6_3mSnap)

	if len(snap.Buffer5m) > 0 {
		s.Buffer5m = make([]nifty50FnO5M3MBufferEntry, len(snap.Buffer5m))
		for i, b := range snap.Buffer5m {
			s.Buffer5m[i] = nifty50FnO5M3MBufferEntry{
				Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
				SMA21: b.SMA21, EMA6: b.EMA6,
			}
		}
	}
	if len(snap.Buffer3m) > 0 {
		s.Buffer3m = make([]nifty50FnO5M3MBufferEntry, len(snap.Buffer3m))
		for i, b := range snap.Buffer3m {
			s.Buffer3m[i] = nifty50FnO5M3MBufferEntry{
				Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
				SMA21: b.SMA21, EMA6: b.EMA6,
			}
		}
	}
	s.BufferIdx5m = snap.BufferIdx5m
	s.BufferCount5m = snap.BufferCount5m
	s.BufferIdx3m = snap.BufferIdx3m
	s.BufferCount3m = snap.BufferCount3m
	s.Side = snap.Side
	if s.Side == "" {
		s.Side = SideNone
	}
	s.IndexEntryPrice = snap.IndexEntryPrice
	s.IndexBestPrice = snap.IndexBestPrice
	s.FNOEntryPrice = snap.FNOEntryPrice
	s.FNOBestPrice = snap.FNOBestPrice
	s.CandlesSinceExit = snap.CandlesSinceExit
	s.InCooldown = snap.InCooldown
	s.Last5mCloseTS = snap.Last5mCloseTS
	s.Last3mCloseTS = snap.Last3mCloseTS
	return s
}

func marshalNifty50FnO5M3MSnapshot(name string, instruments map[string]*nifty50FnO5M3MState) ([]byte, error) {
	snap := nifty50FnO5M3MStrategySnapshot{
		Version:  1,
		Strategy: name,
	}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty50FnO5M3M(k, v))
	}
	return json.Marshal(snap)
}

func unmarshalNifty50FnO5M3MSnapshot(data []byte) (*nifty50FnO5M3MStrategySnapshot, error) {
	var snap nifty50FnO5M3MStrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func restoreNifty50FnO5M3MInstruments(snap *nifty50FnO5M3MStrategySnapshot) map[string]*nifty50FnO5M3MState {
	m := make(map[string]*nifty50FnO5M3MState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty50FnO5M3M(is)
	}
	return m
}
