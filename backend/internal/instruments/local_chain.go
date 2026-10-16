package instruments

import (
	"fmt"
	"time"

	"trading-systemv1/internal/optionmath"
)

// LocalOptionChain builds the option chain with greeks without OptionGreek,
// for indices Angel does not serve there (SENSEX answers AB9019 "No Data
// Available"). Contracts are the instrument master's ATM±strikesEachSide
// CE/PE on the same expiries LoadOptionChain asks for. IV is solved from
// each contract's live premium at spot (paise); greeks follow from it.
// quote returns a token's premium (paise) and open interest; a contract
// without a premium, or one no IV can price, is left out. No broker call.
func (sp *StrikePicker) LocalOptionChain(now time.Time, spot int64, strikesEachSide int, ratePct float64,
	quote func(token string) (premium, oi int64)) ([]OptionContract, error) {
	step := sp.strikeStep
	if spot <= 0 || step <= 0 {
		return nil, fmt.Errorf("local option chain: no spot or strike step")
	}
	s := float64(spot) / 100
	atm := (spot/100 + step/2) / step * step
	var chain []OptionContract
	for _, expiry := range sp.automationExpiries(now) {
		years := optionmath.YearsTo(expiry, now)
		for i := -strikesEachSide; i <= strikesEachSide; i++ {
			strike := atm + int64(i)*step
			for _, ot := range [...]OptionType{OptionTypeCall, OptionTypePut} {
				info, ok := sp.LookupOn(expiry, strike, string(ot))
				if !ok {
					continue
				}
				premium, oi := quote(info.Token)
				call := ot == OptionTypeCall
				p := float64(premium) / 100
				iv, ok := optionmath.ImpliedVol(p, s, float64(strike), years, ratePct/100, call)
				if !ok {
					continue
				}
				g := optionmath.GreeksAt(s, float64(strike), years, iv, ratePct/100, call)
				chain = append(chain, OptionContract{
					Token: info.Token, Symbol: info.Symbol, Strike: strike, Expiry: expiry, OptionType: ot,
					Delta: g.Delta, Gamma: g.Gamma, Theta: g.Theta, Vega: g.Vega,
					IV: iv * 100, Premium: p, LiquidityScore: float64(oi),
				})
			}
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("local option chain: no %s contract with a live premium", sp.prof().OptionName)
	}
	return chain, nil
}
