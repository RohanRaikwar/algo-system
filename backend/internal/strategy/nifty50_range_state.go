package strategy

import (
	"encoding/json"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 RANGE — Support/Resistance Range Trading Strategy
//
//  Detects support/resistance levels and trades within ranges:
//    • BUY near support (expecting bounce)
//    • SELL near resistance (expecting rejection)
//    • Exit on range breakout or opposite level touch
//
//  Key Features:
//    • Dynamic S/R level detection using swing highs/lows
//    • Range confirmation (multiple touches)
//    • Breakout detection and position reversal
//    • Risk management with range-based stops
// ════════════════════════════════════════════════════════════════════

// Nifty50RangeConfig holds all configurable parameters.
type Nifty50RangeConfig struct {
	// ── Indicators ──
	EMA9Period  int // 9  (trend filter)
	SMA21Period int // 21 (trend filter)

	// ── Support/Resistance Detection ──
	SwingLookback       int     // Candles to look back for swing highs/lows (e.g., 20)
	MinTouchesForLevel  int     // Min touches to confirm S/R level (e.g., 2-3)
	TouchTolerancePct   float64 // % tolerance for level touch (e.g., 0.15%)
	MinLevelSeparation  float64 // Min % separation between S/R levels (e.g., 0.3%)
	LevelExpiryCandles  int     // Candles before level expires if not touched (e.g., 50)

	// ── Range Detection ──
	MinRangeSizePts     int64   // Min range size in index points (e.g., 100 = 10 NIFTY points)
	MaxRangeSizePts     int64   // Max range size in index points (e.g., 5000 = 500 NIFTY points)
	MinRangeSizePct     float64 // Min range size as % of price (e.g., 0.5%) - DEPRECATED, use MinRangeSizePts
	MaxRangeSizePct     float64 // Max range size as % of price (e.g., 2.0%) - DEPRECATED, use MaxRangeSizePts
	RangeConfirmCandles int     // Candles to confirm range (e.g., 10)

	// ── Entry Zones ──
	SupportEntryZonePct    float64 // % above support to enter CALL (e.g., 0.1%)
	ResistanceEntryZonePct float64 // % below resistance to enter PUT (e.g., 0.1%)

	// ── Breakout Detection ──
	BreakoutConfirmPct    float64 // % beyond level to confirm breakout (e.g., 0.2%)
	BreakoutVolumeMultiple float64 // Volume multiple for breakout confirmation (e.g., 1.5x)

	// ── Position Management ──
	TargetAtOppositeLevelPct float64 // % before opposite level to take profit (e.g., 0.15%)
	StopBeyondLevelPct       float64 // % beyond entry level for stop loss (e.g., 0.2%)

	// ── FNO Target & SL ──
	FNOTargetProfitPct float64 // FNO target profit % (e.g., 1.5%)
	FNOHardSLPct       float64 // FNO hard stop loss % (e.g., 3.0%)
	FNOTrailSLPct      float64 // FNO trailing stop loss % (e.g., 1.5%)
	FNOTrailStartPct   float64 // Profit % to start trailing (e.g., 2.0%)

	// ── Time-of-day filter ──
	SkipFirstMinutes int
	SkipLastMinutes  int

	// ── Trend Filter ──
	TrendFilterEnabled bool // Only trade with trend
	TrendLookback      int  // Candles for trend detection

	// ── Cooldown ──
	CooldownCandles int // Candles to wait after exit

	// ── Prefetch buffer ──
	BufferSize int

	// ── FNO token identifiers ──
	FNOCallToken string
	FNOPutToken  string

	// ── Candle filter ──
	IndexToken string // e.g. "NSE:99926000"

	// ── Name override ──
	NameOverride string
}

// DefaultNifty50RangeConfig returns sensible defaults.
func DefaultNifty50RangeConfig() Nifty50RangeConfig {
	return Nifty50RangeConfig{
		EMA9Period:  9,
		SMA21Period: 21,

		// S/R Detection
		SwingLookback:      20,
		MinTouchesForLevel: 2,
		TouchTolerancePct:  0.15,
		MinLevelSeparation: 0.3,
		LevelExpiryCandles: 50,

		// Range Detection
		MinRangeSizePts:     1000, // 10 NIFTY points (in paise: 10 * 100 = 1000)
		MaxRangeSizePts:     50000, // 500 NIFTY points (in paise: 500 * 100 = 50000)
		MinRangeSizePct:     0.5,  // Fallback if MinRangeSizePts is 0
		MaxRangeSizePct:     2.0,  // Fallback if MaxRangeSizePts is 0
		RangeConfirmCandles: 10,

		// Entry Zones
		SupportEntryZonePct:    0.1,
		ResistanceEntryZonePct: 0.1,

		// Breakout
		BreakoutConfirmPct:     0.2,
		BreakoutVolumeMultiple: 1.5,

		// Position Management
		TargetAtOppositeLevelPct: 0.15,
		StopBeyondLevelPct:       0.2,

		// FNO
		FNOTargetProfitPct: 1.5,
		FNOHardSLPct:       3.0,
		FNOTrailSLPct:      1.5,
		FNOTrailStartPct:   2.0,

		// Time filter
		SkipFirstMinutes: 0,
		SkipLastMinutes:  0,

		// Trend filter
		TrendFilterEnabled: false,
		TrendLookback:      50,

		// Cooldown
		CooldownCandles: 0,

		// Buffer
		BufferSize: 50,

		// Tokens
		FNOCallToken: "",
		FNOPutToken:  "",
		IndexToken:   "NSE:99926000",
		NameOverride: "NIFTY50_RANGE",
	}
}

// ── Support/Resistance Level ──

type SRLevel struct {
	Price       float64   // Level price
	Type        string    // "support" or "resistance"
	Touches     int       // Number of times price touched this level
	LastTouch   time.Time // Last time price touched this level
	Strength    float64   // Level strength (0-1, based on touches and age)
	CreatedAt   time.Time // When level was first detected
	IsActive    bool      // Whether level is still valid
}

// ── Range Structure ──

type PriceRange struct {
	Support    float64   // Support level price
	Resistance float64   // Resistance level price
	MidPoint   float64   // Range midpoint
	Size       float64   // Range size in points
	SizePct    float64   // Range size as % of price
	Confirmed  bool      // Whether range is confirmed
	CreatedAt  time.Time // When range was detected
	TouchCount int       // Total touches of both levels
}

// ── Buffer entry ──

type nifty50RangeBufferEntry struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
	EMA9   float64
	SMA21  float64
}

// ── Per-instrument state ──

type nifty50RangeState struct {
	// ── Indicators ──
	EMA9  *indicator.EMA
	SMA21 *indicator.SMA

	// ── Ring buffer ──
	Buffer      []nifty50RangeBufferEntry
	BufferIdx   int
	BufferCount int

	// ── Support/Resistance Levels ──
	SupportLevels    []SRLevel
	ResistanceLevels []SRLevel

	// ── Active Range ──
	ActiveRange *PriceRange

	// ── Position state ──
	Side            PositionSide
	EntryPrice      int64
	EntryLevel      float64 // S/R level at entry
	TargetLevel     float64 // Opposite S/R level (target)
	StopLevel       float64 // Stop loss level
	IndexEntryPrice int64
	IndexBestPrice  int64

	// ── FNO tracking ──
	FNOEntryPrice int64
	FNOBestPrice  int64

	// ── Cooldown ──
	InCooldown       bool
	CandlesSinceExit int

	// ── Swing tracking ──
	RecentSwingHighs []SwingPoint
	RecentSwingLows  []SwingPoint

	// ── Volume tracking ──
	AvgVolume float64

	// ── Dedup ──
	LastCloseTS time.Time
}

// SwingPoint represents a swing high or low.
type SwingPoint struct {
	Price float64
	Time  time.Time
	Index int // Buffer index
}

// newNifty50RangeState creates a fresh state with the given config.
func newNifty50RangeState(cfg Nifty50RangeConfig) *nifty50RangeState {
	bufSize := cfg.BufferSize
	if bufSize <= 0 {
		bufSize = 50
	}
	return &nifty50RangeState{
		EMA9:             indicator.NewEMA(cfg.EMA9Period),
		SMA21:            indicator.NewSMA(cfg.SMA21Period),
		Buffer:           make([]nifty50RangeBufferEntry, bufSize),
		Side:             SideNone,
		SupportLevels:    make([]SRLevel, 0, 10),
		ResistanceLevels: make([]SRLevel, 0, 10),
		RecentSwingHighs: make([]SwingPoint, 0, 20),
		RecentSwingLows:  make([]SwingPoint, 0, 20),
	}
}

// bufferReady returns true when enough data exists.
func (st *nifty50RangeState) bufferReady() bool {
	return st.BufferCount >= 3
}

// pushBuffer appends a new entry to the ring buffer.
func (st *nifty50RangeState) pushBuffer(entry nifty50RangeBufferEntry) {
	st.Buffer[st.BufferIdx] = entry
	st.BufferIdx = (st.BufferIdx + 1) % len(st.Buffer)
	if st.BufferCount < len(st.Buffer) {
		st.BufferCount++
	}
}

// latestBufferEntry returns the most recent buffer entry.
func (st *nifty50RangeState) latestBufferEntry() (nifty50RangeBufferEntry, bool) {
	if st.BufferCount == 0 {
		return nifty50RangeBufferEntry{}, false
	}
	size := len(st.Buffer)
	idx := (st.BufferIdx - 1 + size) % size
	return st.Buffer[idx], true
}

// getBufferEntry returns entry at offset from current (0 = latest, 1 = previous, etc.)
func (st *nifty50RangeState) getBufferEntry(offset int) (nifty50RangeBufferEntry, bool) {
	if offset >= st.BufferCount {
		return nifty50RangeBufferEntry{}, false
	}
	size := len(st.Buffer)
	idx := (st.BufferIdx - 1 - offset + size*10) % size
	return st.Buffer[idx], true
}

// updateIndicators feeds indicators and pushes to buffer.
func (st *nifty50RangeState) updateIndicators(candle model.Candle) {
	st.EMA9.Update(candle)
	st.SMA21.Update(candle)

	if st.EMA9.Ready() && st.SMA21.Ready() {
		st.pushBuffer(nifty50RangeBufferEntry{
			Time:   candle.TS,
			Open:   float64(candle.Open),
			High:   float64(candle.High),
			Low:    float64(candle.Low),
			Close:  float64(candle.Close),
			Volume: candle.Volume,
			EMA9:   st.EMA9.Value(),
			SMA21:  st.SMA21.Value(),
		})
	}
}

// ── Snapshot / Restore ──

type nifty50RangeBufferEntrySnapshot struct {
	Time   time.Time `json:"time"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume int64     `json:"volume"`
	EMA9   float64   `json:"ema9"`
	SMA21  float64   `json:"sma21"`
}

type SRLevelSnapshot struct {
	Price     float64   `json:"price"`
	Type      string    `json:"type"`
	Touches   int       `json:"touches"`
	LastTouch time.Time `json:"last_touch"`
	Strength  float64   `json:"strength"`
	CreatedAt time.Time `json:"created_at"`
	IsActive  bool      `json:"is_active"`
}

type PriceRangeSnapshot struct {
	Support    float64   `json:"support"`
	Resistance float64   `json:"resistance"`
	MidPoint   float64   `json:"mid_point"`
	Size       float64   `json:"size"`
	SizePct    float64   `json:"size_pct"`
	Confirmed  bool      `json:"confirmed"`
	CreatedAt  time.Time `json:"created_at"`
	TouchCount int       `json:"touch_count"`
}

type SwingPointSnapshot struct {
	Price float64   `json:"price"`
	Time  time.Time `json:"time"`
	Index int       `json:"index"`
}

type nifty50RangeSnapshot struct {
	Key string `json:"key"`

	EMA9Snap  indicator.IndicatorSnapshot `json:"ema9"`
	SMA21Snap indicator.IndicatorSnapshot `json:"sma21"`

	Buffer      []nifty50RangeBufferEntrySnapshot `json:"buffer"`
	BufferIdx   int                               `json:"buffer_idx"`
	BufferCount int                               `json:"buffer_count"`

	SupportLevels    []SRLevelSnapshot `json:"support_levels"`
	ResistanceLevels []SRLevelSnapshot `json:"resistance_levels"`
	ActiveRange      *PriceRangeSnapshot `json:"active_range,omitempty"`

	Side            PositionSide `json:"side"`
	EntryPrice      int64        `json:"entry_price"`
	EntryLevel      float64      `json:"entry_level"`
	TargetLevel     float64      `json:"target_level"`
	StopLevel       float64      `json:"stop_level"`
	IndexEntryPrice int64        `json:"index_entry_price"`
	IndexBestPrice  int64        `json:"index_best_price"`
	FNOEntryPrice   int64        `json:"fno_entry_price"`
	FNOBestPrice    int64        `json:"fno_best_price"`

	InCooldown       bool `json:"in_cooldown"`
	CandlesSinceExit int  `json:"candles_since_exit"`

	RecentSwingHighs []SwingPointSnapshot `json:"recent_swing_highs"`
	RecentSwingLows  []SwingPointSnapshot `json:"recent_swing_lows"`
	AvgVolume        float64              `json:"avg_volume"`

	LastCloseTS time.Time `json:"last_close_ts"`
}

type nifty50RangeStrategySnapshot struct {
	Version     int                    `json:"version"`
	Strategy    string                 `json:"strategy"`
	Instruments []nifty50RangeSnapshot `json:"instruments"`
}

func snapshotNifty50Range(key string, s *nifty50RangeState) nifty50RangeSnapshot {
	bufSnap := make([]nifty50RangeBufferEntrySnapshot, len(s.Buffer))
	for i, b := range s.Buffer {
		bufSnap[i] = nifty50RangeBufferEntrySnapshot{
			Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
			Volume: b.Volume, EMA9: b.EMA9, SMA21: b.SMA21,
		}
	}

	supportSnap := make([]SRLevelSnapshot, len(s.SupportLevels))
	for i, l := range s.SupportLevels {
		supportSnap[i] = SRLevelSnapshot{
			Price: l.Price, Type: l.Type, Touches: l.Touches,
			LastTouch: l.LastTouch, Strength: l.Strength,
			CreatedAt: l.CreatedAt, IsActive: l.IsActive,
		}
	}

	resistanceSnap := make([]SRLevelSnapshot, len(s.ResistanceLevels))
	for i, l := range s.ResistanceLevels {
		resistanceSnap[i] = SRLevelSnapshot{
			Price: l.Price, Type: l.Type, Touches: l.Touches,
			LastTouch: l.LastTouch, Strength: l.Strength,
			CreatedAt: l.CreatedAt, IsActive: l.IsActive,
		}
	}

	var rangeSnap *PriceRangeSnapshot
	if s.ActiveRange != nil {
		rangeSnap = &PriceRangeSnapshot{
			Support: s.ActiveRange.Support, Resistance: s.ActiveRange.Resistance,
			MidPoint: s.ActiveRange.MidPoint, Size: s.ActiveRange.Size,
			SizePct: s.ActiveRange.SizePct, Confirmed: s.ActiveRange.Confirmed,
			CreatedAt: s.ActiveRange.CreatedAt, TouchCount: s.ActiveRange.TouchCount,
		}
	}

	swingHighsSnap := make([]SwingPointSnapshot, len(s.RecentSwingHighs))
	for i, sp := range s.RecentSwingHighs {
		swingHighsSnap[i] = SwingPointSnapshot{Price: sp.Price, Time: sp.Time, Index: sp.Index}
	}

	swingLowsSnap := make([]SwingPointSnapshot, len(s.RecentSwingLows))
	for i, sp := range s.RecentSwingLows {
		swingLowsSnap[i] = SwingPointSnapshot{Price: sp.Price, Time: sp.Time, Index: sp.Index}
	}

	return nifty50RangeSnapshot{
		Key:              key,
		EMA9Snap:         s.EMA9.Snapshot(),
		SMA21Snap:        s.SMA21.Snapshot(),
		Buffer:           bufSnap,
		BufferIdx:        s.BufferIdx,
		BufferCount:      s.BufferCount,
		SupportLevels:    supportSnap,
		ResistanceLevels: resistanceSnap,
		ActiveRange:      rangeSnap,
		Side:             s.Side,
		EntryPrice:       s.EntryPrice,
		EntryLevel:       s.EntryLevel,
		TargetLevel:      s.TargetLevel,
		StopLevel:        s.StopLevel,
		IndexEntryPrice:  s.IndexEntryPrice,
		IndexBestPrice:   s.IndexBestPrice,
		FNOEntryPrice:    s.FNOEntryPrice,
		FNOBestPrice:     s.FNOBestPrice,
		InCooldown:       s.InCooldown,
		CandlesSinceExit: s.CandlesSinceExit,
		RecentSwingHighs: swingHighsSnap,
		RecentSwingLows:  swingLowsSnap,
		AvgVolume:        s.AvgVolume,
		LastCloseTS:      s.LastCloseTS,
	}
}

func restoreNifty50Range(snap nifty50RangeSnapshot) *nifty50RangeState {
	s := newNifty50RangeState(DefaultNifty50RangeConfig())
	_ = s.EMA9.RestoreFromSnapshot(snap.EMA9Snap)
	_ = s.SMA21.RestoreFromSnapshot(snap.SMA21Snap)

	if len(snap.Buffer) > 0 {
		s.Buffer = make([]nifty50RangeBufferEntry, len(snap.Buffer))
		for i, b := range snap.Buffer {
			s.Buffer[i] = nifty50RangeBufferEntry{
				Time: b.Time, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close,
				Volume: b.Volume, EMA9: b.EMA9, SMA21: b.SMA21,
			}
		}
	}
	s.BufferIdx = snap.BufferIdx
	s.BufferCount = snap.BufferCount

	s.SupportLevels = make([]SRLevel, len(snap.SupportLevels))
	for i, l := range snap.SupportLevels {
		s.SupportLevels[i] = SRLevel{
			Price: l.Price, Type: l.Type, Touches: l.Touches,
			LastTouch: l.LastTouch, Strength: l.Strength,
			CreatedAt: l.CreatedAt, IsActive: l.IsActive,
		}
	}

	s.ResistanceLevels = make([]SRLevel, len(snap.ResistanceLevels))
	for i, l := range snap.ResistanceLevels {
		s.ResistanceLevels[i] = SRLevel{
			Price: l.Price, Type: l.Type, Touches: l.Touches,
			LastTouch: l.LastTouch, Strength: l.Strength,
			CreatedAt: l.CreatedAt, IsActive: l.IsActive,
		}
	}

	if snap.ActiveRange != nil {
		s.ActiveRange = &PriceRange{
			Support: snap.ActiveRange.Support, Resistance: snap.ActiveRange.Resistance,
			MidPoint: snap.ActiveRange.MidPoint, Size: snap.ActiveRange.Size,
			SizePct: snap.ActiveRange.SizePct, Confirmed: snap.ActiveRange.Confirmed,
			CreatedAt: snap.ActiveRange.CreatedAt, TouchCount: snap.ActiveRange.TouchCount,
		}
	}

	s.Side = snap.Side
	if s.Side == "" {
		s.Side = SideNone
	}
	s.EntryPrice = snap.EntryPrice
	s.EntryLevel = snap.EntryLevel
	s.TargetLevel = snap.TargetLevel
	s.StopLevel = snap.StopLevel
	s.IndexEntryPrice = snap.IndexEntryPrice
	s.IndexBestPrice = snap.IndexBestPrice
	s.FNOEntryPrice = snap.FNOEntryPrice
	s.FNOBestPrice = snap.FNOBestPrice
	s.InCooldown = snap.InCooldown
	s.CandlesSinceExit = snap.CandlesSinceExit

	s.RecentSwingHighs = make([]SwingPoint, len(snap.RecentSwingHighs))
	for i, sp := range snap.RecentSwingHighs {
		s.RecentSwingHighs[i] = SwingPoint{Price: sp.Price, Time: sp.Time, Index: sp.Index}
	}

	s.RecentSwingLows = make([]SwingPoint, len(snap.RecentSwingLows))
	for i, sp := range snap.RecentSwingLows {
		s.RecentSwingLows[i] = SwingPoint{Price: sp.Price, Time: sp.Time, Index: sp.Index}
	}

	s.AvgVolume = snap.AvgVolume
	s.LastCloseTS = snap.LastCloseTS

	return s
}

func marshalNifty50RangeSnapshot(name string, instruments map[string]*nifty50RangeState) ([]byte, error) {
	snap := nifty50RangeStrategySnapshot{
		Version:  1,
		Strategy: name,
	}
	for k, v := range instruments {
		snap.Instruments = append(snap.Instruments, snapshotNifty50Range(k, v))
	}
	return json.Marshal(snap)
}

func unmarshalNifty50RangeSnapshot(data []byte) (*nifty50RangeStrategySnapshot, error) {
	var snap nifty50RangeStrategySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func restoreNifty50RangeInstruments(snap *nifty50RangeStrategySnapshot) map[string]*nifty50RangeState {
	m := make(map[string]*nifty50RangeState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = restoreNifty50Range(is)
	}
	return m
}
