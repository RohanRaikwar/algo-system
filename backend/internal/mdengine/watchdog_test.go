package mdengine

import (
	"testing"
	"time"

	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
	"trading-systemv1/pkg/smartconnect"
)

type fakeWatchdogClock struct {
	now      time.Time
	lastTick time.Time
	open     bool
}

func newTestWatchdog(c *fakeWatchdogClock) *feedWatchdog {
	return &feedWatchdog{
		threshold:  staleFeedThreshold,
		now:        func() time.Time { return c.now },
		marketOpen: func(time.Time) bool { return c.open },
		lastTick:   func() time.Time { return c.lastTick },
	}
}

func TestFeedWatchdog_FiresOnStaleFeedDuringMarketHours(t *testing.T) {
	c := &fakeWatchdogClock{now: time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC), open: true}
	w := newTestWatchdog(c)
	c.lastTick = c.now

	if ev := w.check(); ev != watchdogNone {
		t.Fatalf("fresh feed: event %v", ev)
	}
	c.now = c.now.Add(staleFeedThreshold - time.Second)
	if ev := w.check(); ev != watchdogNone {
		t.Fatalf("before threshold: event %v", ev)
	}
	c.now = c.now.Add(time.Second)
	if ev := w.check(); ev != watchdogFired {
		t.Fatalf("at threshold: event %v, want fired", ev)
	}
	// Re-armed: no alert storm while the reconnect is in progress.
	c.now = c.now.Add(time.Second)
	if ev := w.check(); ev != watchdogNone {
		t.Fatalf("right after firing: event %v", ev)
	}
	c.now = c.now.Add(staleFeedThreshold)
	if ev := w.check(); ev != watchdogFired {
		t.Fatalf("still stale after another threshold: event %v, want fired", ev)
	}
	c.lastTick = c.now.Add(time.Second)
	c.now = c.now.Add(2 * time.Second)
	if ev := w.check(); ev != watchdogRecovered {
		t.Fatalf("tick after firing: event %v, want recovered", ev)
	}
}

func TestFeedWatchdog_QuietOutsideMarketHoursAndGraceAtOpen(t *testing.T) {
	c := &fakeWatchdogClock{now: time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)}
	w := newTestWatchdog(c)
	for i := 0; i < 5; i++ { // no ticks, market closed
		c.now = c.now.Add(staleFeedThreshold)
		if ev := w.check(); ev != watchdogNone {
			t.Fatalf("closed market: event %v", ev)
		}
	}
	// Market opens with no tick seen yet: the threshold counts from the open.
	c.open = true
	if ev := w.check(); ev != watchdogNone {
		t.Fatalf("at open: event %v", ev)
	}
	c.now = c.now.Add(staleFeedThreshold)
	if ev := w.check(); ev != watchdogFired {
		t.Fatalf("no tick for threshold after open: event %v, want fired", ev)
	}
}

func TestOnStaleFeed_AlertsCountsAndForcesReconnect(t *testing.T) {
	s, tc := newTestService(Config{})
	s.health = metrics.NewHealthStatus()
	reasons := 0
	s.setFeedReconnect(func(string) { reasons++ })

	s.onStaleFeed(time.Now(), 45*time.Second)

	if tc.feedStale.n != 1 {
		t.Fatalf("stale alerts counted = %d, want 1", tc.feedStale.n)
	}
	if reasons != 1 {
		t.Fatalf("forced reconnects = %d, want 1", reasons)
	}
	if !s.health.FeedStale {
		t.Fatal("health not marked stale")
	}

	s.setFeedReconnect(nil) // no live session: alert only, no panic
	s.onStaleFeed(time.Now(), time.Minute)
}

// Frames/pongs still arriving but no ticks (market-wide halt, unlisted
// holiday, illiquid-only subscriptions): alert once, then rate-limited, and
// never force a re-login.
func TestOnStaleFeed_SocketAliveAlertsOnlyRateLimited(t *testing.T) {
	s, _ := newTestService(Config{})
	s.health = metrics.NewHealthStatus()
	now := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
	relogins := 0
	s.setFeedReconnect(func(string) { relogins++ })
	s.setFeedLastFrame(func() time.Time { return now.Add(-5 * time.Second) }) // pong 5s ago

	alerts := 0
	for i := 0; i < 20; i++ { // 20 firings, 30s apart = 10 minutes
		if s.onStaleFeed(now, staleFeedThreshold) {
			alerts++
		}
		now = now.Add(staleFeedThreshold)
	}
	if relogins != 0 {
		t.Fatalf("forced re-logins = %d with a live socket, want 0", relogins)
	}
	if alerts != 1 {
		t.Fatalf("alerts in 10 min = %d, want 1 (rate-limited)", alerts)
	}
	if !s.onStaleFeed(now, staleFeedThreshold) {
		t.Fatal("no repeat alert after the rate-limit interval")
	}
}

// A dead socket (no frame, not even a pong, for the threshold) forces a
// reconnect/re-login.
func TestOnStaleFeed_DeadSocketForcesRelogin(t *testing.T) {
	s, _ := newTestService(Config{})
	s.health = metrics.NewHealthStatus()
	now := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
	relogins := 0
	s.setFeedReconnect(func(string) { relogins++ })
	s.setFeedLastFrame(func() time.Time { return now.Add(-2 * staleFeedThreshold) })

	s.onStaleFeed(now, staleFeedThreshold)
	if relogins != 1 {
		t.Fatalf("forced re-logins = %d, want 1", relogins)
	}
}

// After maxForcedRelogins consecutive forced re-logins that brought no tick
// back, stop forcing (alert only) until ticks recover.
func TestOnStaleFeed_CapsConsecutiveForcedRelogins(t *testing.T) {
	s, _ := newTestService(Config{})
	s.health = metrics.NewHealthStatus()
	now := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
	relogins := 0
	s.setFeedReconnect(func(string) { relogins++ })
	s.setFeedLastFrame(func() time.Time { return time.Time{} }) // never a frame

	for i := 0; i < maxForcedRelogins+3; i++ {
		s.onStaleFeed(now, staleFeedThreshold)
		now = now.Add(staleFeedThreshold)
	}
	if relogins != maxForcedRelogins {
		t.Fatalf("forced re-logins = %d, want cap %d", relogins, maxForcedRelogins)
	}

	s.resetStaleFeed() // ticks came back
	s.onStaleFeed(now, staleFeedThreshold)
	if relogins != maxForcedRelogins+1 {
		t.Fatalf("forced re-logins after recovery = %d, want %d", relogins, maxForcedRelogins+1)
	}
}

func TestRecordIngest_UpdatesLastTickTime(t *testing.T) {
	s, _ := newTestService(Config{})
	ts := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	tick := model.Tick{Token: "A", TickTS: ts}
	s.recordIngest(&tick)
	if got := s.lastTickTime(); !got.Equal(ts) {
		t.Fatalf("lastTickTime = %v, want %v", got, ts)
	}
}

func TestFeedSubscribeMode_IsQuoteForVolume(t *testing.T) {
	if feedSubscribeMode != smartconnect.ModeQuote {
		t.Fatalf("feed must subscribe in Quote mode (volume); got %d", feedSubscribeMode)
	}
}
