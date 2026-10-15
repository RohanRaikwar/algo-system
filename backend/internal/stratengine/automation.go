// Option automation (STRAT_OPTION_AUTOMATION with DynamicStrikes): picks
// expiry and strike per BUY from the cached greeks chain, and the helpers
// that turn a signal's context into an instruments.AutomationInput.

package stratengine

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"
	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/strategy"
)

// applyOptionAutomation replaces the ATM picker's contract for this side
// with the automation pick. Any error keeps the current contract.
func (svc *Service) applyOptionAutomation(ctx context.Context, sig *strategy.Signal, now time.Time) {
	if !svc.cfg.OptionAutomation || !svc.cfg.DynamicStrikes || sig == nil || sig.Action != strategy.ActionBuy {
		return
	}
	if svc.srExtraFor(sig.StrategyName) != nil {
		return // the NIFTY strike picker; other underlyings resolve in resolveEntryStrike
	}
	if sig.Side != strategy.SideCall && sig.Side != strategy.SidePut {
		return
	}
	sp := svc.strikePicker.Load()
	if sp == nil {
		log.Printf("[stratengine] option automation skipped: strike picker not ready for %s", sig.Side)
		return
	}

	spotPrice := svc.orderExecutor.GetLTP(sig.Token)
	if spotPrice <= 0 {
		spotPrice = parseSignalClose(sig.Reason)
	}
	if spotPrice <= 0 {
		log.Printf("[stratengine] option automation skipped: no spot price for %s", sig.StrategyName)
		return
	}

	// Signal path: the cached chain and the instrument master only, never a
	// broker call. Without them the static/ATM tokens stay (as on any
	// automation error).
	chain, err := svc.entryChain(now)
	if err != nil {
		log.Printf("[stratengine] option automation fallback: %v", err)
		return
	}
	input := svc.buildAutomationInput(*sig, now, spotPrice)
	input.OptionChain = chain
	input.Offline = true
	pick, err := sp.ResolveForEntry(input)
	if err != nil {
		log.Printf("[stratengine] option automation fallback: %v", err)
		return
	}

	var previousToken string
	switch sig.Side {
	case strategy.SideCall:
		previousToken = sp.GetCallToken().Token
	case strategy.SidePut:
		previousToken = sp.GetPutToken().Token
	}

	sp.ApplyPick(pick)
	svc.orderExecutor.SetStrikePicker(sp)
	svc.syncStrategyFNOTokens(sp.GetCallToken().Token, sp.GetPutToken().Token)

	log.Printf("[stratengine] 🤖 option automation selected %s expiry=%s strike=%d symbol=%s token=%s fallback=%v reason=%s",
		sig.Side,
		pick.SelectedExpiry.In(time.FixedZone("IST", 5*3600+30*60)).Format("02Jan2006"),
		pick.Strike,
		pick.Symbol,
		pick.Token,
		pick.UsedFallback,
		pick.Reason,
	)

	if pick.Token != "" && pick.Token != previousToken {
		svc.publishFNOSubscription(ctx, pick.Token)
	}
	svc.publishStrikeInfo(ctx, spotPrice)
}

func (svc *Service) buildAutomationInput(sig strategy.Signal, now time.Time, spotPrice int64) instruments.AutomationInput {
	marketState := instruments.MarketStateSideways
	trendStrength := instruments.StrengthLow
	momentumStrength := instruments.StrengthLow

	if ctx, ok := svc.strategyAutomationContext(sig); ok {
		marketState = automationMarketStateFromStrategy(ctx.MarketState)
		trendStrength = automationStrengthFromStrategy(ctx.TrendStrength)
		momentumStrength = automationStrengthFromStrategy(ctx.MomentumStrength)
	} else {
		pct := parseSignalDistancePct(sig.Reason)
		if strings.Contains(strings.ToLower(sig.Reason), "momentum") && pct >= 0.12 {
			marketState = instruments.MarketStateTrending
		}
		strength := automationStrengthFromPct(pct)
		trendStrength = strength
		momentumStrength = strength
	}

	return instruments.AutomationInput{
		SignalType:       automationSignalType(sig.Side),
		IsExpiryDay:      isNIFTYExpiryDay(now),
		EntryTime:        now,
		MarketState:      marketState,
		TrendStrength:    trendStrength,
		MomentumStrength: momentumStrength,
		HoldType:         automationHoldType(svc.cfg.OptionHoldType),
		ExpectedIVMove:   automationIVMove(svc.cfg.ExpectedIVMove),
		SpotPrice:        spotPrice,
	}
}

func automationSignalType(side strategy.PositionSide) instruments.AutomationSignalType {
	switch side {
	case strategy.SideCall:
		return instruments.SignalBuyCall
	case strategy.SidePut:
		return instruments.SignalBuyPut
	default:
		return ""
	}
}

func (svc *Service) strategyAutomationContext(sig strategy.Signal) (strategy.EntryAutomationContext, bool) {
	// No registered strategy exposes its own automation context; callers
	// fall back to reason parsing.
	return strategy.EntryAutomationContext{}, false
}

func automationMarketStateFromStrategy(state strategy.EntryMarketState) instruments.AutomationMarketState {
	switch state {
	case strategy.EntryMarketStateTrending:
		return instruments.MarketStateTrending
	case strategy.EntryMarketStateChoppy:
		return instruments.MarketStateChoppy
	default:
		return instruments.MarketStateSideways
	}
}

func automationStrengthFromStrategy(strength strategy.EntryStrength) instruments.AutomationStrength {
	switch strength {
	case strategy.EntryStrengthHigh:
		return instruments.StrengthHigh
	case strategy.EntryStrengthMedium:
		return instruments.StrengthMedium
	default:
		return instruments.StrengthLow
	}
}

func automationStrengthFromPct(pct float64) instruments.AutomationStrength {
	switch {
	case pct >= 0.20:
		return instruments.StrengthHigh
	case pct >= 0.12:
		return instruments.StrengthMedium
	default:
		return instruments.StrengthLow
	}
}

func automationHoldType(v string) instruments.AutomationHoldType {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "carry":
		return instruments.HoldTypeCarry
	default:
		return instruments.HoldTypeIntraday
	}
}

func normalizeMarketStateValue(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "trending":
		return "trending"
	case "choppy":
		return "choppy"
	case "range":
		return "range"
	case "sideways":
		return "sideways"
	default:
		return ""
	}
}

func inferMarketStateFromReason(reason string) string {
	hay := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(hay, "range "):
		return "range"
	case strings.Contains(hay, "choppy"):
		return "choppy"
	case strings.Contains(hay, "sideways"):
		return "sideways"
	case strings.Contains(hay, "momentum"):
		return "trending"
	default:
		return ""
	}
}

func automationIVMove(v string) instruments.AutomationIVMove {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "rise":
		return instruments.IVMoveRise
	case "fall":
		return instruments.IVMoveFall
	default:
		return instruments.IVMoveNeutral
	}
}

func parseSignalDistancePct(reason string) float64 {
	marker := " is "
	start := strings.Index(reason, marker)
	if start < 0 {
		return 0
	}
	rest := reason[start+len(marker):]
	end := strings.Index(rest, "%")
	if end <= 0 {
		return 0
	}
	pct, err := strconv.ParseFloat(strings.TrimSpace(rest[:end]), 64)
	if err != nil {
		return 0
	}
	return pct
}

func parseSignalClose(reason string) int64 {
	marker := "close="
	start := strings.Index(reason, marker)
	if start < 0 {
		return 0
	}
	rest := reason[start+len(marker):]
	end := len(rest)
	for i, r := range rest {
		if r < '0' || r > '9' {
			end = i
			break
		}
	}
	if end <= 0 {
		return 0
	}
	price, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return 0
	}
	return price
}

func isNIFTYExpiryDay(now time.Time) bool {
	now = now.In(time.FixedZone("IST", 5*3600+30*60))
	if now.Weekday() != time.Tuesday {
		return false
	}
	return now.Hour() < 15 || (now.Hour() == 15 && now.Minute() < 30)
}
