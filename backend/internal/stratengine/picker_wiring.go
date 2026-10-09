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
//    shadow — the picker decides every SR entry and the
//             decision is recorded, but the entry uses the old path.
//    on     — the picker's contract is bought; a picker refusal refuses
//             the entry (never substituted). The picker refuses only when
//             no contract is tradable; otherwise it picks the best one and
//             lists any rules it broke (Waived).
//  Exits never go through the picker.
// ════════════════════════════════════════════════════════════════════

func (svc *Service) pickerConfig() optionpicker.Config {
	return optionpicker.Config{
		Rules: optionpicker.Rules{
			MaxSpreadPct: svc.cfg.PickMaxSpreadPct, MaxQuoteAge: svc.cfg.PickMaxQuoteAge, MaxChainAge: svc.cfg.PickMaxChainAge,
			GammaDTE:     svc.cfg.SRGammaDTE,
			MaxBuyIV:     svc.cfg.RangeMaxBuyIV,
			MinLiquidity: float64(svc.cfg.RangeMinLiquidity), CostMultiple: svc.cfg.RangeCostMultiple, RatePct: 6.5,
			Rank: svc.cfg.PickRank, HoldMinutes: svc.cfg.PickHoldMinutes,
		},
		LadderStrikes: 8, StrikeStep: ladderStrikeStep,
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

// newPicker builds the picker; nil when the mode is off, or when SR is
// disabled (no strategy would ever consume a decision, so running the
// picker's background refresh would only add broker load).
func (svc *Service) newPicker() *optionpicker.Picker {
	if !svc.pickerEnabled() || svc.orderExecutor == nil {
		return nil
	}
	if !svc.cfg.SREnabled {
		return nil
	}
	sp := orderexec.NewStrikePicker(svc.orderExecutor.GetSmartConnect())
	log.Printf("[stratengine] 🎯 option picker mode=%s", svc.cfg.PickerMode)
	return optionpicker.New(svc.pickerConfig(), chainAdapter{svc}, resolverAdapter{sp}, subscriberAdapter{svc})
}

// srPickerIntent is what the picker buys for an SR entry on opt; the live
// view uses SRViewTargetMove as the target.
func (svc *Service) srPickerIntent(opt string, target int64) optionpicker.SingleIntent {
	name := "NIFTY50_SR"
	if svc.srStrategy != nil {
		name = svc.srStrategy.Name()
	}
	return optionpicker.SingleIntent{Strategy: name, Option: opt,
		DeltaMin: svc.cfg.SRDeltaMin, DeltaMax: svc.cfg.SRDeltaMax, MinDTE: svc.cfg.SRMinDTE, TargetMove: target,
		ThetaMaxGainPct: svc.cfg.SRThetaMaxGainPct, MaxGamma: svc.cfg.SRMaxGamma}
}

// intentFor maps a single-leg entry to what the picker should buy.
func (svc *Service) intentFor(sig strategy.Signal) (optionpicker.SingleIntent, bool) {
	if sig.Action != strategy.ActionBuy || sig.Leg != "" || len(sig.Legs) > 0 {
		return optionpicker.SingleIntent{}, false
	}
	opt := optionTypeFor(sig.Side)
	switch {
	case svc.isSRSignal(&sig):
		return svc.srPickerIntent(opt, sig.TargetMove), true
	}
	return optionpicker.SingleIntent{}, false
}

type pickerDecision struct {
	Strategy string   `json:"strategy"`
	Mode     string   `json:"mode"`
	Result   string   `json:"result"` // picked / refused
	Reason   string   `json:"reason,omitempty"`
	Strike   int64    `json:"strike,omitempty"`
	Symbol   string   `json:"symbol,omitempty"`
	Token    string   `json:"token,omitempty"`
	Delta    float64  `json:"delta,omitempty"`
	IV       float64  `json:"iv,omitempty"`
	Score    float64  `json:"score,omitempty"`  // expected return on premium for the target move (0.25 = 25 %)
	Waived   []string `json:"waived,omitempty"` // rules the pick breaks (nothing passed them all)
	Bid      int64    `json:"bid,omitempty"`
	Ask      int64    `json:"ask,omitempty"`

	// What the old (non-picker) path chose for the same entry, recorded by
	// recordOldChoice after it runs — shadow mode never substitutes it, this
	// is purely for comparison. Agree reports whether the old choice
	// matches the picker's pick (false when the picker refused).
	OldSymbol string `json:"old_symbol,omitempty"`
	Agree     bool   `json:"agree,omitempty"`

	TS string `json:"ts"`
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
	log.Printf("[stratengine] 🎯 picker %s %s: %s %s %s waived=%v", d.Mode, d.Strategy, d.Result, d.Symbol, d.Reason, d.Waived)
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
	d.Score = math.Round(p.Score*1000) / 1000
	d.Waived = p.Waived
	return d
}

// recordOldChoice attaches what the old (non-picker) path chose to the most
// recently recorded picker decision for name, so a shadow/on-mode review
// can see agree/disagree without the old path itself changing at all.
// No-op when there's no decision to attach to (e.g. PickerMode "off",
// where the picker never runs).
func (svc *Service) recordOldChoice(strategyName, symbol string) {
	svc.pickerDec.mu.Lock()
	defer svc.pickerDec.mu.Unlock()
	d, ok := svc.pickerDec.last[strategyName]
	if !ok || symbol == "" {
		return
	}
	d.OldSymbol = symbol
	d.Agree = d.Result == "picked" && d.Symbol == symbol
	svc.pickerDec.last[strategyName] = d
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
	svc.noteEntryGreeks(sig.StrategyName, p.Delta, p.Theta)
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

// pickRank is the effective rank name ("" means delta).
func pickRank(r string) string {
	if r == "" {
		return optionpicker.RankDelta
	}
	return r
}
