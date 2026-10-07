// Package exitpolicy decides when an open option position should be closed
// for reasons that are not specific to one strategy. Strategies declare a
// Plan (rules + mode) per position; the Arbiter evaluates it and returns
// typed decisions. It does not place orders: stratengine turns an act
// decision into the owning strategy's exit signal.
//
// Money is int64 paise throughout. Float greeks enter only through
// NewEntryGreeks, once, at entry.
package exitpolicy

import (
	"fmt"
	"math"
	"time"
)

// Reason is the typed code of an exit decision.
type Reason string

const (
	ReasonTheta Reason = "THETA"
)

// Mode says what happens to a strategy's decisions.
type Mode string

const (
	ModeOff    Mode = "off"    // not tracked
	ModeShadow Mode = "shadow" // recorded only
	ModeAct    Mode = "act"    // position is closed
)

func (m Mode) valid() bool { return m == ModeOff || m == ModeShadow || m == ModeAct }

// sessionMinutes spreads a calendar day's theta over the trading session,
// as the option picker does.
const sessionMinutes = 375

// EntryGreeks are the pick's greeks frozen at entry.
type EntryGreeks struct {
	DeltaMilli       int64 // delta × 1000, signed (PUT < 0)
	DecayPerDayPaise int64 // |theta| per day, paise
	ExpGainPaise     int64 // |delta| × target move, premium paise
}

// NewEntryGreeks converts the picker's float greeks (theta in rupees per
// calendar day) and the strategy's target move (index paise). It returns
// nil when delta or the target is missing: the theta rule then runs on
// time only.
func NewEntryGreeks(delta, thetaRupeesPerDay float64, targetMovePaise int64) *EntryGreeks {
	if delta == 0 || targetMovePaise <= 0 || math.IsNaN(delta) || math.IsNaN(thetaRupeesPerDay) {
		return nil
	}
	return &EntryGreeks{
		DeltaMilli:       int64(math.Round(delta * 1000)),
		DecayPerDayPaise: int64(math.Round(math.Abs(thetaRupeesPerDay) * 100)),
		ExpGainPaise:     int64(math.Round(math.Abs(delta) * float64(targetMovePaise))),
	}
}

// Position is one open option position as the arbiter tracks it.
type Position struct {
	Key        string // strategy|side
	Strategy   string
	Side       string // CALL or PUT
	IndexToken string
	FNOToken   string
	EntryTS    time.Time
	IndexEntry int64
	PremEntry  int64
	Greeks     *EntryGreeks
}

// Snapshot is the market as a rule sees it.
type Snapshot struct {
	Now          time.Time
	IndexLTP     int64
	PremLTP      int64
	PremBest     int64
	ProtectArmed bool // strategy has moved its stop to protect profit
}

// Decision is a rule asking for the position to close.
type Decision struct {
	Position Position
	Reason   Reason
	Text     string
	Shadow   bool
	Detail   map[string]int64
	TS       time.Time
}

// Rule is one exit condition. Rules are stateless; debouncing is the
// arbiter's job.
type Rule interface {
	Name() Reason
	Evaluate(Position, Snapshot) (Decision, bool)
	ConfirmMinutes() int
}

// Plan is the rules a position runs under, frozen at Open.
type Plan struct {
	Mode  Mode
	Rules []Rule
}

func rupees(paise int64) string {
	sign := ""
	if paise < 0 {
		sign, paise = "-", -paise
	}
	return fmt.Sprintf("%s₹%d.%02d", sign, paise/100, paise%100)
}
