package mdengine

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/model"
	"trading-systemv1/pkg/smartconnect"
)

// Option chart leases: a dashboard chart of one option contract keeps that
// contract streaming by renewing a lease (cmd:option_lease) every ~20 s.
// The contract is subscribed on the first lease and unsubscribed
// OPTION_LIVE_LINGER (default 60 s) after the last one. A token that
// stratengine subscribed (cmd:subscribe_token) is pinned: a lease never
// unsubscribes it. Option ticks build no candles (candleTokenSet), so a
// lease stores nothing.

const leaseSweepEvery = 10 * time.Second

// feedSubscriber is the live session's subscribe/unsubscribe pair; nil
// between sessions (leases are then resubscribed by the next sweep).
type feedSubscriber interface {
	SubscribeTokens(mode int, tokens []smartconnect.TokenListEntry) error
	UnsubscribeTokens(mode int, tokens []smartconnect.TokenListEntry) error
}

type optionLease struct {
	exchType int
	token    string
	until    time.Time
	live     bool // subscribed on the current feed
}

type optionLeases struct {
	mu     sync.Mutex
	linger time.Duration
	leases map[string]*optionLease
	pinned map[string]bool
	feed   feedSubscriber
}

func newOptionLeases(linger time.Duration) *optionLeases {
	if linger <= 0 {
		linger = time.Minute
	}
	return &optionLeases{linger: linger, leases: map[string]*optionLease{}, pinned: map[string]bool{}}
}

func leaseKey(exchType int, token string) string { return strconv.Itoa(exchType) + ":" + token }

// pin marks tokens stratengine subscribed; leases never drop them.
func (l *optionLeases) pin(sub dynSub) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range sub.Tokens {
		for _, t := range e.Tokens {
			l.pinned[leaseKey(e.ExchangeType, t)] = true
		}
	}
}

// setFeed installs (or, with nil, removes) the live session's feed. A new
// feed starts with no lease subscribed; the next renew or sweep sends them.
func (l *optionLeases) setFeed(f feedSubscriber) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.feed = f
	for _, ls := range l.leases {
		ls.live = false
	}
}

// renew extends (or opens) the lease of one contract and subscribes it if
// the feed does not carry it yet.
func (l *optionLeases) renew(exchType int, token string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := leaseKey(exchType, token)
	ls := l.leases[k]
	if ls == nil {
		ls = &optionLease{exchType: exchType, token: token}
		l.leases[k] = ls
		log.Printf("[mdengine] 📈 option lease opened %s", k)
	}
	ls.until = now.Add(l.linger)
	l.subscribeLocked(k, ls)
}

func (l *optionLeases) subscribeLocked(k string, ls *optionLease) {
	if ls.live || l.feed == nil {
		return
	}
	if l.pinned[k] {
		ls.live = true // stratengine already streams it
		return
	}
	entry := []smartconnect.TokenListEntry{{ExchangeType: ls.exchType, Tokens: []string{ls.token}}}
	if err := l.feed.SubscribeTokens(smartconnect.ModeQuote, entry); err != nil {
		log.Printf("[mdengine] ⚠️  option lease subscribe %s: %v", k, err)
		return
	}
	ls.live = true
}

// sweep drops expired leases (unsubscribing unpinned ones) and subscribes
// live leases the current feed does not carry yet.
func (l *optionLeases) sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, ls := range l.leases {
		if now.Before(ls.until) {
			l.subscribeLocked(k, ls)
			continue
		}
		delete(l.leases, k)
		if ls.live && !l.pinned[k] && l.feed != nil {
			entry := []smartconnect.TokenListEntry{{ExchangeType: ls.exchType, Tokens: []string{ls.token}}}
			if err := l.feed.UnsubscribeTokens(smartconnect.ModeQuote, entry); err != nil {
				log.Printf("[mdengine] ⚠️  option lease unsubscribe %s: %v", k, err)
			}
		}
		log.Printf("[mdengine] 📉 option lease closed %s", k)
	}
}

// parseLeaseCommand reads {"exchange_type":2,"token":"45123"}. Only option
// segments (NFO 2, BFO 4) are leased.
func parseLeaseCommand(payload string) (int, string, bool) {
	var cmd struct {
		ExchangeType int    `json:"exchange_type"`
		Token        string `json:"token"`
	}
	if err := json.Unmarshal([]byte(payload), &cmd); err != nil {
		return 0, "", false
	}
	tok := strings.TrimSpace(cmd.Token)
	if cmd.ExchangeType != 2 && cmd.ExchangeType != 4 || tok == "" {
		return 0, "", false
	}
	if _, err := strconv.ParseUint(tok, 10, 64); err != nil {
		return 0, "", false
	}
	return cmd.ExchangeType, tok, true
}

// optionLiveLinger is OPTION_LIVE_LINGER (default 60s).
func optionLiveLinger() time.Duration {
	if d, err := time.ParseDuration(config.GetEnv("OPTION_LIVE_LINGER", "60s")); err == nil && d > 0 {
		return d
	}
	return time.Minute
}

// runOptionLeases listens for leases and sweeps them until ctx ends.
func (s *Service) runOptionLeases(ctx context.Context) {
	if s.redisClient == nil {
		return
	}
	pubsub := s.redisClient.Subscribe(ctx, model.OptionLeaseChannel)
	defer pubsub.Close()
	ch := pubsub.Channel()
	t := time.NewTicker(leaseSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.leases.sweep(now)
		case msg, ok := <-ch:
			if !ok {
				return
			}
			ex, tok, ok := parseLeaseCommand(msg.Payload)
			if !ok {
				log.Printf("[mdengine] ⚠️  invalid option lease: %s", msg.Payload)
				continue
			}
			s.leases.renew(ex, tok, time.Now())
		}
	}
}
