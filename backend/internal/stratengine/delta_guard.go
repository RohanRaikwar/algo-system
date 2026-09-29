package stratengine

import (
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// ════════════════════════════════════════════════════════════════════
//  Delta guard for range entries — no fallback.
//
//  The strike rule (ATM, 1 OTM in a wide range) targets the guide's
//  delta band. Near expiry an OTM strike's delta collapses, so the chosen
//  contract's delta is checked against OptionGreek:
//    band = [0.40, 0.60] − 0.10 × OTM steps  (ATM 0.40-0.60, OTM1 0.30-0.50)
//  Out of band → one strike toward the band is tried; still out, or no
//  greeks at all → the entry is refused. Nothing is guessed.
// ════════════════════════════════════════════════════════════════════

const deltaChainTTL = 60 * time.Second

// greeksSource loads the NIFTY option chain with greeks. Satisfied by
// *orderexec.StrikePicker (needs a SmartConnect session).
type greeksSource interface {
	LoadOptionChain(entryTime time.Time) ([]orderexec.OptionContract, error)
}

type deltaState struct {
	greeks  greeksSource // test hook; nil = the strike picker
	chainMu sync.Mutex
	chain   []orderexec.OptionContract
	chainAt time.Time
}

// deltaBand is the accepted |delta| range for a strike otm steps out of the money.
func deltaBand(otm int) (lo, hi float64) {
	return 0.40 - 0.10*float64(otm), 0.60 - 0.10*float64(otm)
}

func (svc *Service) greeksFor() (greeksSource, error) {
	if svc.greeks != nil {
		return svc.greeks, nil
	}
	if svc.strikePicker != nil {
		return svc.strikePicker, nil
	}
	if r, ok := svc.resolverForLegs().(greeksSource); ok {
		return r, nil
	}
	return nil, fmt.Errorf("no option chain source")
}

// optionChain returns the chain, cached for deltaChainTTL (OptionGreek is
// one broker call per expiry).
func (svc *Service) optionChain(now time.Time) ([]orderexec.OptionContract, error) {
	return svc.optionChainFresh(now, deltaChainTTL)
}

// optionChainFresh is optionChain with an explicit TTL. It is the single
// loader shared by the old delta-guard path (via optionChain) and the
// picker's chainAdapter (with its own 15s TTL) so the two paths do not each
// call LoadOptionChain and double the broker calls: whichever path loads
// first refreshes chain/chainAt, and the other reuses it as long as its own
// TTL still calls the cached load fresh.
func (svc *Service) optionChainFresh(now time.Time, ttl time.Duration) ([]orderexec.OptionContract, error) {
	svc.chainMu.Lock()
	defer svc.chainMu.Unlock()
	if svc.chain != nil && now.Sub(svc.chainAt) < ttl && !now.Before(svc.chainAt) {
		return svc.chain, nil
	}
	src, err := svc.greeksFor()
	if err != nil {
		return nil, err
	}
	chain, err := src.LoadOptionChain(now)
	if err != nil {
		return nil, err
	}
	svc.chain, svc.chainAt = chain, now
	return chain, nil
}

// symbolExpiry reads "06OCT26" out of "NIFTY06OCT2624200CE".
func symbolExpiry(symbol string) (time.Time, bool) {
	s := strings.TrimPrefix(strings.ToUpper(symbol), "NIFTY")
	if len(s) < 7 {
		return time.Time{}, false
	}
	// Go month names are "Oct", not "OCT".
	d := s[:3] + strings.ToLower(s[3:5]) + s[5:7]
	t, err := time.ParseInLocation("02Jan06", d, time.FixedZone("IST", 5*3600+30*60))
	return t, err == nil
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.In(a.Location()).Date()
	return ay == by && am == bm && ad == bd
}

// findContract finds a strike/type/expiry in the chain.
func findContract(chain []orderexec.OptionContract, info orderexec.StrikeInfo, opt string) (orderexec.OptionContract, bool) {
	exp, haveExp := symbolExpiry(info.Symbol)
	for _, c := range chain {
		if c.Strike != info.Strike || string(c.OptionType) != opt {
			continue
		}
		if haveExp && !c.Expiry.IsZero() && !sameDay(exp, c.Expiry) {
			continue
		}
		return c, true
	}
	return orderexec.OptionContract{}, false
}

// contractDelta finds |delta| for a strike/type/expiry in the chain.
func contractDelta(chain []orderexec.OptionContract, info orderexec.StrikeInfo, opt string) (float64, bool) {
	c, ok := findContract(chain, info, opt)
	if !ok || c.Delta == 0 {
		return 0, false
	}
	return math.Abs(c.Delta), true
}

// normIV returns IV in percent; a feed reporting a fraction (0.15) is scaled.
func normIV(iv float64) float64 {
	if iv > 0 && iv < 1.5 {
		return iv * 100
	}
	return iv
}

// entryQualityCheck applies the chain-based filters to a bought option:
// liquidity, IV ceiling, and expected gain at target vs round-trip costs.
func (svc *Service) entryQualityCheck(sig *strategy.Signal, info orderexec.StrikeInfo, now time.Time) error {
	chain, err := svc.optionChain(now)
	if err != nil {
		return fmt.Errorf("quality check: no option chain (%v)", err)
	}
	c, ok := findContract(chain, info, optionTypeFor(sig.Side))
	if !ok {
		return fmt.Errorf("quality check: %s not in option chain", info.Symbol)
	}
	if min := svc.cfg.RangeMinLiquidity; min > 0 && c.LiquidityScore < float64(min) {
		return fmt.Errorf("%s liquidity %.0f below %d (volume/OI)", info.Symbol, c.LiquidityScore, min)
	}
	if max := svc.cfg.RangeMaxBuyIV; max > 0 {
		if iv := normIV(c.IV); iv > max {
			return fmt.Errorf("%s IV %.1f%% above %.1f%% — premium too expensive to buy", info.Symbol, iv, max)
		}
	}
	if mult := svc.cfg.RangeCostMultiple; mult > 0 && sig.TargetMove > 0 && c.Delta != 0 {
		roundTrip := 2 * svc.orderExecutor.PaperSlippage(svc.orderExecutor.GetLTP(info.Token))
		// Expected premium gain ≈ |delta| × index move, in integer paise.
		deltaMilli := int64(math.Round(math.Abs(c.Delta) * 1000))
		gain := sig.TargetMove * deltaMilli / 1000
		if roundTrip > 0 && gain < mult*roundTrip {
			return fmt.Errorf("%s expected gain %d paise < %d× round-trip cost %d", info.Symbol, gain, mult, roundTrip)
		}
	}
	return nil
}

// basketQualityCheck applies liquidity to every leg and an IV floor to the
// short legs (a condor sells premium; too little IV means too little credit).
func (svc *Service) basketQualityCheck(legs []strategy.Signal, now time.Time) error {
	chain, err := svc.optionChain(now)
	if err != nil {
		return fmt.Errorf("quality check: no option chain (%v)", err)
	}
	var ivSum float64
	var ivN int
	for _, l := range legs {
		info := orderexec.StrikeInfo{Token: l.FNOToken, Symbol: l.FNOSymbol, Strike: l.Strike}
		c, ok := findContract(chain, info, optionTypeFor(l.Side))
		if !ok {
			return fmt.Errorf("quality check: leg %s %s not in option chain", l.Leg, l.FNOSymbol)
		}
		if min := svc.cfg.RangeMinLiquidity; min > 0 && c.LiquidityScore < float64(min) {
			return fmt.Errorf("leg %s liquidity %.0f below %d (volume/OI)", l.Leg, c.LiquidityScore, min)
		}
		if iv := normIV(c.IV); l.Short && iv > 0 {
			ivSum += iv
			ivN++
		}
	}
	if min := svc.cfg.RangeMinSellIV; min > 0 && ivN > 0 && ivSum/float64(ivN) < min {
		return fmt.Errorf("short-leg IV %.1f%% below %.1f%% — too little premium to sell", ivSum/float64(ivN), min)
	}
	return nil
}

// deltaChecked returns the contract to buy after the delta guard: info
// itself, or one strike toward the band. Errors mean refuse the entry.
func (svc *Service) deltaChecked(sig *strategy.Signal, info orderexec.StrikeInfo, now time.Time) (orderexec.StrikeInfo, error) {
	chain, err := svc.optionChain(now)
	if err != nil {
		return info, fmt.Errorf("delta guard: no greeks (%v)", err)
	}
	opt := optionTypeFor(sig.Side)
	spot := svc.orderExecutor.GetLTP(sig.Token)
	if spot <= 0 {
		spot = parseSignalClose(sig.Reason)
	}
	if spot <= 0 {
		return info, fmt.Errorf("delta guard: no spot price")
	}
	atm := nearestStrike(spot, ladderStrikeStep)
	// dir: +1 moves a strike further OTM, −1 toward ATM/ITM.
	dir := int64(1)
	if sig.Side == strategy.SidePut {
		dir = -1
	}
	steps := func(strike int64) int {
		n := int((strike - atm) * dir / ladderStrikeStep)
		if n < 0 {
			return 0
		}
		return n
	}

	check := func(c orderexec.StrikeInfo) (float64, bool, error) {
		d, ok := contractDelta(chain, c, opt)
		if !ok {
			return 0, false, fmt.Errorf("delta guard: %s not in option chain", c.Symbol)
		}
		lo, hi := deltaBand(steps(c.Strike))
		return d, d >= lo && d <= hi, nil
	}

	d, ok, err := check(info)
	if err != nil {
		return info, err
	}
	if ok {
		return info, nil
	}
	// One strike toward the band: too far OTM (delta low) → toward ATM,
	// too deep (delta high) → further OTM.
	lo, _ := deltaBand(steps(info.Strike))
	next := info.Strike + dir*ladderStrikeStep
	if d < lo {
		next = info.Strike - dir*ladderStrikeStep
	}
	alt, err := svc.resolverForLegs().ResolveStrike(now, next, opt)
	if err != nil {
		return info, fmt.Errorf("delta guard: %d%s delta %.2f out of band, %d%s unavailable: %v", info.Strike, opt, d, next, opt, err)
	}
	d2, ok2, err := check(alt)
	if err != nil {
		return info, err
	}
	if !ok2 {
		return info, fmt.Errorf("delta guard: %d%s delta %.2f and %d%s delta %.2f both out of band", info.Strike, opt, d, next, opt, d2)
	}
	log.Printf("[stratengine] Δ guard: %d%s delta %.2f out of band → %d%s delta %.2f", info.Strike, opt, d, next, opt, d2)
	return alt, nil
}
