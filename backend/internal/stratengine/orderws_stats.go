package stratengine

import (
	"sync"
	"time"

	"trading-systemv1/internal/metrics"
)

// Order-status socket states shown on the Health page.
const (
	orderWSOff        = "off"        // STRAT_ORDER_WS=false
	orderWSPaper      = "paper"      // no live orders or session: never connects
	orderWSConnecting = "connecting" // started, first connect pending
	orderWSUp         = "up"
	orderWSDown       = "down"
)

// orderWSStats records the order-status socket's state for the metrics
// snapshot. Updated per push or state change only, never on the order path.
type orderWSStats struct {
	mu       sync.Mutex
	status   string
	since    time.Time
	ups      int
	pushes   int
	lastPush time.Time
	lastErr  string
}

func (s *orderWSStats) setStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLocked(status)
}

func (s *orderWSStats) setLocked(status string) {
	if s.status != status {
		s.status, s.since = status, time.Now()
	}
}

func (s *orderWSStats) onState(up bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if up {
		s.ups++
		s.setLocked(orderWSUp)
		return
	}
	s.setLocked(orderWSDown)
}

func (s *orderWSStats) onPush() {
	s.mu.Lock()
	s.pushes++
	s.lastPush = time.Now()
	s.mu.Unlock()
}

func (s *orderWSStats) onError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err.Error()
	// A failed dial never reports a state change, so leave "connecting"
	// for "down" here.
	if s.status == orderWSConnecting {
		s.setLocked(orderWSDown)
	}
}

func (s *orderWSStats) snapshot() *metrics.OrderWSSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := &metrics.OrderWSSnapshot{
		Status:     s.status,
		Reconnects: float64(max(s.ups-1, 0)),
		Pushes:     float64(s.pushes),
		LastError:  s.lastErr,
	}
	if snap.Status == "" {
		snap.Status = orderWSOff
	}
	if !s.since.IsZero() {
		snap.Since = s.since.UTC().Format(time.RFC3339)
	}
	if !s.lastPush.IsZero() {
		snap.LastPushAt = s.lastPush.UTC().Format(time.RFC3339)
	}
	return snap
}
