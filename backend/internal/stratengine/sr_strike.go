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
//  the nearest expiry after today by greeks:
//    |delta| in [SRDeltaMin, SRDeltaMax]   (directional exposure)
//    |theta| ≤ SRMaxThetaPct % of premium  (no decay trap)
//    gamma ≤ SRMaxGamma within SRGammaDTE days of expiry (no gamma whipsaw)
//    liquidity ≥ RangeMinLiquidity
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
}

func (svc *Service) srGreekLimits() srGreekLimits {
	return srGreekLimits{
		DeltaMin: svc.cfg.SRDeltaMin, DeltaMax: svc.cfg.SRDeltaMax,
		MaxThetaPct: svc.cfg.SRMaxThetaPct, MaxGamma: svc.cfg.SRMaxGamma,
		GammaDTE: svc.cfg.SRGammaDTE, MinLiquidity: float64(svc.cfg.RangeMinLiquidity),
	}
}

func (svc *Service) isSRSignal(sig *strategy.Signal) bool {
	return svc.srStrategy != nil && sig.StrategyName == svc.srStrategy.Name()
}

// pickSRStrike sets sig.Strike to the contract the greeks select.
func (svc *Service) pickSRStrike(sig *strategy.Signal, now time.Time) error {
	chain, err := svc.optionChain(now)
	if err != nil {
		return fmt.Errorf("SR strike: no greeks (%v)", err)
	}
	opt := optionTypeFor(sig.Side)
	// OptionGreek carries no LTP; premiums come from the subscribed ladder.
	premium := func(c orderexec.OptionContract) float64 {
		if c.Premium > 0 {
			return c.Premium
		}
		info, err := svc.resolverForLegs().ResolveStrike(now, c.Strike, opt)
		if err != nil {
			return 0
		}
		return float64(svc.orderExecutor.GetLTP(info.Token)) / 100
	}
	c, err := selectSRContract(chain, opt, now, svc.srGreekLimits(), premium)
	if err != nil {
		return err
	}
	log.Printf("[stratengine] SR strike %d%s (asked %d): delta=%.2f gamma=%.4f theta=%.2f premium=%.2f iv=%.1f",
		c.Strike, c.OptionType, sig.Strike, c.Delta, c.Gamma, c.Theta, premium(c), normIV(c.IV))
	sig.Strike = c.Strike
	return nil
}

// selectSRContract applies the greek filters to one side of the chain.
// premium returns a contract's premium in rupees (the chain's unit), 0 if unknown.
func selectSRContract(chain []orderexec.OptionContract, opt string, now time.Time, lim srGreekLimits,
	premium func(orderexec.OptionContract) float64) (orderexec.OptionContract, error) {
	today := dayStart(now)
	var expiry time.Time
	for _, c := range chain {
		if string(c.OptionType) != opt || c.Expiry.IsZero() || !dayStart(c.Expiry).After(today) {
			continue
		}
		if expiry.IsZero() || c.Expiry.Before(expiry) {
			expiry = c.Expiry
		}
	}
	if expiry.IsZero() {
		return orderexec.OptionContract{}, fmt.Errorf("SR strike: no %s contracts after today in chain", opt)
	}
	dte := int(dayStart(expiry).Sub(today).Hours() / 24)

	mid := (lim.DeltaMin + lim.DeltaMax) / 2
	var best orderexec.OptionContract
	found := false
	var nDelta, nPrem, nTheta, nGamma, nLiq int
	for _, c := range chain {
		if string(c.OptionType) != opt || !sameDay(c.Expiry, expiry) {
			continue
		}
		d := math.Abs(c.Delta)
		if d < lim.DeltaMin || d > lim.DeltaMax {
			nDelta++
			continue
		}
		p := premium(c)
		switch {
		case p <= 0:
			nPrem++
			continue
		case lim.MaxThetaPct > 0 && math.Abs(c.Theta)*100 > p*lim.MaxThetaPct:
			nTheta++
			continue
		case lim.MaxGamma > 0 && dte <= lim.GammaDTE && c.Gamma > lim.MaxGamma:
			nGamma++
			continue
		case lim.MinLiquidity > 0 && c.LiquidityScore < lim.MinLiquidity:
			nLiq++
			continue
		}
		if !found {
			best, found = c, true
			continue
		}
		db, dc := math.Abs(math.Abs(best.Delta)-mid), math.Abs(d-mid)
		if dc < db || (dc == db && c.LiquidityScore > best.LiquidityScore) {
			best = c
		}
	}
	if !found {
		return orderexec.OptionContract{}, fmt.Errorf("SR strike: no %s passes greeks (dte=%d; out: delta %d, no premium %d, theta %d, gamma %d, liquidity %d)",
			opt, dte, nDelta, nPrem, nTheta, nGamma, nLiq)
	}
	return best, nil
}

func dayStart(t time.Time) time.Time {
	t = t.In(istSR)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, istSR)
}
