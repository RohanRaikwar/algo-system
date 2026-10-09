package stratengine

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// ════════════════════════════════════════════════════════════════════
//  Option-chain greeks for entry checks.
//
//  The NIFTY chain with greeks (OptionGreek) backs NIFTY50_SR's strike
//  pick and entryQualityCheck. No greeks → the check refuses the entry.
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
