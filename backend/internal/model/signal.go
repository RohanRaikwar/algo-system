package model

import "time"

// Signal represents a trading signal emitted by a strategy.
type Signal struct {
	StrategyName  string       `json:"strategy_name"`
	Action        Action       `json:"action"` // BUY, SELL, EXIT
	Side          PositionSide `json:"side"`   // CALL, PUT, NONE
	ReverseTo     PositionSide `json:"reverse_to,omitempty"`
	Token         string       `json:"token"`
	Exchange      string       `json:"exchange"`
	Qty           int64        `json:"qty"`
	Price         int64        `json:"price"`                  // FNO LTP at signal time (paise)
	EntryFNOPrice int64        `json:"entry_fno_price"`        // FNO option price at entry (paise, set on exit signals)
	MarketState   string       `json:"market_state,omitempty"` // trending/sideways/choppy/range
	Reason        string       `json:"reason"`
	// Warnings are situation flags on an entry that was still taken
	// (NIFTY50_SR: day_extreme, sideways).
	Warnings []string `json:"warnings,omitempty"`

	// ── Multi-leg (paper-only) ──
	// A strategy emits one basket signal with Legs; stratengine resolves
	// each strike and fans it out into one executor signal per leg with
	// Leg/FNOToken/Short set. The executor keeps leg positions apart
	// (strategy|side|leg) and never sends them to the broker.
	Legs      []LegSpec `json:"legs,omitempty"`
	Leg       string    `json:"leg,omitempty"`        // leg id, e.g. SHORT_CE
	FNOToken  string    `json:"fno_token,omitempty"`  // option token of this leg
	FNOSymbol string    `json:"fno_symbol,omitempty"` // option symbol of this leg
	Strike    int64     `json:"strike,omitempty"`     // strike in index points
	Short     bool      `json:"short,omitempty"`      // leg is sold to open

	// Pick names the picker that chose the traded contract: "old"
	// (pickSRStrike path) or "new" (optionpicker). PickAlt is the other
	// picker's contract when it differs (shadow mode). Display only.
	Pick    string `json:"pick,omitempty"`
	PickAlt string `json:"pick_alt,omitempty"`

	// TargetMove is the index move (paise) from entry to target, so the
	// engine can check the expected option gain covers trading costs.
	TargetMove int64 `json:"target_move,omitempty"`

	// SameDayExpiry asks for today's expiry contract (gamma blast); every
	// other strategy trades the nearest expiry after today.
	SameDayExpiry bool `json:"same_day_expiry,omitempty"`

	// SignalTS is the market time of the data that produced the signal:
	// the close of the candle, or the tick's canonical time. The engine
	// stamps it; it makes a real entry's order identity deterministic, so
	// a replayed signal is recognised instead of sent twice. Zero when
	// unknown (injected signals).
	SignalTS time.Time `json:"signal_ts,omitempty"`
}

// LegSpec is one option leg of a basket signal.
type LegSpec struct {
	Leg        string `json:"leg"`
	Strike     int64  `json:"strike"`      // index points, e.g. 24200
	OptionType string `json:"option_type"` // CE or PE
	Short      bool   `json:"short"`
	Token      string `json:"token,omitempty"`  // filled by stratengine
	Symbol     string `json:"symbol,omitempty"` // filled by stratengine
}

// Side returns the position side a leg trades (CE → CALL, PE → PUT).
func (l LegSpec) Side() PositionSide {
	if l.OptionType == "PE" {
		return SidePut
	}
	return SideCall
}

// Action represents a trading action.
type Action string

const (
	ActionBuy  Action = "BUY"
	ActionSell Action = "SELL"
	ActionExit Action = "EXIT"
	// ActionWatchBuy is entry advice: stratengine publishes it and never
	// places an order or opens a position for it.
	ActionWatchBuy Action = "WATCH_BUY"
)

// PositionSide represents which option leg is active.
type PositionSide string

const (
	SideNone PositionSide = "NONE"
	SideCall PositionSide = "CALL"
	SidePut  PositionSide = "PUT"
)
