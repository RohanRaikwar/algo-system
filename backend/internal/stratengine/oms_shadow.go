package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"trading-systemv1/internal/oms"
	"trading-systemv1/internal/strategy"
)

// intentQueueDepth bounds intents waiting to be written to Redis.
const intentQueueDepth = 1024

// startOMSShadow starts publishing order intents for the OMS process when
// STRAT_OMS_SHADOW is on. Shadow only: stratengine still executes every
// order itself, and a dropped intent costs a comparison, not a trade.
// When the OMS takes over order placement this path must become lossless.
func (svc *Service) startOMSShadow(ctx context.Context) {
	if !svc.cfg.OMSShadow || svc.redisWriter == nil {
		return
	}
	svc.intentCh = make(chan oms.Intent, intentQueueDepth)
	bus := oms.NewBus(svc.redisWriter.Client())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case in := <-svc.intentCh:
				pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				if err := bus.PublishIntent(pctx, in); err != nil {
					log.Printf("[stratengine] ⚠️  OMS shadow intent %s not published: %v", in.ID, err)
				}
				cancel()
			}
		}
	}()
	log.Printf("[stratengine] 👥 OMS shadow ON — publishing order intents to %s", oms.IntentStream)
}

// shadowIntent queues sig for the OMS process without ever blocking the
// order path. An entry carries the contract stratengine's executor would
// buy, so the OMS executor trades the same one.
func (svc *Service) shadowIntent(sig strategy.Signal) {
	if svc.intentCh == nil {
		return
	}
	if sig.Action == strategy.ActionBuy && sig.FNOToken == "" && svc.orderExecutor != nil {
		if tok, sym, err := svc.orderExecutor.ResolveEntryInstrument(sig); err == nil {
			sig.FNOToken, sig.FNOSymbol = tok, sym
		}
	}
	payload, err := json.Marshal(sig)
	if err != nil {
		return
	}
	in := oms.Intent{
		ID:        oms.IntentID(sig.StrategyName, string(sig.Side), string(sig.Action), sig.Token, sig.SignalTS),
		Strategy:  sig.StrategyName,
		Action:    string(sig.Action),
		Side:      string(sig.Side),
		Payload:   payload,
		CreatedTS: time.Now(),
	}
	select {
	case svc.intentCh <- in:
	default:
		n := svc.intentDropped.Add(1)
		log.Printf("[stratengine] ⚠️  OMS shadow queue full — intent %s %s %s dropped (total %d)", in.Strategy, in.Action, in.Side, n)
	}
}
