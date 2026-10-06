package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// exitRequestMaxAge drops exitwatch requests that sat in Redis or were
// replayed: a late exit for a position may hit the next one.
const exitRequestMaxAge = 30 * time.Second

// exitRequester closes a strategy's open position for an outside reason.
type exitRequester interface {
	ExitRequested(side strategy.PositionSide, fnoToken, reason string) *strategy.Signal
}

func (svc *Service) exitRequesterFor(name string) exitRequester {
	if svc.srStrategy != nil && name == svc.srStrategy.Name() {
		return svc.srStrategy
	}
	return nil
}

// exitRequestSignal turns an exitwatch request into the strategy's exit
// signal, or says why it is ignored. Only strategies listed in
// ExitWatchAuto are closed, never the real-order strategy.
func (svc *Service) exitRequestSignal(payload string, now time.Time) (*strategy.Signal, error) {
	var req model.ExitRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return nil, fmt.Errorf("bad payload: %w", err)
	}
	switch {
	case req.Strategy == model.RealOrderStrategy:
		return nil, fmt.Errorf("%s places real orders; exitwatch never closes it", req.Strategy)
	case !slices.Contains(svc.cfg.ExitWatchAuto, req.Strategy):
		return nil, fmt.Errorf("%s not in STRAT_EXITWATCH_AUTO", req.Strategy)
	case req.TS.IsZero() || now.Sub(req.TS) > exitRequestMaxAge:
		return nil, fmt.Errorf("stale request from %s", req.TS.Format(time.RFC3339))
	}
	r := svc.exitRequesterFor(req.Strategy)
	if r == nil {
		return nil, fmt.Errorf("%s not running", req.Strategy)
	}
	return svc.exitRequestVia(r, payload, now)
}

// exitRequestVia closes the position through r (payload already checked).
func (svc *Service) exitRequestVia(r exitRequester, payload string, now time.Time) (*strategy.Signal, error) {
	var req model.ExitRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return nil, fmt.Errorf("bad payload: %w", err)
	}
	sig := r.ExitRequested(strategy.PositionSide(req.Side), svc.unqualifyToken(req.FNOToken), req.Reason)
	if sig == nil {
		return nil, fmt.Errorf("no open %s %s position", req.Strategy, req.Side)
	}
	return sig, nil
}

// exitRequestLoop closes positions on exitwatch's requests through the
// normal exit path, resubscribing if the subscription drops.
func (svc *Service) exitRequestLoop(ctx context.Context) {
	client := svc.redisWriter.Client()
	backoff := time.Second
	for ctx.Err() == nil {
		sub := client.Subscribe(ctx, model.ExitRequestChannel)
		for msg := range sub.Channel() {
			backoff = time.Second
			sig, err := svc.exitRequestSignal(msg.Payload, time.Now())
			if err != nil {
				log.Printf("[stratengine] exitwatch exit request ignored: %v", err)
				continue
			}
			log.Printf("[stratengine] 🚪 exitwatch closing %s %s: %s", sig.StrategyName, sig.Side, sig.Reason)
			svc.tfEngine.SendSignal(*sig)
		}
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		log.Printf("[stratengine] ⚠️  %s subscription closed — resubscribing in %s", model.ExitRequestChannel, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
