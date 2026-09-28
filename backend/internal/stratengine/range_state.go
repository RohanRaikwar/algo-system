package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// Range strategy dashboard state. Same pattern as publishPnLSummary: the
// full view is written to a Redis key (REST / SNAPSHOT after a gateway
// restart) and published on a full-state channel for WS push.
const (
	rangeStateKey     = "range:state"
	rangeStateChannel = "pub:range"
	rangeStateEvery   = 5 * time.Second
	rangeIndexKey     = "NSE:99926000"
)

// rangeStateLoop publishes NIFTY50_RANGE's view whenever it changes.
func (svc *Service) rangeStateLoop(ctx context.Context) {
	t := time.NewTicker(rangeStateEvery)
	defer t.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		last = svc.publishRangeState(ctx, last)
	}
}

// publishRangeState publishes the view if it differs from last; it returns
// the JSON sent (or last, when nothing changed).
func (svc *Service) publishRangeState(ctx context.Context, last string) string {
	if svc.nifty50RangeStrategy == nil {
		return last
	}
	v, ok := svc.nifty50RangeStrategy.View(rangeIndexKey)
	if !ok {
		return last
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("[stratengine] range state marshal error: %v", err)
		return last
	}
	payload := string(b)
	if payload == last {
		return last
	}
	if svc.rangePublishHook != nil {
		svc.rangePublishHook(payload)
		return payload
	}
	if svc.redisWriter == nil {
		return last
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	svc.redisWriter.Client().Set(pctx, rangeStateKey, payload, 24*time.Hour)
	svc.redisWriter.Client().Publish(pctx, rangeStateChannel, payload)
	return payload
}
