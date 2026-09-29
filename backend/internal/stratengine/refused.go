package stratengine

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/strategy"
)

// Refused entries: entry signals a strategy produced that stratengine
// blocked before any order (kill switch, market closed, EOD cutoff, strike
// not resolvable, no greeks, ...). Same pattern as publishRangeState: today's
// full list is written to a Redis key (SNAPSHOT fallback after a gateway
// restart) and published on a full-state channel, so the dashboard can mark
// them on the chart with the reason.
const (
	refusedStateKey     = "refused:state"
	refusedStateChannel = "pub:refused"
	refusedMax          = 100 // per IST day; oldest dropped first
)

// RefusedEntry is one blocked entry signal. Strike is in index points.
type RefusedEntry struct {
	Strategy       string `json:"strategy"`
	Side           string `json:"side"`
	Token          string `json:"token"`
	Exchange       string `json:"exchange"`
	Strike         int64  `json:"strike,omitempty"`
	Reason         string `json:"reason"`          // why it was refused
	StrategyReason string `json:"strategy_reason"` // why the strategy wanted to enter
	TS             string `json:"ts"`              // RFC3339 UTC
}

// refusedView is the pub:refused payload: today's refused entries, oldest first.
type refusedView struct {
	Date    string         `json:"date"` // IST YYYY-MM-DD
	Entries []RefusedEntry `json:"entries"`
}

type refusedLog struct {
	mu   sync.Mutex
	view refusedView
}

// add appends e (resetting on a new IST day) and returns the JSON view.
func (l *refusedLog) add(e RefusedEntry, now time.Time) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	day := now.In(markethours.IST).Format("2006-01-02")
	if l.view.Date != day {
		l.view = refusedView{Date: day}
	}
	l.view.Entries = append(l.view.Entries, e)
	if n := len(l.view.Entries); n > refusedMax {
		l.view.Entries = append([]RefusedEntry(nil), l.view.Entries[n-refusedMax:]...)
	}
	return json.Marshal(l.view)
}

// recordRefusedEntry logs a blocked entry and publishes today's list.
func (svc *Service) recordRefusedEntry(ctx context.Context, sig strategy.Signal, reason string, now time.Time) {
	b, err := svc.refused.add(RefusedEntry{
		Strategy:       sig.StrategyName,
		Side:           string(sig.Side),
		Token:          sig.Token,
		Exchange:       sig.Exchange,
		Strike:         sig.Strike,
		Reason:         reason,
		StrategyReason: sig.Reason,
		TS:             now.UTC().Format(time.RFC3339Nano),
	}, now)
	if err != nil {
		log.Printf("[stratengine] refused state marshal error: %v", err)
		return
	}
	payload := string(b)
	if svc.refusedPublishHook != nil {
		svc.refusedPublishHook(payload)
		return
	}
	if svc.redisWriter == nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	svc.redisWriter.Client().Set(pctx, refusedStateKey, payload, 24*time.Hour)
	svc.redisWriter.Client().Publish(pctx, refusedStateChannel, payload)
}
