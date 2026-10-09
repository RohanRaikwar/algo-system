package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"time"
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
)

// srStateLoop publishes NIFTY50_SR's view whenever it changes.
func (svc *Service) srStateLoop(ctx context.Context) {
	t := time.NewTicker(srStateEvery)
	defer t.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		last = svc.publishSRState(ctx, last)
	}
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
