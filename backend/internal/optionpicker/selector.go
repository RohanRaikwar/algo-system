package optionpicker

import (
	"fmt"
	"math"
	"time"

	"trading-systemv1/internal/optionmath"
)

var istZone = time.FixedZone("IST", 5*3600+30*60)

type Env struct {
	Spot    int64 // paise
	Now     time.Time
	ChainAt time.Time
	Quote   func(token string) (Quote, bool)
}

// DaysBetween counts IST calendar days from now's date to expiry's date.
func DaysBetween(now, expiry time.Time) int {
	a, b := now.In(istZone), expiry.In(istZone)
	d0 := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, istZone)
	d1 := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, istZone)
	return int(d1.Sub(d0).Hours() / 24)
}

// checkEnv refuses selection on missing spot or a stale chain.
func checkEnv(r Rules, env Env) error {
	if env.Spot <= 0 {
		return &Refusal{Reason: "no spot"}
	}
	if age := env.Now.Sub(env.ChainAt); env.ChainAt.IsZero() || age > r.MaxChainAge {
		return &Refusal{Reason: fmt.Sprintf("greeks stale %s", age.Truncate(time.Second))}
	}
	return nil
}

// pickExpiry is the nearest expiry of opt at least minDTE (≥ 1) days out.
func pickExpiry(chain []Contract, opt string, now time.Time, minDTE int) (time.Time, int, bool) {
	if minDTE < 1 {
		minDTE = 1 // never same-day expiry
	}
	var best time.Time
	for _, c := range chain {
		if c.Option != opt || c.Expiry.IsZero() || DaysBetween(now, c.Expiry) < minDTE {
			continue
		}
		if best.IsZero() || c.Expiry.Before(best) {
			best = c.Expiry
		}
	}
	return best, DaysBetween(now, best), !best.IsZero()
}

// evaluated is a candidate with quote and live greeks, or the rule it failed.
type evaluated struct {
	pick   Pick
	failed string
}

// baseCheck keeps only tradable contracts: streamed, a fresh two-sided
// quote that is not crossed, and IV for the greeks. Greeks are computed at
// the live spot.
func baseCheck(c Contract, dte int, r Rules, env Env) evaluated {
	if c.Token == "" {
		return evaluated{failed: "not streamed"}
	}
	q, ok := env.Quote(c.Token)
	if !ok || q.Bid <= 0 || q.Ask <= 0 || env.Now.Sub(q.At) > r.MaxQuoteAge {
		return evaluated{failed: "quote stale"}
	}
	if q.Ask < q.Bid {
		return evaluated{failed: "crossed"}
	}
	if c.IV <= 0 {
		return evaluated{failed: "no iv"}
	}
	g := optionmath.GreeksAt(float64(env.Spot)/100, float64(c.Strike), optionmath.YearsTo(c.Expiry, env.Now),
		c.IV/100, r.RatePct/100, c.Option == "CE")
	return evaluated{pick: Pick{Contract: c, Delta: g.Delta, Gamma: g.Gamma, Theta: g.Theta, Vega: g.Vega, Quote: q, DTE: dte}}
}

func spreadOK(q Quote, r Rules) bool {
	return r.MaxSpreadPct <= 0 || float64(q.Ask-q.Bid)*100 <= float64(q.Mid())*r.MaxSpreadPct
}

func liquidityOK(p Pick, r Rules) bool {
	return r.MinLiquidity <= 0 || math.Max(float64(p.Quote.OI), p.Liquidity) >= r.MinLiquidity
}

// buyFailures lists every bought-option rule a tradable pick breaks, in
// Rejects order. Empty means it passes them all.
func buyFailures(p Pick, in SingleIntent, r Rules) []string {
	d := math.Abs(p.Delta)
	premium := float64(p.Quote.Mid()) / 100 // rupees
	var f []string
	if !spreadOK(p.Quote, r) {
		f = append(f, "spread")
	}
	if d < in.DeltaMin || d > in.DeltaMax {
		f = append(f, "delta")
	}
	if (in.MaxThetaPct > 0 && math.Abs(p.Theta)*100 > premium*in.MaxThetaPct) || !thetaGainOK(p, in, r) {
		f = append(f, "theta")
	}
	if in.MaxGamma > 0 && p.DTE <= r.GammaDTE && p.Gamma > in.MaxGamma {
		f = append(f, "gamma")
	}
	if r.MaxBuyIV > 0 && p.IV > r.MaxBuyIV {
		f = append(f, "iv")
	}
	if !liquidityOK(p, r) {
		f = append(f, "liquidity")
	}
	if r.CostMultiple > 0 && in.TargetMove > 0 &&
		in.TargetMove*int64(math.Round(d*1000))/1000 < r.CostMultiple*(p.Quote.Ask-p.Quote.Bid) {
		f = append(f, "cost")
	}
	return f
}

// thetaGainOK: premium lost to theta over the expected hold stays within
// ThetaMaxGainPct % of the expected gain |delta| × target. Unlike a flat
// cap on theta per day, it does not refuse every ATM weekly a few days
// before expiry, where an intraday hold pays only a fraction of the decay.
func thetaGainOK(p Pick, in SingleIntent, r Rules) bool {
	if in.ThetaMaxGainPct <= 0 || r.HoldMinutes <= 0 || in.TargetMove <= 0 {
		return true
	}
	decay := math.Abs(p.Theta) * r.HoldMinutes / tradingMinutesPerDay // rupees
	gain := math.Abs(p.Delta) * float64(in.TargetMove) / 100          // rupees
	return decay*100 <= gain*in.ThetaMaxGainPct
}

// SelectSingle picks the best tradable bought option on the nearest expiry
// ≥ MinDTE days out. Tradable (baseCheck) is the only hard filter; the
// other rules rank. Best is, in order: fewest rules broken, then |delta|
// nearest the band, then the Rank (highest expected return, or |delta|
// nearest the band middle), then the tighter spread. A pick that breaks
// rules lists them in Waived. It refuses only when nothing is tradable.
func SelectSingle(chain []Contract, in SingleIntent, r Rules, env Env) (Pick, Rejects, error) {
	rej := Rejects{}
	if err := checkEnv(r, env); err != nil {
		return Pick{}, rej, err
	}
	expiry, dte, ok := pickExpiry(chain, in.Option, env.Now, in.MinDTE)
	if !ok {
		minDTE := in.MinDTE
		if minDTE < 1 {
			minDTE = 1
		}
		return Pick{}, rej, &Refusal{Reason: fmt.Sprintf("no %s expiry %d+ days out", in.Option, minDTE)}
	}
	var best Pick
	found := false
	for _, c := range chain {
		if c.Option != in.Option || DaysBetween(env.Now, c.Expiry) != dte || !c.Expiry.Equal(expiry) {
			continue
		}
		ev := baseCheck(c, dte, r, env)
		if ev.failed != "" {
			rej[ev.failed]++
			continue
		}
		p := ev.pick
		p.Waived = buyFailures(p, in, r)
		for _, f := range p.Waived {
			rej[f]++
		}
		p.Score = expectedReturn(p, in.TargetMove, r.HoldMinutes)
		if !found || betterBuy(p, best, in, r) {
			best, found = p, true
		}
	}
	if !found {
		return Pick{}, rej, &Refusal{Reason: fmt.Sprintf("no %s tradable (dte %d; rejected %s)", in.Option, dte, rej), Rejects: rej}
	}
	return best, rej, nil
}

// betterBuy reports whether p ranks above best (see SelectSingle).
func betterBuy(p, best Pick, in SingleIntent, r Rules) bool {
	if len(p.Waived) != len(best.Waived) {
		return len(p.Waived) < len(best.Waived)
	}
	outside := func(x Pick) float64 {
		d := math.Abs(x.Delta)
		return math.Max(0, math.Max(in.DeltaMin-d, d-in.DeltaMax))
	}
	if op, ob := outside(p), outside(best); op != ob {
		return op < ob
	}
	if r.Rank == RankReturn && in.TargetMove > 0 && p.Score != best.Score {
		return p.Score > best.Score
	}
	mid := (in.DeltaMin + in.DeltaMax) / 2
	db, dp := math.Abs(math.Abs(best.Delta)-mid), math.Abs(math.Abs(p.Delta)-mid)
	return dp < db || (dp == db && p.Quote.Ask-p.Quote.Bid < best.Quote.Ask-best.Quote.Bid)
}

// tradingMinutesPerDay spreads a calendar day's theta over the session
// (09:15–15:30), where intraday decay is realised.
const tradingMinutesPerDay = 375

// expectedReturn is the option's expected gain for an index move of move
// paise — delta and gamma, less theta over holdMin minutes and the spread
// paid to get in and out — as a fraction of the ask. 0 without a target.
func expectedReturn(p Pick, move int64, holdMin float64) float64 {
	ask := float64(p.Quote.Ask) / 100
	if move <= 0 || ask <= 0 {
		return 0
	}
	m := float64(move) / 100 // index points
	gain := math.Abs(p.Delta)*m + 0.5*p.Gamma*m*m -
		math.Abs(p.Theta)*holdMin/tradingMinutesPerDay -
		float64(p.Quote.Ask-p.Quote.Bid)/100
	return gain / ask
}
