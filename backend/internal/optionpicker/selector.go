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

// baseCheck applies the rules every leg needs: streamed, fresh two-sided
// quote, spread, IV present. Greeks are computed at the live spot.
func baseCheck(c Contract, dte int, r Rules, env Env) evaluated {
	if c.Token == "" {
		return evaluated{failed: "not streamed"}
	}
	q, ok := env.Quote(c.Token)
	if !ok || q.Bid <= 0 || q.Ask <= 0 || q.Ask < q.Bid || env.Now.Sub(q.At) > r.MaxQuoteAge {
		return evaluated{failed: "quote stale"}
	}
	mid := q.Mid()
	if r.MaxSpreadPct > 0 && float64(q.Ask-q.Bid)*100 > float64(mid)*r.MaxSpreadPct {
		return evaluated{failed: "spread"}
	}
	if c.IV <= 0 {
		return evaluated{failed: "no iv"}
	}
	g := optionmath.GreeksAt(float64(env.Spot)/100, float64(c.Strike), optionmath.YearsTo(c.Expiry, env.Now),
		c.IV/100, r.RatePct/100, c.Option == "CE")
	return evaluated{pick: Pick{Contract: c, Delta: g.Delta, Gamma: g.Gamma, Theta: g.Theta, Vega: g.Vega, Quote: q, DTE: dte}}
}

func liquidityOK(p Pick, r Rules) bool {
	return r.MinLiquidity <= 0 || math.Max(float64(p.Quote.OI), p.Liquidity) >= r.MinLiquidity
}

// buyCheck adds the bought-option rules to a base-checked pick.
func buyCheck(p Pick, in SingleIntent, r Rules) string {
	d := math.Abs(p.Delta)
	premium := float64(p.Quote.Mid()) / 100 // rupees
	switch {
	case d < in.DeltaMin || d > in.DeltaMax:
		return "delta"
	case in.MaxThetaPct > 0 && math.Abs(p.Theta)*100 > premium*in.MaxThetaPct:
		return "theta"
	case in.MaxGamma > 0 && p.DTE <= r.GammaDTE && p.Gamma > in.MaxGamma:
		return "gamma"
	case r.MaxBuyIV > 0 && p.IV > r.MaxBuyIV:
		return "iv"
	case !liquidityOK(p, r):
		return "liquidity"
	case r.CostMultiple > 0 && in.TargetMove > 0 &&
		in.TargetMove*int64(math.Round(d*1000))/1000 < r.CostMultiple*(p.Quote.Ask-p.Quote.Bid):
		return "cost"
	}
	return ""
}

// SelectSingle picks the bought option closest to the delta band's middle
// (tie → tighter spread) on the nearest expiry ≥ MinDTE days out.
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
	mid := (in.DeltaMin + in.DeltaMax) / 2
	byReturn := r.Rank == RankReturn && in.TargetMove > 0
	var best Pick
	found := false
	for _, c := range chain {
		if c.Option != in.Option || DaysBetween(env.Now, c.Expiry) != dte || !c.Expiry.Equal(expiry) {
			continue
		}
		ev := baseCheck(c, dte, r, env)
		if ev.failed == "" {
			ev.failed = buyCheck(ev.pick, in, r)
		}
		if ev.failed != "" {
			rej[ev.failed]++
			continue
		}
		p := ev.pick
		p.Score = expectedReturn(p, in.TargetMove, r.HoldMinutes)
		if !found {
			best, found = p, true
			continue
		}
		if byReturn && p.Score != best.Score {
			if p.Score > best.Score {
				best = p
			}
			continue
		}
		db, dp := math.Abs(math.Abs(best.Delta)-mid), math.Abs(math.Abs(p.Delta)-mid)
		if dp < db || (dp == db && p.Quote.Ask-p.Quote.Bid < best.Quote.Ask-best.Quote.Bid) {
			best = p
		}
	}
	if !found {
		return Pick{}, rej, &Refusal{Reason: fmt.Sprintf("no %s passes (dte %d; rejected %s)", in.Option, dte, rej), Rejects: rej}
	}
	return best, rej, nil
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

// SelectCondor builds a short iron condor past the range edges. All legs
// or nothing.
func SelectCondor(chain []Contract, in CondorIntent, r Rules, env Env) (CondorPick, Rejects, error) {
	rej := Rejects{}
	if err := checkEnv(r, env); err != nil {
		return CondorPick{}, rej, err
	}
	expiry, dte, ok := pickExpiry(chain, "CE", env.Now, in.MinDTE)
	if !ok {
		return CondorPick{}, rej, &Refusal{Reason: "no condor expiry"}
	}
	byKey := make(map[string]Contract, len(chain))
	for _, c := range chain {
		if c.Expiry.Equal(expiry) {
			byKey[fmt.Sprintf("%d%s", c.Strike, c.Option)] = c
		}
	}
	leg := func(strike int64, opt string, short bool) (Pick, bool) {
		c, ok := byKey[fmt.Sprintf("%d%s", strike, opt)]
		if !ok {
			rej["not streamed"]++
			return Pick{}, false
		}
		ev := baseCheck(c, dte, r, env)
		if ev.failed == "" && !liquidityOK(ev.pick, r) {
			ev.failed = "liquidity"
		}
		if ev.failed == "" && short && math.Abs(ev.pick.Delta) > in.MaxShortDelta {
			ev.failed = "delta"
		}
		if ev.failed != "" {
			rej[ev.failed]++
			return Pick{}, false
		}
		return ev.pick, true
	}
	side := func(opt string, from, dir int64) (Pick, Pick, bool) {
		const maxSteps = 12
		step := int64(50)
		for i := int64(0); i < maxSteps; i++ {
			k := from + dir*i*step
			s, ok := leg(k, opt, true)
			if !ok {
				continue
			}
			l, ok := leg(k+dir*in.WingWidth, opt, false)
			if !ok {
				continue
			}
			return s, l, true
		}
		return Pick{}, Pick{}, false
	}
	sCE, lCE, okCE := side("CE", in.ShortCEAtLeast, +1)
	sPE, lPE, okPE := side("PE", in.ShortPEAtMost, -1)
	if !okCE || !okPE {
		return CondorPick{}, rej, &Refusal{Reason: fmt.Sprintf("no condor legs pass (rejected %s)", rej), Rejects: rej}
	}
	if r.MinSellIV > 0 && (sCE.IV+sPE.IV)/2 < r.MinSellIV {
		rej["sell iv"]++
		return CondorPick{}, rej, &Refusal{Reason: fmt.Sprintf("short legs IV %.1f%% < %.1f%%", (sCE.IV+sPE.IV)/2, r.MinSellIV), Rejects: rej}
	}
	credit := sCE.Quote.Bid + sPE.Quote.Bid - lCE.Quote.Ask - lPE.Quote.Ask
	need := in.WingWidth * 100 * in.MinCreditPct / 100
	switch {
	case credit <= 0:
		rej["credit"]++
		return CondorPick{}, rej, &Refusal{Reason: fmt.Sprintf("credit %d paise is not positive", credit), Rejects: rej}
	case in.MinCreditPct > 0 && credit < need:
		rej["credit"]++
		return CondorPick{}, rej, &Refusal{Reason: fmt.Sprintf("credit %d < %d paise", credit, need), Rejects: rej}
	}
	return CondorPick{ShortCE: sCE, LongCE: lCE, ShortPE: sPE, LongPE: lPE, Credit: credit}, rej, nil
}
