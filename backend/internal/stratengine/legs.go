package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// ════════════════════════════════════════════════════════════════════
//  Multi-leg (basket) signals — paper only.
//
//  A basket strategy (the iron condor) emits one signal carrying Legs.
//  Here it is resolved into per-leg executor signals (Leg/FNOToken/Short
//  set). The executor routes any signal with a Leg to its paper-only
//  leg path, so nothing in this file can move real money.
// ════════════════════════════════════════════════════════════════════

// legResolver resolves an option contract at a strike. Satisfied by
// *orderexec.StrikePicker.
type legResolver interface {
	ResolveStrike(now time.Time, strike int64, optionType string) (orderexec.StrikeInfo, error)
}

// basketStrategy is what the fan-out needs back from a basket strategy.
type basketStrategy interface {
	SetLegTokens(tokens map[string]string)
	CancelBasket(reason string)
	MinCredit() int64 // paise per unit; a basket collecting less is not opened
}

// defaultLegPriceWait bounds how long an entry waits for every leg's first
// tick after subscribing, so paper fills have a price.
const defaultLegPriceWait = 5 * time.Second

func (svc *Service) basketFor(name string) basketStrategy {
	if svc.testBasket != nil {
		return svc.testBasket
	}
	if svc.nifty50RangeICStrategy != nil && name == svc.nifty50RangeICStrategy.Name() {
		return svc.nifty50RangeICStrategy
	}
	return nil
}

func (svc *Service) resolverForLegs() legResolver {
	if svc.legResolver != nil {
		return svc.legResolver
	}
	if svc.strikePicker != nil {
		return svc.strikePicker
	}
	svc.legPickerOnce.Do(func() {
		// Offline instrument master first; SearchScrip only with a session.
		svc.legPicker = orderexec.NewStrikePicker(svc.orderExecutor.GetSmartConnect())
	})
	return svc.legPicker
}

// expandLegSignals turns a basket signal into one executor signal per leg.
// Entries resolve every strike (all or nothing) and put the long wings
// first; exits put the short legs first. Either order keeps the book
// hedged while legs are placed one by one.
func (svc *Service) expandLegSignals(sig strategy.Signal, now time.Time) ([]strategy.Signal, error) {
	legs := append([]strategy.LegSpec(nil), sig.Legs...)
	entry := sig.Action == strategy.ActionBuy
	if entry {
		res := svc.resolverForLegs()
		for i := range legs {
			info, err := res.ResolveStrike(now, legs[i].Strike, legs[i].OptionType)
			if err != nil {
				return nil, fmt.Errorf("leg %s %d%s: %w", legs[i].Leg, legs[i].Strike, legs[i].OptionType, err)
			}
			legs[i].Token, legs[i].Symbol = info.Token, info.Symbol
		}
	}
	sort.SliceStable(legs, func(i, j int) bool {
		if entry {
			return !legs[i].Short && legs[j].Short
		}
		return legs[i].Short && !legs[j].Short
	})

	out := make([]strategy.Signal, 0, len(legs))
	for _, l := range legs {
		ls := sig
		ls.Legs = nil
		ls.Side = l.Side()
		ls.Leg, ls.FNOToken, ls.FNOSymbol, ls.Strike, ls.Short = l.Leg, l.Token, l.Symbol, l.Strike, l.Short
		out = append(out, ls)
	}
	return out, nil
}

// handleBasketSignal gates, expands and dispatches a basket signal.
func (svc *Service) handleBasketSignal(ctx context.Context, sig strategy.Signal, now time.Time) {
	basket := svc.basketFor(sig.StrategyName)
	cancel := func(reason string) {
		log.Printf("[stratengine] BASKET %s %s blocked: %s", sig.StrategyName, sig.Action, reason)
		if basket != nil {
			basket.CancelBasket(reason)
		}
	}

	if sig.Action == strategy.ActionBuy {
		switch {
		case svc.killSwitchActive():
			cancel("kill switch")
			return
		case !markethours.IsMarketOpen(now):
			cancel("market closed")
			return
		case svc.pastEODCutoff(now):
			cancel("past EOD cutoff")
			return
		}
	}

	legs, err := svc.expandLegSignals(sig, now)
	if err != nil {
		cancel(err.Error())
		return
	}
	if sig.Action == strategy.ActionBuy && svc.cfg.RangeDeltaGuard {
		if err := svc.basketQualityCheck(legs, now); err != nil {
			cancel(err.Error())
			return
		}
	}

	if sig.Action != strategy.ActionBuy {
		// Exits are never delayed or blocked.
		for _, l := range legs {
			svc.removeLiveLeg(l)
			svc.dispatchOrder(ctx, l)
			svc.publishLegSignal(ctx, l, now)
		}
		return
	}

	tokens := make([]string, 0, len(legs))
	legTokens := make(map[string]string, len(legs))
	for _, l := range legs {
		tokens = append(tokens, l.FNOToken)
		legTokens[l.Leg] = svc.qualifyFNOToken(l.FNOToken)
	}
	if svc.redisWriter != nil {
		svc.publishFNOSubscription(ctx, tokens...)
	}
	if basket != nil {
		basket.SetLegTokens(legTokens)
	}
	go svc.dispatchLegsWhenPriced(ctx, legs, now)
}

// dispatchLegsWhenPriced waits for every leg's first tick, then dispatches
// the legs in order. If a leg never prices, no leg is opened.
func (svc *Service) dispatchLegsWhenPriced(ctx context.Context, legs []strategy.Signal, now time.Time) {
	wait := svc.legPriceWait
	if wait <= 0 {
		wait = defaultLegPriceWait
	}
	deadline := time.Now().Add(wait)
	for {
		priced := true
		for _, l := range legs {
			if svc.orderExecutor.GetLTP(l.FNOToken) <= 0 {
				priced = false
				break
			}
		}
		if priced {
			break
		}
		if time.Now().After(deadline) {
			if b := svc.basketFor(legs[0].StrategyName); b != nil {
				b.CancelBasket("no premium for every leg within " + wait.String())
			}
			log.Printf("[stratengine] BASKET %s: legs not priced within %s — nothing opened", legs[0].StrategyName, wait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	var credit int64
	for _, l := range legs {
		if p := svc.orderExecutor.GetLTP(l.FNOToken); l.Short {
			credit += p
		} else {
			credit -= p
		}
	}
	if b := svc.basketFor(legs[0].StrategyName); b != nil && credit < b.MinCredit() {
		reason := fmt.Sprintf("net credit %d paise below minimum %d", credit, b.MinCredit())
		b.CancelBasket(reason)
		log.Printf("[stratengine] BASKET %s: %s — nothing opened", legs[0].StrategyName, reason)
		return
	}
	for _, l := range legs {
		l.Price = svc.orderExecutor.GetLTP(l.FNOToken)
		svc.upsertLiveLeg(l, l.Price)
		svc.dispatchOrder(ctx, l)
		svc.publishLegSignal(ctx, l, now)
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	svc.publishLiveOrders(pctx)
	cancel()
}

// legRecord is how a leg appears in the journal and on pub:signal: under
// its own option contract (so the journal's per-instrument unique key keeps
// all four legs apart) with a "[SHORT_CE 24200]" tag the dashboard parses
// after a reload.
func (svc *Service) legRecord(sig strategy.Signal) strategy.Signal {
	rec := sig
	if sig.FNOToken != "" {
		rec.Token = sig.FNOToken
		rec.Exchange = svc.cfg.FNOExchange
		if rec.Exchange == "" {
			rec.Exchange = "NFO"
		}
	}
	rec.Reason = fmt.Sprintf("[%s %d] %s", sig.Leg, sig.Strike, sig.Reason)
	return rec
}

// publishLegSignal journals one leg and publishes it on pub:signal.
func (svc *Service) publishLegSignal(ctx context.Context, sig strategy.Signal, now time.Time) {
	rec := svc.legRecord(sig)
	if svc.journal != nil {
		if err := svc.journal.Record(rec, now, nil, false, false); err != nil {
			log.Printf("[stratengine] journal write error: %v", err)
		}
	}
	log.Printf("[stratengine] LEG SIGNAL: %s %s %s %d short=%v token=%s — %s",
		sig.Action, sig.StrategyName, sig.Leg, sig.Strike, sig.Short, sig.FNOToken, sig.Reason)
	if svc.redisWriter == nil {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"strategy_name":     rec.StrategyName,
		"action":            rec.Action,
		"side":              rec.Side,
		"market_state":      rec.MarketState,
		"token":             rec.Token,
		"exchange":          rec.Exchange,
		"qty":               rec.Qty,
		"price":             rec.Price,
		"current_fno_price": svc.orderExecutor.GetLTP(sig.FNOToken),
		"reason":            rec.Reason,
		"order_mode":        "PAPER",
		"leg":               sig.Leg,
		"strike":            sig.Strike,
		"short":             sig.Short,
		"fno_token":         sig.FNOToken,
		"fno_symbol":        sig.FNOSymbol,
		"ts":                now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := svc.redisWriter.Client().Publish(pctx, "pub:signal", string(payload)).Err(); err != nil {
		log.Printf("[stratengine] leg signal publish error: %v", err)
	}
}

func liveLegKey(strategyName string, side strategy.PositionSide, leg string) string {
	return liveOrderKey(strategyName, side) + "|" + leg
}

func (svc *Service) upsertLiveLeg(sig strategy.Signal, entryPrice int64) {
	svc.liveOrdersMu.Lock()
	defer svc.liveOrdersMu.Unlock()
	if svc.liveOrders == nil {
		svc.liveOrders = make(map[string]*liveOrderRuntime)
	}
	svc.liveOrders[liveLegKey(sig.StrategyName, sig.Side, sig.Leg)] = &liveOrderRuntime{
		StrategyName: sig.StrategyName,
		Side:         sig.Side,
		Token:        sig.FNOToken,
		EntryPrice:   entryPrice,
		BestPrice:    entryPrice,
		CurrentPrice: entryPrice,
		Leg:          sig.Leg,
		Strike:       sig.Strike,
		Short:        sig.Short,
	}
}

func (svc *Service) removeLiveLeg(sig strategy.Signal) {
	svc.liveOrdersMu.Lock()
	delete(svc.liveOrders, liveLegKey(sig.StrategyName, sig.Side, sig.Leg))
	svc.liveOrdersMu.Unlock()
}
