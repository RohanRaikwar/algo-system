package stratengine

import (
	"fmt"
	"log"
	"math"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50_SR strike selection from the live option chain — no fallback.
//
//  The strategy asks for ATM; the contract bought is picked here from
//  the nearest expiry at least SRMinDTE days out by greeks:
//    |delta| in [SRDeltaMin, SRDeltaMax]   (directional exposure)
//    |theta| ≤ SRMaxThetaPct % of premium  (no decay trap)
//    gamma ≤ SRMaxGamma within SRGammaDTE days of expiry (no gamma whipsaw)
//    liquidity ≥ RangeMinLiquidity
//    IV ≤ RangeMaxBuyIV                    (premium not too expensive)
//    |delta| × target move ≥ RangeCostMultiple × round-trip slippage
//  Closest to the middle of the delta band wins; ties go to liquidity.
//  Nothing passes → the entry is refused.
// ════════════════════════════════════════════════════════════════════

var istSR = time.FixedZone("IST", 5*3600+30*60)

type srGreekLimits struct {
	DeltaMin, DeltaMax float64
	MaxThetaPct        float64
	MaxGamma           float64
	GammaDTE           int
	MinLiquidity       float64
	MaxBuyIV           float64 // %, 0 = off
	MinDTE             int     // skip expiries fewer days out (1 = never same day)
	// Cost rule, only with a target (entry signals): |delta| × TargetMove
	// (index paise) must cover CostMultiple × round-trip Slippage.
	CostMultiple int64
	TargetMove   int64
	Slippage     func(ltpPaise int64) int64
}

func (svc *Service) srGreekLimits() srGreekLimits {
	lim := srGreekLimits{
		DeltaMin: svc.cfg.SRDeltaMin, DeltaMax: svc.cfg.SRDeltaMax,
		MaxThetaPct: svc.cfg.SRMaxThetaPct, MaxGamma: svc.cfg.SRMaxGamma,
		GammaDTE: svc.cfg.SRGammaDTE, MinLiquidity: float64(svc.cfg.RangeMinLiquidity),
		MaxBuyIV: svc.cfg.RangeMaxBuyIV, MinDTE: svc.cfg.SRMinDTE,
		CostMultiple: svc.cfg.RangeCostMultiple,
	}
	if svc.orderExecutor != nil {
		lim.Slippage = svc.orderExecutor.PaperSlippage
	}
	return lim
}

func (svc *Service) isSRSignal(sig *strategy.Signal) bool {
	return svc.srStrategy != nil && sig.StrategyName == svc.srStrategy.Name()
}

// pickSRStrike sets sig.Strike to the contract the greeks select and
// returns it; the caller resolves that exact expiry.
func (svc *Service) pickSRStrike(sig *strategy.Signal, now time.Time) (orderexec.OptionContract, error) {
	chain, err := svc.optionChain(now)
	if err != nil {
		return orderexec.OptionContract{}, fmt.Errorf("SR strike: no greeks (%v)", err)
	}
	opt := optionTypeFor(sig.Side)
	premium := svc.srPremium(now, opt)
	lim := svc.srGreekLimits()
	lim.TargetMove = sig.TargetMove
	c, err := selectSRContract(chain, opt, now, lim, premium)
	if err != nil {
		return orderexec.OptionContract{}, err
	}
	log.Printf("[stratengine] SR strike %d%s (asked %d): delta=%.2f gamma=%.4f theta=%.2f premium=%.2f iv=%.1f",
		c.Strike, c.OptionType, sig.Strike, c.Delta, c.Gamma, c.Theta, premium(c), normIV(c.IV))
	svc.recordSRPick(*sig, c, premium(c), now)
	sig.Strike = c.Strike
	return c, nil
}

// expiryResolver resolves a contract on a given expiry (the strike picker).
type expiryResolver interface {
	ResolveStrikeOn(expiry time.Time, strike int64, optionType string) (orderexec.StrikeInfo, error)
}

// resolveOn resolves strike/opt on expiry; without an expiry (or a resolver
// that cannot pick one) it falls back to the nearest expiry.
func (svc *Service) resolveOn(now, expiry time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	r := svc.resolverForLegs()
	if er, ok := r.(expiryResolver); ok && !expiry.IsZero() {
		return er.ResolveStrikeOn(expiry, strike, opt)
	}
	return r.ResolveStrike(now, strike, opt)
}

// srPremium returns a contract's premium in rupees. OptionGreek carries no
// LTP; premiums come from the subscribed ladder.
func (svc *Service) srPremium(now time.Time, opt string) func(orderexec.OptionContract) float64 {
	return func(c orderexec.OptionContract) float64 {
		if c.Premium > 0 {
			return c.Premium
		}
		info, err := svc.resolveOn(now, c.Expiry, c.Strike, opt)
		if err != nil {
			return 0
		}
		return float64(svc.orderExecutor.GetLTP(info.Token)) / 100
	}
}

// srRejects counts contracts of the chosen expiry dropped by each filter.
type srRejects struct {
	Delta     int `json:"delta"`
	Premium   int `json:"premium"`
	Theta     int `json:"theta"`
	Gamma     int `json:"gamma"`
	Liquidity int `json:"liquidity"`
	IV        int `json:"iv"`
	Cost      int `json:"cost"`
}

// srEval is one side's greek selection: the winner (if any), the expiry
// examined and why the other contracts were dropped.
type srEval struct {
	Best    orderexec.OptionContract
	Found   bool
	Expiry  time.Time
	DTE     int
	Rejects srRejects
}

// selectSRContract applies the greek filters to one side of the chain.
// premium returns a contract's premium in rupees (the chain's unit), 0 if unknown.
func selectSRContract(chain []orderexec.OptionContract, opt string, now time.Time, lim srGreekLimits,
	premium func(orderexec.OptionContract) float64) (orderexec.OptionContract, error) {
	ev, err := evalSRContracts(chain, opt, now, lim, premium)
	if err != nil {
		return orderexec.OptionContract{}, err
	}
	if !ev.Found {
		r := ev.Rejects
		return orderexec.OptionContract{}, fmt.Errorf("SR strike: no %s passes greeks (dte=%d; out: delta %d, no premium %d, theta %d, gamma %d, liquidity %d, iv %d, cost %d)",
			opt, ev.DTE, r.Delta, r.Premium, r.Theta, r.Gamma, r.Liquidity, r.IV, r.Cost)
	}
	return ev.Best, nil
}

// evalSRContracts runs the greek filters and reports the winner and rejects.
// It errors only when the chain has no contract of this type after today.
func evalSRContracts(chain []orderexec.OptionContract, opt string, now time.Time, lim srGreekLimits,
	premium func(orderexec.OptionContract) float64) (srEval, error) {
	today := dayStart(now)
	minDTE := lim.MinDTE
	if minDTE < 1 {
		minDTE = 1 // never same-day expiry
	}
	var ev srEval
	for _, c := range chain {
		if string(c.OptionType) != opt || c.Expiry.IsZero() || daysBetween(today, c.Expiry) < minDTE {
			continue
		}
		if ev.Expiry.IsZero() || c.Expiry.Before(ev.Expiry) {
			ev.Expiry = c.Expiry
		}
	}
	if ev.Expiry.IsZero() {
		return ev, fmt.Errorf("SR strike: no %s contracts %d+ days out in chain", opt, minDTE)
	}
	ev.DTE = daysBetween(today, ev.Expiry)

	mid := (lim.DeltaMin + lim.DeltaMax) / 2
	for _, c := range chain {
		if string(c.OptionType) != opt || !sameDay(c.Expiry, ev.Expiry) {
			continue
		}
		d := math.Abs(c.Delta)
		if d < lim.DeltaMin || d > lim.DeltaMax {
			ev.Rejects.Delta++
			continue
		}
		p := premium(c)
		switch {
		case p <= 0:
			ev.Rejects.Premium++
			continue
		case lim.MaxThetaPct > 0 && math.Abs(c.Theta)*100 > p*lim.MaxThetaPct:
			ev.Rejects.Theta++
			continue
		case lim.MaxGamma > 0 && ev.DTE <= lim.GammaDTE && c.Gamma > lim.MaxGamma:
			ev.Rejects.Gamma++
			continue
		case lim.MinLiquidity > 0 && c.LiquidityScore < lim.MinLiquidity:
			ev.Rejects.Liquidity++
			continue
		case lim.MaxBuyIV > 0 && normIV(c.IV) > lim.MaxBuyIV:
			ev.Rejects.IV++
			continue
		case !srCostOK(lim, d, p):
			ev.Rejects.Cost++
			continue
		}
		if !ev.Found {
			ev.Best, ev.Found = c, true
			continue
		}
		db, dc := math.Abs(math.Abs(ev.Best.Delta)-mid), math.Abs(d-mid)
		if dc < db || (dc == db && c.LiquidityScore > ev.Best.LiquidityScore) {
			ev.Best = c
		}
	}
	return ev, nil
}

// srCostOK: expected premium gain |delta| × target move covers the
// round-trip cost multiple (same rule as entryQualityCheck). premium is rupees.
func srCostOK(lim srGreekLimits, absDelta, premium float64) bool {
	if lim.CostMultiple <= 0 || lim.TargetMove <= 0 || lim.Slippage == nil {
		return true
	}
	roundTrip := 2 * lim.Slippage(int64(math.Round(premium*100)))
	gain := lim.TargetMove * int64(math.Round(absDelta*1000)) / 1000
	return roundTrip <= 0 || gain >= lim.CostMultiple*roundTrip
}

// daysBetween counts IST calendar days from today to t's day.
func daysBetween(today, t time.Time) int {
	return int(dayStart(t).Sub(today).Hours() / 24)
}

func dayStart(t time.Time) time.Time {
	t = t.In(istSR)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, istSR)
}
