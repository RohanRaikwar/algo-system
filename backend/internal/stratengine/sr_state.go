package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"trading-systemv1/internal/strategy"
)

// NIFTY50_SR dashboard state: regime, levels, what it waits for and why the
// last entry bar was refused. Same pattern as publishPnLSummary: the full
// view is written to a Redis key (REST / SNAPSHOT after a gateway restart)
// and published on a full-state channel for WS push.
const (
	srStateKey     = "sr:state"
	srStateChannel = "pub:sr"
	srStateEvery   = 5 * time.Second
	srIndexKey     = "NSE:99926000"

	// Extra underlyings' SR views (SENSEX_SR, …), keyed by index key, as
	// one full-state map so NIFTY's pub:sr stays as it was.
	srxStateKey     = "srx:state"
	srxStateChannel = "pub:srx"
)

// srStateLoop publishes NIFTY50_SR's view whenever it changes.
func (svc *Service) srStateLoop(ctx context.Context) {
	t := time.NewTicker(srStateEvery)
	defer t.Stop()
	var last, lastX string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		last = svc.publishSRState(ctx, last)
		lastX = svc.publishSRXState(ctx, lastX)
	}
}

// publishSRXState publishes the extra underlyings' SR views as one map
// {"BSE:99919000": view} when it differs from last; it returns the JSON
// sent (or last).
func (svc *Service) publishSRXState(ctx context.Context, last string) string {
	if len(svc.srExtras) == 0 {
		return last
	}
	views := make(map[string]strategy.SRView, len(svc.srExtras))
	for _, ex := range svc.srExtras {
		if v, ok := ex.strat.View(ex.prof.IndexKey()); ok {
			views[ex.prof.IndexKey()] = v
		}
	}
	if len(views) == 0 {
		return last
	}
	b, err := json.Marshal(views)
	if err != nil {
		log.Printf("[stratengine] SRX state marshal error: %v", err)
		return last
	}
	payload := string(b)
	if payload == last {
		return last
	}
	if svc.srxPublishHook != nil {
		svc.srxPublishHook(payload)
		return payload
	}
	if svc.redisWriter == nil {
		return last
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	svc.redisWriter.Client().Set(pctx, srxStateKey, payload, 24*time.Hour)
	svc.redisWriter.Client().Publish(pctx, srxStateChannel, payload)
	return payload
}

// publishSRState publishes the view if it differs from last; it returns
// the JSON sent (or last, when nothing changed).
func (svc *Service) publishSRState(ctx context.Context, last string) string {
	if svc.srStrategy == nil {
		return last
	}
	v, ok := svc.srStrategy.View(srIndexKey)
	if !ok {
		return last
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("[stratengine] SR state marshal error: %v", err)
		return last
	}
	payload := string(b)
	if payload == last {
		return last
	}
	if svc.srPublishHook != nil {
		svc.srPublishHook(payload)
		return payload
	}
	if svc.redisWriter == nil {
		return last
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	svc.redisWriter.Client().Set(pctx, srStateKey, payload, 24*time.Hour)
	svc.redisWriter.Client().Publish(pctx, srStateChannel, payload)
	return payload
}
