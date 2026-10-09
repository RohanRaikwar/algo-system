package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// Open-position context for exitwatch: entry, target, stop and held option
// token of every open position. Internal only — pub:poscontext is not in the
// gateway's forward list. Published on change; an empty set means flat.
const (
	posContextKey     = "poscontext:latest"
	posContextChannel = "pub:poscontext"
	posContextEvery   = time.Second
)

// posContextLoop publishes the open-position set whenever it changes.
func (svc *Service) posContextLoop(ctx context.Context) {
	t := time.NewTicker(posContextEvery)
	defer t.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		last = svc.publishPosContext(ctx, last)
	}
}

// collectPosContexts gathers contexts from every strategy that reports them.
func (svc *Service) collectPosContexts() []model.PositionContext {
	var srcs []strategy.PositionContexter
	if svc.srStrategy != nil {
		srcs = append(srcs, svc.srStrategy)
	}
	out := []model.PositionContext{}
	for _, s := range srcs {
		out = append(out, s.PositionContexts()...)
	}
	return out
}

// publishPosContext publishes when the set differs from last and returns the
// positions JSON sent (or last when unchanged).
func (svc *Service) publishPosContext(ctx context.Context, last string) string {
	positions := svc.collectPosContexts()
	pb, err := json.Marshal(positions)
	if err != nil {
		log.Printf("[stratengine] poscontext marshal error: %v", err)
		return last
	}
	if string(pb) == last || svc.redisWriter == nil {
		return last
	}
	payload, err := json.Marshal(model.PositionContextSet{Positions: positions, TS: time.Now().UTC()})
	if err != nil {
		log.Printf("[stratengine] poscontext marshal error: %v", err)
		return last
	}
	c := svc.redisWriter.Client()
	if err := c.Set(ctx, posContextKey, payload, 24*time.Hour).Err(); err != nil {
		log.Printf("[stratengine] poscontext store error: %v", err)
		return last
	}
	if err := c.Publish(ctx, posContextChannel, string(payload)).Err(); err != nil {
		log.Printf("[stratengine] poscontext publish error: %v", err)
	}
	return string(pb)
}
