package mdengine

import (
	"strings"
	"sync"
	"testing"
	"time"

	"trading-systemv1/pkg/smartconnect"
)

type fakeFeed struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeFeed) record(op string, tokens []smartconnect.TokenListEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range tokens {
		for _, t := range e.Tokens {
			f.calls = append(f.calls, op+":"+leaseKey(e.ExchangeType, t))
		}
	}
}
func (f *fakeFeed) SubscribeTokens(_ int, t []smartconnect.TokenListEntry) error {
	f.record("sub", t)
	return nil
}
func (f *fakeFeed) UnsubscribeTokens(_ int, t []smartconnect.TokenListEntry) error {
	f.record("unsub", t)
	return nil
}
func (f *fakeFeed) got() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, ",")
}

func TestOptionLease_SubscribeRenewExpire(t *testing.T) {
	l := newOptionLeases(time.Minute)
	f := &fakeFeed{}
	l.setFeed(f)
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	l.renew(2, "45123", t0)
	l.renew(2, "45123", t0.Add(20*time.Second)) // renew: no second subscribe
	l.sweep(t0.Add(70 * time.Second))           // 50 s after last renew: kept
	if got := f.got(); got != "sub:2:45123" {
		t.Fatalf("calls %q", got)
	}
	l.sweep(t0.Add(81 * time.Second)) // 61 s after last renew: dropped
	if got := f.got(); got != "sub:2:45123,unsub:2:45123" {
		t.Fatalf("calls %q", got)
	}
	l.sweep(t0.Add(200 * time.Second))
	if got := f.got(); got != "sub:2:45123,unsub:2:45123" {
		t.Fatalf("extra calls %q", got)
	}
}

// A token stratengine subscribed is never unsubscribed by a lease.
func TestOptionLease_PinnedNeverUnsubscribed(t *testing.T) {
	l := newOptionLeases(time.Minute)
	f := &fakeFeed{}
	l.setFeed(f)
	l.pin(dynSub{Mode: smartconnect.ModeQuote, Tokens: []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"53408"}}}})
	t0 := time.Now()
	l.renew(2, "53408", t0)
	l.sweep(t0.Add(2 * time.Minute))
	if got := f.got(); got != "" {
		t.Fatalf("pinned token touched: %q", got)
	}
	// Pinned after the lease opened: still never unsubscribed.
	l.renew(4, "1163095", t0)
	l.pin(dynSub{Tokens: []smartconnect.TokenListEntry{{ExchangeType: 4, Tokens: []string{"1163095"}}}})
	l.sweep(t0.Add(2 * time.Minute))
	if got := f.got(); got != "sub:4:1163095" {
		t.Fatalf("calls %q", got)
	}
}

// Leases opened with no session are subscribed once a feed is installed,
// and resubscribed on a new feed after a re-login.
func TestOptionLease_NoFeedThenFeed(t *testing.T) {
	l := newOptionLeases(time.Minute)
	t0 := time.Now()
	l.renew(2, "45123", t0)
	f := &fakeFeed{}
	l.setFeed(f)
	l.sweep(t0.Add(10 * time.Second))
	f2 := &fakeFeed{}
	l.setFeed(f2)
	l.sweep(t0.Add(20 * time.Second))
	if f.got() != "sub:2:45123" || f2.got() != "sub:2:45123" {
		t.Fatalf("calls %q / %q", f.got(), f2.got())
	}
	l.setFeed(nil)
	l.sweep(t0.Add(5 * time.Minute)) // expired with no feed: dropped, nothing sent
	if len(l.leases) != 0 {
		t.Fatal("lease kept")
	}
}

func TestParseLeaseCommand(t *testing.T) {
	if ex, tok, ok := parseLeaseCommand(`{"exchange_type":2,"token":"45123"}`); !ok || ex != 2 || tok != "45123" {
		t.Fatal("valid lease refused")
	}
	for _, bad := range []string{`{"exchange_type":1,"token":"99926000"}`, `{"exchange_type":2,"token":"x1"}`, `{"exchange_type":4}`, `nope`} {
		if _, _, ok := parseLeaseCommand(bad); ok {
			t.Errorf("accepted %s", bad)
		}
	}
}
