package backtest

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/strategy"
)

// ════════════════════════════════════════════════════════════════════
//  Strike-choice comparison (option model only)
//
//  Replays the same trades — same entry and exit times, same index
//  prices — and prices each one at three strike choices:
//    signal  : the strike the strategy asked for (ATM / OTM by index price)
//    delta   : the live option picker, |delta| closest to the band middle
//    return  : the live option picker, highest expected return on premium
//  The picker sees a modeled chain: ATM ± 8 strikes of the trade's expiry,
//  Black-Scholes at India VIX (flat IV, no smile), bid/ask = model price ∓
//  paper slippage. A trade the picker refuses is counted as refused, not
//  traded. Exits are the base run's, so a variant's own premium stop is
//  not simulated: run with -premium-sl 0 for a like-for-like comparison.
// ════════════════════════════════════════════════════════════════════

// StrikeVariant is one strike choice's result over the same trades.
type StrikeVariant struct {
	Name      string
	Trades    int   // trades priced (not refused)
	Refused   int   // picker found no contract passing its rules
	Wins      int   // trades with P&L > 0
	PnLPaise  int64 // per unit of quantity
	AvgStrike float64
	// Picker variants only: the signal strike's P&L on the same priced
	// trades (separates better strikes from skipped trades), the signal
	// strike's P&L on the refused trades, and why they were refused.
	SignalPnLOnPriced  int64
	SignalPnLOnRefused int64
	RefusedBy          map[string]int
}

// WinRate is the share of priced trades with P&L > 0 (0..1).
func (v StrikeVariant) WinRate() float64 {
	if v.Trades == 0 {
		return 0
	}
	return float64(v.Wins) / float64(v.Trades)
}

const compareStrikes = 8 // ATM ± strikes in the modeled chain

// CompareStrikes prices trades at the signal strike and at the picker's
// delta and return picks. It needs the option model enabled.
func (e *Engine) CompareStrikes(trades []Trade) ([]StrikeVariant, error) {
	m := e.cfg.Option
	if !m.Enabled {
		return nil, fmt.Errorf("strike comparison needs -option-model")
	}
	signal := StrikeVariant{Name: "signal strike (index price)"}
	delta := StrikeVariant{Name: "picker: delta"}
	ret := StrikeVariant{Name: "picker: expected return"}
	var sumSig, sumDelta, sumRet float64
	for i := range trades {
		t := &trades[i]
		if t.Strike == 0 || t.ExitTime.IsZero() || t.SameDay {
			continue
		}
		pnl := e.strikePnL(t, t.Strike)
		signal.add(pnl)
		sumSig += float64(t.Strike)

		for _, v := range []struct {
			variant *StrikeVariant
			sum     *float64
			rank    string
		}{{&delta, &sumDelta, optionpicker.RankDelta}, {&ret, &sumRet, optionpicker.RankReturn}} {
			strike, rej, ok := e.pickStrike(t, v.rank)
			if !ok {
				v.variant.Refused++
				v.variant.SignalPnLOnRefused += pnl
				if v.variant.RefusedBy == nil {
					v.variant.RefusedBy = map[string]int{}
				}
				v.variant.RefusedBy[rej]++
				continue
			}
			v.variant.SignalPnLOnPriced += pnl
			v.variant.add(e.strikePnL(t, strike))
			*v.sum += float64(strike)
		}
	}
	for _, v := range []struct {
		variant *StrikeVariant
		sum     float64
	}{{&signal, sumSig}, {&delta, sumDelta}, {&ret, sumRet}} {
		if v.variant.Trades > 0 {
			v.variant.AvgStrike = v.sum / float64(v.variant.Trades)
		}
	}
	return []StrikeVariant{signal, delta, ret}, nil
}

func (v *StrikeVariant) add(pnl int64) {
	v.Trades++
	v.PnLPaise += pnl
	if pnl > 0 {
		v.Wins++
	}
}

// strikePnL is the modeled round trip at strike: buy at the modeled price
// plus slippage at entry, sell at the modeled price minus slippage at exit.
func (e *Engine) strikePnL(t *Trade, strike int64) int64 {
	m := e.cfg.Option
	in := m.slip(true, m.premiumTo(t.Side, strike, t.EntryPrice, t.EntryTime, e.vixAt(t.EntryTime), e.expiryFor(t, t.EntryTime)))
	out := m.slip(false, m.premiumTo(t.Side, strike, t.ExitPrice, t.ExitTime, e.vixAt(t.ExitTime), e.expiryFor(t, t.ExitTime)))
	return out - in
}

// pickStrike runs the live option picker on a modeled chain at entry.
// On refusal it returns the rule that dropped the most candidates.
func (e *Engine) pickStrike(t *Trade, rank string) (int64, string, bool) {
	m := e.cfg.Option
	opt := "CE"
	if t.Side == strategy.SidePut {
		opt = "PE"
	}
	iv := e.vixAt(t.EntryTime)
	if iv <= 0 {
		iv = m.IVPct
	}
	expiry := e.expiryFor(t, t.EntryTime)
	atm := m.atmStrike(t.EntryPrice)
	step := m.StrikeStep
	if step <= 0 {
		step = 50
	}
	chain := make([]optionpicker.Contract, 0, 2*compareStrikes+1)
	quotes := make(map[string]optionpicker.Quote, 2*compareStrikes+1)
	for i := -compareStrikes; i <= compareStrikes; i++ {
		k := atm + int64(i)*step
		tok := strconv.FormatInt(k, 10) + opt
		chain = append(chain, optionpicker.Contract{Token: tok, Symbol: tok, Strike: k, Option: opt, Expiry: expiry, IV: iv, Liquidity: math.MaxFloat64})
		mid := m.premiumTo(t.Side, k, t.EntryPrice, t.EntryTime, iv, expiry)
		quotes[tok] = optionpicker.Quote{LTP: mid, Bid: m.slip(false, mid), Ask: m.slip(true, mid), At: t.EntryTime}
	}
	lo, hi, maxTheta, maxGamma := e.pickBand(t, atm, step)
	in := optionpicker.SingleIntent{Strategy: e.cfg.StrategyType, Option: opt, DeltaMin: lo, DeltaMax: hi, MinDTE: 1,
		TargetMove: t.TargetMove, MaxThetaPct: maxTheta, MaxGamma: maxGamma}
	rules := optionpicker.Rules{MaxSpreadPct: 2, MaxQuoteAge: time.Minute, MaxChainAge: time.Minute, GammaDTE: 1,
		MaxBuyIV: 25, CostMultiple: 3, RatePct: m.RatePct, Rank: rank, HoldMinutes: 60}
	env := optionpicker.Env{Spot: t.EntryPrice, Now: t.EntryTime, ChainAt: t.EntryTime,
		Quote: func(tok string) (optionpicker.Quote, bool) { q, ok := quotes[tok]; return q, ok }}
	p, rej, err := optionpicker.SelectSingle(chain, in, rules, env)
	if err != nil {
		// Out-of-band strikes always fail "delta"; the cause is the rule that
		// dropped the in-band ones, so prefer any other rule.
		top, n := "", 0
		for k, c := range rej {
			if k == "delta" {
				continue
			}
			if c > n || (c == n && k < top) {
				top, n = k, c
			}
		}
		if top == "" {
			top = "delta"
		}
		return 0, top, false
	}
	return p.Strike, "", true
}

// pickBand is the live intent's delta band and caps: NIFTY50_SR's
// configured band with its theta/gamma caps; the range strategy's band for
// the signal's OTM steps (ATM 0.40–0.60, 1 OTM 0.30–0.50), no caps.
func (e *Engine) pickBand(t *Trade, atm, step int64) (lo, hi, maxTheta, maxGamma float64) {
	if e.cfg.StrategyType == "nifty50_sr" {
		return 0.45, 0.60, 8, 0.005
	}
	d := t.Strike - atm
	if t.Side == strategy.SidePut {
		d = -d
	}
	otm := 0
	if d > 0 {
		otm = int(d / step)
	}
	return 0.40 - 0.10*float64(otm), 0.60 - 0.10*float64(otm), 0, 0
}
