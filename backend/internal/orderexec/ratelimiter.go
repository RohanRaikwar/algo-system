package orderexec

import (
	"fmt"
	"sync"
	"time"
)

// RateLimiter implements a sliding-window rate limiter for order placement.
// It tracks timestamps of recent orders and rejects new ones that exceed
// the configured maximum within the time window.
type RateLimiter struct {
	mu        sync.Mutex
	maxOrders int
	window    time.Duration
	orders    []time.Time
}

// NewRateLimiter creates a new rate limiter.
// maxOrders: maximum number of orders allowed within the window (e.g., 10)
// window: time window for rate limiting (e.g., 60 seconds)
func NewRateLimiter(maxOrders int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		maxOrders: maxOrders,
		window:    window,
		orders:    make([]time.Time, 0, maxOrders),
	}
}

// Allow checks if a new order is allowed and records it.
// Returns nil if allowed, ErrRateLimited if the rate limit is exceeded.
func (rl *RateLimiter) Allow() error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Prune expired entries
	valid := rl.orders[:0]
	for _, t := range rl.orders {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	rl.orders = valid

	if len(rl.orders) >= rl.maxOrders {
		return ErrRateLimited
	}

	rl.orders = append(rl.orders, now)
	return nil
}

// Count returns the number of orders in the current window.
func (rl *RateLimiter) Count() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	count := 0
	for _, t := range rl.orders {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// ErrRateLimited is returned when the rate limit is exceeded.
var ErrRateLimited = fmt.Errorf("order rate limit exceeded")
