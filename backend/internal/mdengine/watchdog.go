package mdengine

import (
	"context"
	"log"
	"time"

	"trading-systemv1/internal/markethours"
)

// Stale-feed watchdog: a socket can stay "connected" while delivering nothing
// (half-open, or subscribed to nothing after a silent server-side reset).
const (
	// staleFeedThreshold: no tick for this long during market hours fires
	// the watchdog.
	staleFeedThreshold = 30 * time.Second
	// staleFeedCheckInterval is how often the watchdog runs (and how often
	// /healthz last_tick_time is refreshed).
	staleFeedCheckInterval = time.Second
	// staleAlertInterval rate-limits repeat alerts that do not force a
	// re-login (socket alive, or the forced re-login cap reached).
	staleAlertInterval = 10 * time.Minute
	// maxForcedRelogins caps consecutive forced re-logins without a tick.
	maxForcedRelogins = 3
)

type watchdogEvent int

const (
	watchdogNone watchdogEvent = iota
	watchdogFired
	watchdogRecovered
)

// feedWatchdog decides when the feed is stale. Pure logic with injectable
// time so it can be tested without waiting.
type feedWatchdog struct {
	threshold  time.Duration
	now        func() time.Time
	marketOpen func(time.Time) bool
	lastTick   func() time.Time

	armed   time.Time // staleness is measured from max(lastTick, armed)
	firedAt time.Time
	stale   bool
}

// check evaluates the feed once. It fires when no tick has arrived for
// threshold during market hours (counted from the open / watchdog start if no
// tick was seen since), then re-arms so the next alert needs another full
// threshold. It reports recovery on the first tick after firing.
func (w *feedWatchdog) check() watchdogEvent {
	now := w.now()
	last := w.lastTick()
	if w.stale && last.After(w.firedAt) {
		w.stale = false
		return watchdogRecovered
	}
	if !w.marketOpen(now) {
		w.armed = time.Time{} // re-arm at the next open
		return watchdogNone
	}
	if w.armed.IsZero() {
		w.armed = now
	}
	ref := last
	if ref.Before(w.armed) {
		ref = w.armed
	}
	if now.Sub(ref) < w.threshold {
		return watchdogNone
	}
	w.armed = now
	w.firedAt = now
	w.stale = true
	return watchdogFired
}

// runFeedWatchdog runs the stale-feed watchdog until ctx ends, and keeps the
// /healthz last_tick_time current.
func (s *Service) runFeedWatchdog(ctx context.Context) {
	w := &feedWatchdog{
		threshold:  staleFeedThreshold,
		now:        time.Now,
		marketOpen: markethours.IsMarketOpen,
		lastTick:   s.lastTickTime,
	}
	ticker := time.NewTicker(staleFeedCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		last := s.lastTickTime()
		if !last.IsZero() {
			s.health.SetLastTickTime(last)
		}
		switch w.check() {
		case watchdogFired:
			age := staleFeedThreshold
			if !last.IsZero() {
				age = time.Since(last)
			}
			s.onStaleFeed(time.Now(), age)
		case watchdogRecovered:
			s.resetStaleFeed()
			s.health.SetFeedStale(false)
			log.Printf("[mdengine] ✅ feed recovered — ticks flowing again")
		}
	}
}

// onStaleFeed handles a watchdog firing at now. It forces a reconnect and
// re-login only when the socket itself looks dead (no frame — data, ping or
// pong — for staleFeedThreshold). If frames still flow but no ticks do (a
// market-wide halt, an unlisted holiday, illiquid-only subscriptions) a new
// session would not help: it alerts (rate-limited) and keeps the session.
// After maxForcedRelogins consecutive forced re-logins that brought no tick
// back it stops forcing. Reports whether an alert was logged.
func (s *Service) onStaleFeed(now time.Time, age time.Duration) bool {
	s.ctr.feedStale.Inc()
	s.health.SetFeedStale(true)
	ageStr := age.Truncate(time.Second).String()

	s.feedMu.Lock()
	fn := s.feedReconnect
	alive, frameAge := false, time.Duration(0)
	if s.feedLastFrame != nil {
		if lf := s.feedLastFrame(); !lf.IsZero() {
			frameAge = now.Sub(lf)
			alive = frameAge < staleFeedThreshold
		}
	}
	force := fn != nil && !alive && s.staleForced < maxForcedRelogins
	if force {
		s.staleForced++
	}
	forced := s.staleForced
	alert := force || s.staleLastAlert.IsZero() || now.Sub(s.staleLastAlert) >= staleAlertInterval
	if alert {
		s.staleLastAlert = now
	}
	s.feedMu.Unlock()

	switch {
	case force:
		log.Printf("[mdengine] 🚨 STALE FEED: no tick for %v and socket silent — forcing reconnect/re-login (%d/%d)", ageStr, forced, maxForcedRelogins)
		fn("no tick for " + ageStr)
	case !alert:
	case alive:
		log.Printf("[mdengine] 🚨 STALE FEED: no tick for %v but socket alive (last frame %v ago) — NOT re-logging (halt / holiday / illiquid tokens?)", ageStr, frameAge.Truncate(time.Second))
	case fn != nil:
		log.Printf("[mdengine] 🚨 STALE FEED: no tick for %v after %d forced re-logins — no longer forcing, needs attention", ageStr, forced)
	default:
		log.Printf("[mdengine] 🚨 STALE FEED: no tick for %v during market hours (no live session)", ageStr)
	}
	return alert
}

// resetStaleFeed clears the forced re-login count and alert rate limit (ticks
// recovered, or the session closed).
func (s *Service) resetStaleFeed() {
	s.feedMu.Lock()
	s.staleForced = 0
	s.staleLastAlert = time.Time{}
	s.feedMu.Unlock()
}

// setFeedLastFrame installs the live socket's last-frame clock (nil when no
// session is running).
func (s *Service) setFeedLastFrame(fn func() time.Time) {
	s.feedMu.Lock()
	s.feedLastFrame = fn
	s.feedMu.Unlock()
}

// setFeedReconnect installs the live session's forced-reconnect hook (nil
// when no session is running).
func (s *Service) setFeedReconnect(fn func(reason string)) {
	s.feedMu.Lock()
	s.feedReconnect = fn
	s.feedMu.Unlock()
}

// lastTickTime is the local receive time of the newest ingested tick.
func (s *Service) lastTickTime() time.Time {
	n := s.lastTickNano.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}
