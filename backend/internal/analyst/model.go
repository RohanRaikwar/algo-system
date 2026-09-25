package analyst

import (
	"encoding/json"
	"time"
)

// ── Market State ──

// MarketState represents the detected market regime.
type MarketState string

const (
	MarketStateTrendingUp   MarketState = "TRENDING_UP"
	MarketStateTrendingDown MarketState = "TRENDING_DOWN"
	MarketStateSideways     MarketState = "SIDEWAYS"
	MarketStateChoppy       MarketState = "CHOPPY"
	MarketStateMixed        MarketState = "MIXED"
)

// IndicatorVote represents a single indicator's vote on market state.
type IndicatorVote struct {
	Name  string      `json:"name"`  // e.g. "ADX", "ATR%", "RSI_SLOPE", "EMA_SPREAD", "VWAP_DEV"
	State MarketState `json:"state"` // what this indicator thinks
	Value float64     `json:"value"` // raw indicator value
}

// MarketStateEvent is the published event when market state changes or is recalculated.
type MarketStateEvent struct {
	State           MarketState     `json:"state"`
	Confluence      int             `json:"confluence"`       // how many indicators agree (0-5)
	Direction       string          `json:"direction"`        // "BULLISH", "BEARISH", "NEUTRAL"
	Votes           []IndicatorVote `json:"votes"`            // per-indicator breakdown
	Token           string          `json:"token"`
	Exchange        string          `json:"exchange"`
	TF              int             `json:"tf"`
	TS              time.Time       `json:"ts"`
	PreviousState   MarketState     `json:"previous_state,omitempty"`
	StateChangedAt  time.Time       `json:"state_changed_at,omitempty"`
}

// JSON returns the JSON-encoded event.
func (e *MarketStateEvent) JSON() []byte {
	b, _ := json.Marshal(e)
	return b
}

// ── Support & Resistance ──

// LevelType classifies the origin of an S/R level.
type LevelType string

const (
	LevelTypeHorizontal LevelType = "HORIZONTAL"   // swing high/low
	LevelTypeVWAP       LevelType = "VWAP"          // VWAP-derived
	LevelTypeEMA        LevelType = "EMA"           // EMA-derived (EMA9/EMA21)
	LevelTypePremarket  LevelType = "PREMARKET"     // pre-market high/low, prev-day close
	LevelTypeRound      LevelType = "ROUND_NUMBER"  // psychological level
)

// LevelStrength classifies how reliable a level is.
type LevelStrength string

const (
	LevelStrengthStrong LevelStrength = "STRONG"
	LevelStrengthMedium LevelStrength = "MEDIUM"
	LevelStrengthWeak   LevelStrength = "WEAK"
)

// SRLevel represents a detected support or resistance level.
type SRLevel struct {
	Price         int64         `json:"price"`          // level price in paise
	Type          LevelType     `json:"type"`           // origin of the level
	Strength      LevelStrength `json:"strength"`       // Strong/Medium/Weak
	TouchCount    int           `json:"touch_count"`    // number of times price touched this level
	IsResistance  bool          `json:"is_resistance"`  // true if resistance, false if support
	IsRoundNumber bool          `json:"is_round_number"`
	HasHTF        bool          `json:"has_htf"`        // has higher-timeframe confluence
	HasVolSpike   bool          `json:"has_vol_spike"`  // volume spike on touch
	LastTouchBar  int           `json:"last_touch_bar"` // bar index of last touch (for staleness)
	CreatedAt     time.Time     `json:"created_at"`
}

// LevelUpdateEvent is the published event with current active S/R levels.
type LevelUpdateEvent struct {
	Levels   []SRLevel `json:"levels"`
	Token    string    `json:"token"`
	Exchange string    `json:"exchange"`
	TF       int       `json:"tf"`
	TS       time.Time `json:"ts"`
}

// JSON returns the JSON-encoded event.
func (e *LevelUpdateEvent) JSON() []byte {
	b, _ := json.Marshal(e)
	return b
}

// ── Breakout ──

// BreakoutStage represents the stage of a breakout event.
type BreakoutStage string

const (
	BreakoutStageCompression BreakoutStage = "COMPRESSION" // BBW squeeze, vol drying up
	BreakoutStageBreak       BreakoutStage = "BREAK"       // candle closed beyond level
	BreakoutStageRetest      BreakoutStage = "RETEST"      // price retesting broken level
	BreakoutStageConfirmed   BreakoutStage = "CONFIRMED"   // retest held, breakout valid
	BreakoutStageFailed      BreakoutStage = "FAILED"      // price back inside level
)

// BreakoutEvent represents a detected breakout or pre-breakout signal.
type BreakoutEvent struct {
	Stage         BreakoutStage `json:"stage"`
	Level         int64         `json:"level"`           // the S/R level that was broken
	Direction     string        `json:"direction"`       // "UP" or "DOWN"
	BreakPrice    int64         `json:"break_price"`     // close of the break candle
	BreakVolume   int64         `json:"break_volume"`    // volume on break candle
	AvgVolume     float64       `json:"avg_volume"`      // 10-bar avg volume for comparison
	VolumeRatio   float64       `json:"volume_ratio"`    // break_volume / avg_volume
	ADXValue      float64       `json:"adx_value"`
	ADXRising     bool          `json:"adx_rising"`
	MeasuredMove  int64         `json:"measured_move"`   // target price (measured move)
	RangeHeight   int64         `json:"range_height"`    // range high - range low
	BarsSinceBreak int          `json:"bars_since_break"`
	Token         string        `json:"token"`
	Exchange      string        `json:"exchange"`
	TF            int           `json:"tf"`
	TS            time.Time     `json:"ts"`
}

// JSON returns the JSON-encoded event.
func (e *BreakoutEvent) JSON() []byte {
	b, _ := json.Marshal(e)
	return b
}
