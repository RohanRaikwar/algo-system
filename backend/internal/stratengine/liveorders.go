// Live orders view: the open positions with their current premium and
// stoploss, rebuilt on option ticks and published on pub:orders (key
// orders:live) for the dashboard. Display state only; the executor owns
// the positions.

package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// liveOrdersView is the Service's live orders state.
type liveOrdersView struct {
	liveOrdersMu       sync.Mutex
	liveOrders         map[string]*liveOrderRuntime
	lastLiveOrdersJSON string
}

type liveOrderRuntime struct {
	StrategyName string
	Side         strategy.PositionSide
	Token        string
	EntryPrice   int64
	BestPrice    int64
	CurrentPrice int64
	Leg          string // multi-leg id; "" for single-leg positions
	Strike       int64
	Short        bool
}

type liveOrderPayload struct {
	StrategyName    string                `json:"strategy_name"`
	Side            strategy.PositionSide `json:"side"`
	FNOToken        string                `json:"fno_token,omitempty"`
	FNOSymbol       string                `json:"fno_symbol,omitempty"`
	EntryFNOPrice   int64                 `json:"entry_fno_price,omitempty"`
	CurrentFNOPrice int64                 `json:"current_fno_price,omitempty"`
	BestFNOPrice    int64                 `json:"best_fno_price,omitempty"`
	StoplossPrice   int64                 `json:"stoploss_price,omitempty"`
	StoplossKind    string                `json:"stoploss_kind,omitempty"`
	TargetPrice     int64                 `json:"target_price,omitempty"`
	IndexStop       int64                 `json:"index_stop,omitempty"`
	IndexTarget     int64                 `json:"index_target,omitempty"`
	Manual          bool                  `json:"manual,omitempty"` // index levels moved by hand
	Real            bool                  `json:"real,omitempty"`   // real orders: read-only on the dashboard
	Leg             string                `json:"leg,omitempty"`
	Strike          int64                 `json:"strike,omitempty"`
	Short           bool                  `json:"short,omitempty"`
}

func liveOrderKey(strategyName string, side strategy.PositionSide) string {
	return strategyName + "|" + string(side)
}

func (svc *Service) seedLiveOrdersFromStrategies() {
	for _, sr := range svc.srAll() {
		if pos := sr.CurrentFNOPosition(); pos != nil {
			svc.setLiveOrderFromPosition(sr.Name(), pos)
		}
	}
}

func (svc *Service) setLiveOrderFromPosition(strategyName string, pos *strategy.LiveFNOPosition) {
	if pos == nil || pos.Side == strategy.SideNone || pos.EntryPrice <= 0 {
		return
	}
	token := svc.unqualifyToken(pos.Token)
	svc.liveOrdersMu.Lock()
	svc.liveOrders[liveOrderKey(strategyName, pos.Side)] = &liveOrderRuntime{
		StrategyName: strategyName,
		Side:         pos.Side,
		Token:        token,
		EntryPrice:   pos.EntryPrice,
		BestPrice:    pos.BestPrice,
		CurrentPrice: svc.orderExecutor.GetLTP(token),
	}
	svc.liveOrdersMu.Unlock()
}

func (svc *Service) upsertLiveOrder(strategyName string, side strategy.PositionSide, token string, entryPrice int64) {
	if strategyName == "" || side == strategy.SideNone {
		return
	}
	token = svc.unqualifyToken(token)
	svc.liveOrdersMu.Lock()
	rt, ok := svc.liveOrders[liveOrderKey(strategyName, side)]
	if !ok {
		rt = &liveOrderRuntime{
			StrategyName: strategyName,
			Side:         side,
		}
		svc.liveOrders[liveOrderKey(strategyName, side)] = rt
	}
	rt.Token = token
	if entryPrice > 0 {
		rt.EntryPrice = entryPrice
		rt.BestPrice = entryPrice
		rt.CurrentPrice = entryPrice
	} else if token != "" {
		rt.CurrentPrice = svc.orderExecutor.GetLTP(token)
	}
	svc.liveOrdersMu.Unlock()
}

func (svc *Service) removeLiveOrder(strategyName string, side strategy.PositionSide) {
	if strategyName == "" || side == strategy.SideNone {
		return
	}
	svc.liveOrdersMu.Lock()
	delete(svc.liveOrders, liveOrderKey(strategyName, side))
	svc.liveOrdersMu.Unlock()
}

func (svc *Service) updateLiveOrdersFromTick(ctx context.Context, tick model.Tick) {
	if tick.Token == "" || tick.Price <= 0 {
		return
	}

	changed := false

	svc.liveOrdersMu.Lock()
	for _, rt := range svc.liveOrders {
		if rt == nil || rt.Token != tick.Token {
			continue
		}
		if rt.CurrentPrice != tick.Price {
			rt.CurrentPrice = tick.Price
			changed = true
		}
		switch {
		case rt.Short:
			// A sold leg profits as premium falls — track lowest as best.
			if rt.BestPrice == 0 || tick.Price < rt.BestPrice {
				rt.BestPrice = tick.Price
				changed = true
			}
		case rt.Side == strategy.SideCall, rt.Side == strategy.SidePut:
			// Both CALL and PUT are BUY orders — track highest as best
			if tick.Price > rt.BestPrice {
				rt.BestPrice = tick.Price
				changed = true
			}
		}
	}
	svc.liveOrdersMu.Unlock()

	if changed {
		svc.publishLiveOrders(ctx)
	}
}

func (svc *Service) stopConfig(strategyName string) (hardSL, trailSL, trailStart float64, ok bool) {
	switch {
	case svc.srByName(strategyName) != nil:
		cfg := svc.srByName(strategyName).Config()
		return float64(cfg.FNOHardSLPct), float64(cfg.FNOTrailSLPct), float64(cfg.FNOTrailStartPct), true
	default:
		return 0, 0, 0, false
	}
}

// computeLiveStoploss computes the SL price for a live FNO order.
// Both CALL and PUT are BUY orders: premium UP = profit, premium DOWN = loss.
func computeLiveStoploss(side strategy.PositionSide, entryPrice, bestPrice int64, hardSL, trailSL, trailStart float64) (int64, string) {
	if entryPrice <= 0 {
		return 0, ""
	}

	// Hard SL: exit if premium drops hardSL% below entry (same for CALL and PUT)
	var hardStop int64
	if hardSL > 0 {
		hardStop = int64(float64(entryPrice) * (1 - hardSL/100))
	}

	// Trail SL: exit if premium drops trailSL% below best (same for CALL and PUT)
	var trailStop int64
	trailActive := false
	if trailSL > 0 && bestPrice > 0 {
		entryF := float64(entryPrice)
		bestF := float64(bestPrice)
		profit := (bestF - entryF) / entryF * 100
		if profit >= trailStart {
			trailStop = int64(bestF * (1 - trailSL/100))
			trailActive = trailStop > 0
		}
	}

	// Return the tighter (higher) SL price
	if trailActive && (hardStop == 0 || trailStop > hardStop) {
		return trailStop, "TRAIL"
	}
	if hardStop > 0 {
		return hardStop, "HARD"
	}
	if trailActive {
		return trailStop, "TRAIL"
	}
	return 0, ""
}

func (svc *Service) snapshotLiveOrdersLocked() []liveOrderPayload {
	orders := make([]liveOrderPayload, 0, len(svc.liveOrders))
	for _, rt := range svc.liveOrders {
		if rt == nil || rt.Side == strategy.SideNone || rt.EntryPrice <= 0 {
			continue
		}
		payload := liveOrderPayload{
			StrategyName:    rt.StrategyName,
			Side:            rt.Side,
			FNOToken:        rt.Token,
			FNOSymbol:       svc.fnoSymbolFor(rt.Token),
			EntryFNOPrice:   rt.EntryPrice,
			CurrentFNOPrice: rt.CurrentPrice,
			BestFNOPrice:    rt.BestPrice,
			Leg:             rt.Leg,
			Strike:          rt.Strike,
			Short:           rt.Short,
		}
		if hardSL, trailSL, trailStart, ok := svc.stopConfig(rt.StrategyName); ok && rt.Leg == "" {
			payload.StoplossPrice, payload.StoplossKind = computeLiveStoploss(rt.Side, rt.EntryPrice, rt.BestPrice, hardSL, trailSL, trailStart)
		}
		svc.addPositionLevels(&payload)
		orders = append(orders, payload)
	}
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].StrategyName != orders[j].StrategyName {
			return orders[i].StrategyName < orders[j].StrategyName
		}
		if orders[i].Side != orders[j].Side {
			return orders[i].Side < orders[j].Side
		}
		return orders[i].Leg < orders[j].Leg
	})
	return orders
}

func (svc *Service) liveOrderPayload(strategyName string, side strategy.PositionSide) (liveOrderPayload, bool) {
	svc.liveOrdersMu.Lock()
	defer svc.liveOrdersMu.Unlock()

	rt, ok := svc.liveOrders[liveOrderKey(strategyName, side)]
	if !ok || rt == nil || rt.EntryPrice <= 0 {
		return liveOrderPayload{}, false
	}

	payload := liveOrderPayload{
		StrategyName:    rt.StrategyName,
		Side:            rt.Side,
		FNOToken:        rt.Token,
		EntryFNOPrice:   rt.EntryPrice,
		CurrentFNOPrice: rt.CurrentPrice,
		BestFNOPrice:    rt.BestPrice,
	}
	if hardSL, trailSL, trailStart, ok := svc.stopConfig(rt.StrategyName); ok {
		payload.StoplossPrice, payload.StoplossKind = computeLiveStoploss(rt.Side, rt.EntryPrice, rt.BestPrice, hardSL, trailSL, trailStart)
	}
	svc.addPositionLevels(&payload)
	return payload, true
}

// addPositionLevels adds the strategy's premium target, index stop/target
// and any hand-set premium stop to a single-leg order.
func (svc *Service) addPositionLevels(p *liveOrderPayload) {
	p.Real = svc.isRealStrategy(p.StrategyName)
	if strategy.IsTelegramStrategy(p.StrategyName) {
		svc.addTGLevels(p)
		return
	}
	sr := svc.srByName(p.StrategyName)
	if sr == nil || p.Leg != "" {
		return
	}
	pos := sr.CurrentFNOPosition()
	if pos == nil || pos.Side != p.Side {
		return
	}
	p.TargetPrice, p.IndexStop, p.IndexTarget, p.Manual = pos.Target, pos.IndexStop, pos.IndexTarget, pos.Manual
	if pos.ManualSL > 0 {
		p.StoplossPrice, p.StoplossKind = pos.ManualSL, "MANUAL"
	}
}

// addTGLevels adds a Telegram trade's premium stop and target. Its stop is
// the call's (raised by the ladder, the caller or by hand), not a %.
func (svc *Service) addTGLevels(p *liveOrderPayload) {
	tg := svc.tgByName(p.StrategyName)
	if tg == nil || p.Leg != "" {
		return
	}
	pos := tg.Position()
	if pos == nil || pos.Arm.Call.Side() != p.Side || pos.Arm.FNOToken != svc.unqualifyToken(p.FNOToken) {
		return
	}
	p.StoplossPrice, p.StoplossKind, p.TargetPrice = pos.SL, "TG", pos.Target
	if pos.ManualSL {
		p.StoplossKind = "MANUAL"
	}
	if pos.Best > p.BestFNOPrice {
		p.BestFNOPrice = pos.Best
	}
}

func (svc *Service) publishLiveOrders(ctx context.Context) {
	if svc.redisWriter == nil {
		return
	}

	svc.liveOrdersMu.Lock()
	orders := svc.snapshotLiveOrdersLocked()
	payload, err := json.Marshal(map[string]interface{}{
		"orders": orders,
	})
	if err != nil {
		svc.liveOrdersMu.Unlock()
		log.Printf("[stratengine] live orders marshal error: %v", err)
		return
	}
	payloadStr := string(payload)
	if payloadStr == svc.lastLiveOrdersJSON {
		svc.liveOrdersMu.Unlock()
		return
	}
	svc.lastLiveOrdersJSON = payloadStr
	svc.liveOrdersMu.Unlock()

	svc.redisWriter.Client().Set(ctx, model.OrdersKey, payload, 24*time.Hour)
	svc.redisWriter.Client().Publish(ctx, model.OrdersChannel, payloadStr)
}
