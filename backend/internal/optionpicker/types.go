package optionpicker

import (
	"fmt"
	"time"
)

// Contract is one option from the Angel chain, joined with its feed token.
type Contract struct {
	Token, Symbol string
	Strike        int64  // index points
	Option        string // "CE" / "PE"
	Expiry        time.Time
	IV            float64 // percent, e.g. 14.2
	Liquidity     float64 // max(volume, OI) from the chain snapshot
}

// Quote is the latest feed state of one token. Prices in paise.
type Quote struct {
	LTP, Bid, Ask, OI int64
	At                time.Time // receive time of Bid/Ask
}

// Mid returns (bid+ask)/2, or 0 without both sides.
func (q Quote) Mid() int64 {
	if q.Bid <= 0 || q.Ask <= 0 {
		return 0
	}
	return (q.Bid + q.Ask) / 2
}

// Rules are the selection limits. Zero disables a rule unless noted.
// MaxThetaPct and MaxGamma are not here: the theta/gamma decay a strategy
// will tolerate is a property of that strategy's intent (SR and RANGE want
// different caps), not a global picker limit, so they live on SingleIntent.
type Rules struct {
	MaxSpreadPct float64       // (ask−bid)/mid × 100
	MaxQuoteAge  time.Duration // required (> 0)
	MaxChainAge  time.Duration // required (> 0)
	GammaDTE     int           // window (days to expiry) an intent's MaxGamma applies within
	MaxBuyIV     float64       // percent
	MinSellIV    float64       // percent, average of sold legs
	MinLiquidity float64       // max(live OI, chain liquidity)
	CostMultiple int64         // bought: |delta| × TargetMove ≥ k × (ask − bid)
	RatePct      float64       // risk-free rate for greeks, e.g. 6.5
	// Rank chooses the winner among contracts that pass every rule:
	// RankDelta (default, "") = |delta| closest to the band middle;
	// RankReturn = highest expected return on premium for the intent's
	// TargetMove (falls back to RankDelta when TargetMove is 0).
	Rank        string
	HoldMinutes float64 // expected holding time for RankReturn's decay term
}

const (
	RankDelta  = "delta"
	RankReturn = "return"
)

// SingleIntent asks for one bought option.
type SingleIntent struct {
	Strategy           string
	Option             string // "CE" / "PE"
	DeltaMin, DeltaMax float64
	MinDTE             int
	TargetMove         int64   // index paise to target; 0 = no cost rule
	MaxThetaPct        float64 // |theta|/day ≤ this % of premium, 0 = off
	MaxGamma           float64 // gamma cap within Rules.GammaDTE days, 0 = off
}

// CondorIntent asks for a short iron condor anchored at the range edges.
type CondorIntent struct {
	Strategy       string
	ShortCEAtLeast int64 // index points
	ShortPEAtMost  int64
	MaxShortDelta  float64
	WingWidth      int64 // index points
	MinDTE         int
	MinCreditPct   int64 // net credit ≥ this % of the wing width
}

// Pick is a chosen contract with the greeks and quote it was chosen on.
type Pick struct {
	Contract
	Delta, Gamma, Theta, Vega float64
	Quote                     Quote
	DTE                       int
	// Score is the expected return on premium (0.25 = 25 %) for the
	// intent's TargetMove; 0 when there is no target.
	Score float64
}

// CondorPick is the four legs of a condor and its net credit (paise).
type CondorPick struct {
	ShortCE, LongCE, ShortPE, LongPE Pick
	Credit                           int64
}

// Rejects counts candidates dropped per rule name.
type Rejects map[string]int

// String lists rejects as "delta 12, spread 1" in rule order.
func (r Rejects) String() string {
	order := []string{"not streamed", "quote stale", "crossed", "spread", "no iv", "delta", "theta", "gamma", "iv", "liquidity", "cost", "credit", "sell iv"}
	s := ""
	for _, k := range order {
		if n := r[k]; n > 0 {
			if s != "" {
				s += ", "
			}
			s += fmt.Sprintf("%s %d", k, n)
		}
	}
	if s == "" {
		return "none"
	}
	return s
}

// Refusal is returned when nothing passes; Reason names the cause.
type Refusal struct {
	Reason  string
	Rejects Rejects
}

func (r *Refusal) Error() string { return r.Reason }
