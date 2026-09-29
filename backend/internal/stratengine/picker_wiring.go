package stratengine

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"
)

// ════════════════════════════════════════════════════════════════════
//  Global option picker wiring (internal/optionpicker).
//
//  STRAT_PICKER_MODE:
//    off    — no picker; entries choose contracts as before.
//    shadow — the picker decides every SR / RANGE / IC entry and the
//             decision is recorded, but the entry uses the old path.
//    on     — the picker's contract is bought; a picker refusal refuses
//             the entry (never substituted).
//  Exits never go through the picker.
// ════════════════════════════════════════════════════════════════════

func (svc *Service) pickerConfig() optionpicker.Config {
	strikes := 8
	if svc.cfg.RangeICEnabled {
		strikes = 12 // condor shorts sit past the range edges, wings 100 pts further
	}
	return optionpicker.Config{
		Rules: optionpicker.Rules{
			MaxSpreadPct: svc.cfg.PickMaxSpreadPct, MaxQuoteAge: svc.cfg.PickMaxQuoteAge, MaxChainAge: svc.cfg.PickMaxChainAge,
			MaxThetaPct: svc.cfg.SRMaxThetaPct, MaxGamma: svc.cfg.SRMaxGamma, GammaDTE: svc.cfg.SRGammaDTE,
			MaxBuyIV: svc.cfg.RangeMaxBuyIV, MinSellIV: svc.cfg.RangeMinSellIV,
			MinLiquidity: float64(svc.cfg.RangeMinLiquidity), CostMultiple: svc.cfg.RangeCostMultiple, RatePct: 6.5,
		},
		LadderStrikes: strikes, StrikeStep: ladderStrikeStep,
		ChainEvery: 15 * time.Second, ChainBackoff: 60 * time.Second, SearchEvery: time.Second,
	}
}

// chainAdapter loads Angel's option chain for the picker.
type chainAdapter struct{ svc *Service }

func (a chainAdapter) Load(now time.Time) ([]optionpicker.Contract, error) {
	// Shares svc's chain cache with the old delta-guard path (optionChain)
	// so the picker's 15s refresh and the old path's checks do not each hit
	// the broker: one LoadOptionChain call serves both.
	chain, err := a.svc.optionChainFresh(now, 15*time.Second)
	if err != nil {
		return nil, err
	}
	out := make([]optionpicker.Contract, 0, len(chain))
	for _, c := range chain {
		out = append(out, optionpicker.Contract{
			Strike: c.Strike, Option: string(c.OptionType), Expiry: c.Expiry,
			IV: normIV(c.IV), Liquidity: c.LiquidityScore,
		})
	}
	return out, nil
}

// resolverAdapter: offline instrument master for Lookup, broker for Search.
type resolverAdapter struct{ sp *orderexec.StrikePicker }

func (r resolverAdapter) Lookup(e time.Time, k int64, o string) (string, string, bool) {
	info, ok := r.sp.LookupOn(e, k, o)
	return info.Token, info.Symbol, ok
}

func (r resolverAdapter) Search(e time.Time, k int64, o string) (string, string, error) {
	info, err := r.sp.ResolveStrikeOn(e, k, o)
	return info.Token, info.Symbol, err
}

// subscriberAdapter subscribes option tokens in SnapQuote.
type subscriberAdapter struct{ svc *Service }

func (s subscriberAdapter) SubscribeOptions(tokens []string) {
	if s.svc.redisWriter != nil {
		s.svc.publishFNOSubscriptionMode(context.Background(), smartSnapQuote, tokens...)
	}
}

const smartSnapQuote = smartconnect.ModeSnapQuote

// pickerEnabled reports whether a picker is built at all ("" = off).
func (svc *Service) pickerEnabled() bool {
	return svc.cfg.PickerMode == "shadow" || svc.cfg.PickerMode == "on"
}

// newPicker builds the picker; nil when the mode is off, or when SR, RANGE
// and RANGE_IC are all disabled (no strategy would ever consume a decision,
// so running the picker's background refresh would only add broker load).
func (svc *Service) newPicker() *optionpicker.Picker {
	if !svc.pickerEnabled() || svc.orderExecutor == nil {
		return nil
	}
	if !svc.cfg.SREnabled && !svc.cfg.RangeEnabled && !svc.cfg.RangeICEnabled {
		return nil
	}
	sp := orderexec.NewStrikePicker(svc.orderExecutor.GetSmartConnect())
	log.Printf("[stratengine] 🎯 option picker mode=%s", svc.cfg.PickerMode)
	return optionpicker.New(svc.pickerConfig(), chainAdapter{svc}, resolverAdapter{sp}, subscriberAdapter{svc})
}

// intentFor maps a single-leg entry to what the picker should buy.
func (svc *Service) intentFor(sig strategy.Signal) (optionpicker.SingleIntent, bool) {
	if sig.Action != strategy.ActionBuy || sig.Leg != "" || len(sig.Legs) > 0 {
		return optionpicker.SingleIntent{}, false
	}
	opt := optionTypeFor(sig.Side)
	switch {
	case svc.isSRSignal(&sig):
		return optionpicker.SingleIntent{Strategy: sig.StrategyName, Option: opt,
			DeltaMin: svc.cfg.SRDeltaMin, DeltaMax: svc.cfg.SRDeltaMax, MinDTE: svc.cfg.SRMinDTE, TargetMove: sig.TargetMove}, true
	case svc.nifty50RangeStrategy != nil && sig.StrategyName == svc.nifty50RangeStrategy.Name():
		// The range strategy's strike rule (ATM, or OTM in a wide range)
		// sets the delta band, as in the delta guard.
		var spot int64
		if svc.orderExecutor != nil {
			spot = svc.orderExecutor.GetLTP(optionpicker.SpotToken)
		}
		if spot <= 0 {
			spot = parseSignalClose(sig.Reason)
		}
		otm := 0
		if spot > 0 && sig.Strike > 0 {
			atm := nearestStrike(spot, ladderStrikeStep)
			d := sig.Strike - atm
			if sig.Side == strategy.SidePut {
				d = -d
			}
			if d > 0 {
				otm = int(d / ladderStrikeStep)
			}
		}
		lo, hi := deltaBand(otm)
		return optionpicker.SingleIntent{Strategy: sig.StrategyName, Option: opt, DeltaMin: lo, DeltaMax: hi, MinDTE: 1, TargetMove: sig.TargetMove}, true
	}
	return optionpicker.SingleIntent{}, false
}

// condorIntentFor maps an iron-condor basket to the picker's condor intent.
func (svc *Service) condorIntentFor(sig strategy.Signal) (optionpicker.CondorIntent, bool) {
	var sCE, lCE, sPE, lPE int64
	for _, l := range sig.Legs {
		switch {
		case l.OptionType == "CE" && l.Short:
			sCE = l.Strike
		case l.OptionType == "CE":
			lCE = l.Strike
		case l.OptionType == "PE" && l.Short:
			sPE = l.Strike
		default:
			lPE = l.Strike
		}
	}
	if len(sig.Legs) != 4 || sCE == 0 || sPE == 0 || lCE <= sCE || lPE >= sPE {
		return optionpicker.CondorIntent{}, false
	}
	var minCreditPct int64
	if svc.nifty50RangeICStrategy != nil {
		minCreditPct = svc.nifty50RangeICStrategy.Config().MinCreditPct
	}
	return optionpicker.CondorIntent{Strategy: sig.StrategyName, ShortCEAtLeast: sCE, ShortPEAtMost: sPE,
		MaxShortDelta: 0.25, WingWidth: lCE - sCE, MinDTE: 1, MinCreditPct: minCreditPct}, true
}

type pickerDecision struct {
	Strategy string  `json:"strategy"`
	Mode     string  `json:"mode"`
	Result   string  `json:"result"` // picked / refused
	Reason   string  `json:"reason,omitempty"`
	Strike   int64   `json:"strike,omitempty"`
	Symbol   string  `json:"symbol,omitempty"`
	Token    string  `json:"token,omitempty"`
	Delta    float64 `json:"delta,omitempty"`
	IV       float64 `json:"iv,omitempty"`
	Bid      int64   `json:"bid,omitempty"`
	Ask      int64   `json:"ask,omitempty"`
	TS       string  `json:"ts"`
}

type pickerDecisions struct {
	mu   sync.Mutex
	last map[string]pickerDecision
}

func (svc *Service) recordPickerDecision(d pickerDecision) {
	svc.pickerDec.mu.Lock()
	if svc.pickerDec.last == nil {
		svc.pickerDec.last = map[string]pickerDecision{}
	}
	svc.pickerDec.last[d.Strategy] = d
	svc.pickerDec.mu.Unlock()
	log.Printf("[stratengine] 🎯 picker %s %s: %s %s %s", d.Mode, d.Strategy, d.Result, d.Symbol, d.Reason)
}

func (svc *Service) lastPickerDecision(name string) pickerDecision {
	svc.pickerDec.mu.Lock()
	defer svc.pickerDec.mu.Unlock()
	return svc.pickerDec.last[name]
}

func decisionFor(name, mode string, p optionpicker.Pick, err error, now time.Time) pickerDecision {
	d := pickerDecision{Strategy: name, Mode: mode, TS: now.UTC().Format(time.RFC3339)}
	if err != nil {
		d.Result, d.Reason = "refused", err.Error()
		return d
	}
	d.Result = "picked"
	d.Strike, d.Symbol, d.Token = p.Strike, p.Symbol, p.Token
	d.Delta, d.IV, d.Bid, d.Ask = math.Round(p.Delta*1000)/1000, p.IV, p.Quote.Bid, p.Quote.Ask
	return d
}

// pickEntry runs the picker on a single-leg entry. decided=false means the
// caller's old path chooses the contract (off, shadow, or no intent). In
// "on" mode decided=true: err != nil refuses the entry, else sig carries
// the picked contract.
func (svc *Service) pickEntry(sig *strategy.Signal, now time.Time) (bool, error) {
	if svc.picker == nil || !svc.pickerEnabled() {
		return false, nil
	}
	in, ok := svc.intentFor(*sig)
	if !ok {
		return false, nil
	}
	p, _, err := svc.picker.PickSingle(in, now)
	svc.recordPickerDecision(decisionFor(sig.StrategyName, svc.cfg.PickerMode, p, err, now))
	if svc.cfg.PickerMode != "on" {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("picker: %w", err)
	}
	sig.Strike, sig.FNOToken, sig.FNOSymbol = p.Strike, p.Token, p.Symbol
	return true, nil
}

// pickBasket is pickEntry for a condor basket; in "on" mode a success
// sets every leg's strike and token.
func (svc *Service) pickBasket(sig *strategy.Signal, now time.Time) (bool, error) {
	if svc.picker == nil || !svc.pickerEnabled() || sig.Action != strategy.ActionBuy {
		return false, nil
	}
	in, ok := svc.condorIntentFor(*sig)
	if !ok {
		return false, nil
	}
	cp, _, err := svc.picker.PickCondor(in, now)
	svc.recordPickerDecision(decisionFor(sig.StrategyName, svc.cfg.PickerMode, cp.ShortCE, err, now))
	if svc.cfg.PickerMode != "on" {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("picker: %w", err)
	}
	legs := map[bool]map[string]optionpicker.Pick{
		true:  {"CE": cp.ShortCE, "PE": cp.ShortPE},
		false: {"CE": cp.LongCE, "PE": cp.LongPE},
	}
	out := make([]strategy.LegSpec, len(sig.Legs)) // never write through to the strategy's slice
	for i, l := range sig.Legs {
		p := legs[l.Short][l.OptionType]
		l.Strike, l.Token, l.Symbol = p.Strike, p.Token, p.Symbol
		out[i] = l
	}
	sig.Legs = out
	return true, nil
}

// runPicker starts the picker's background refresh; ticks arrive via
// tickRouterLoop.
func (svc *Service) runPicker(ctx context.Context) {
	if svc.picker == nil {
		return
	}
	svc.orderExecutor.SetQuoteSource(svc.picker.Quotes())
	svc.picker.Run(ctx, markethours.IsMarketOpen)
}
