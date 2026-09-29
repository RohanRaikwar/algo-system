package optionpicker

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

type ChainSource interface {
	Load(now time.Time) ([]Contract, error)
}

// ChainCache refreshes the option chain in the background. A failed load
// keeps the last good snapshot; selection refuses once it is older than
// Rules.MaxChainAge.
type ChainCache struct {
	src            ChainSource
	every, backoff time.Duration

	mu        sync.RWMutex
	contracts []Contract
	at        time.Time
	lastErr   error
}

func NewChainCache(src ChainSource, every, backoff time.Duration) *ChainCache {
	return &ChainCache{src: src, every: every, backoff: backoff}
}

func (c *ChainCache) Refresh(now time.Time) time.Duration {
	start := time.Now()
	out, err := c.src.Load(now)
	elapsed := time.Since(start).Truncate(time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastErr = err
		if strings.Contains(err.Error(), "exceeding access rate") {
			log.Printf("[optionpicker] option chain rate-limited after %s, backing off %s: %v", elapsed, c.backoff, err)
			return c.backoff
		}
		log.Printf("[optionpicker] option chain load failed after %s: %v", elapsed, err)
		return c.every
	}
	c.contracts, c.at, c.lastErr = out, now, nil
	log.Printf("[optionpicker] option chain: %d contracts in %s", len(out), elapsed)
	return c.every
}

// Snapshot returns the last good chain. Callers must not modify it.
func (c *ChainCache) Snapshot() ([]Contract, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contracts, c.at
}

func (c *ChainCache) LastError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

// Run refreshes until ctx ends; active reports whether to call the broker
// now (market hours).
func (c *ChainCache) Run(ctx context.Context, active func(time.Time) bool) {
	wait := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		now := time.Now()
		if !active(now) {
			wait = c.every
			continue
		}
		wait = c.Refresh(now)
	}
}
