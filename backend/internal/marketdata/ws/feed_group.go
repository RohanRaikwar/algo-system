package ws

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"trading-systemv1/internal/model"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

// MaxFeedConns is Angel's limit of concurrent sockets per client code; one
// more is rejected with 429.
const MaxFeedConns = 3

const (
	// connStallThreshold: an open socket with no frame (data, ping, pong) for
	// this long during market hours is redialled. Live, the feed carries
	// hundreds of frames a second plus a pong every 10s, so 5s of silence is a
	// stalled TCP path (loss burst, retransmit backoff), not a quiet market.
	connStallThreshold = 5 * time.Second
	// connRedialInterval rate-limits redials per socket so a dead path cannot
	// cause a redial storm; the 30s read deadline remains the outer net.
	connRedialInterval = 15 * time.Second
	// stallCheckInterval is how often sockets are checked for a stall.
	stallCheckInterval = 500 * time.Millisecond
	// slotRestartDelay is the first pause before rebuilding a socket that
	// gave up while the group runs on; doubles per failure up to
	// slotRestartMaxDelay.
	slotRestartDelay    = 5 * time.Second
	slotRestartMaxDelay = 60 * time.Second
)

// FeedGroup runs several Angel sockets with the same login and subscriptions,
// each pinned to a different server IP, and merges them into one tick stream:
// ticks come from a primary socket, and a primary that goes silent hands over
// to a live one within primaryFailover (see feedMerger). A loss burst or
// server-side stall on one TCP path then no longer stalls the feed, and the
// stalled socket is redialled.
//
// It has the same surface the session loop used on a single Ingest. A socket
// that gives up is rebuilt (same subscriptions) while the group runs, except
// after a 429 (connection limit: dropped for the session) or an auth
// rejection (the session is dead). Start returns ErrFeedLost once no socket is
// left, or on ForceReconnect; the session loop then re-logins.
type FeedGroup struct {
	cfg    IngestConfig
	merge  *feedMerger // nil for a single socket
	pinned bool        // dial each slot's own server IP
	url    string      // endpoint override for every socket, incl. rebuilt ones (tests)

	// Hooks with Ingest's meaning, called once per accepted tick. OnConnState
	// reports the group: true when the first socket is up, false when none is.
	OnReconnect func()
	OnTick      func(token string, price int64)
	OnIngested  func(tick *model.Tick)
	OnDrop      func()
	OnSeqGap    func(token string, missed int64)
	OnClockSkew func()
	OnConnState func(connected bool)

	// Group-only hooks (metrics).
	OnConnsUp   func(up int)                  // sockets currently open
	OnDuplicate func()                        // tick dropped: its socket is not the primary
	OnDelivered func(conn int)                // tick passed from socket conn
	OnFailover  func(from, to int)            // primary moved off a silent socket
	OnRedial    func(conn int, reason string) // stalled socket redialled

	marketOpen   func(time.Time) bool
	now          func() time.Time
	restartDelay time.Duration

	mu    sync.Mutex
	conns []*Ingest // slot -> current socket (replaced on rebuild)
	up    []bool
	nUp   int
	fatal chan error
}

// NewGroup builds n sockets (clamped to 1..MaxFeedConns) for cfg. marketOpen
// gates the stall redial (outside market hours a silent socket is normal).
func NewGroup(cfg IngestConfig, n int, marketOpen func(time.Time) bool) (*FeedGroup, error) {
	if n < 1 {
		n = 1
	}
	if n > MaxFeedConns {
		n = MaxFeedConns
	}
	g := &FeedGroup{
		cfg:          cfg,
		pinned:       n > 1,
		marketOpen:   marketOpen,
		now:          time.Now,
		restartDelay: slotRestartDelay,
		up:           make([]bool, n),
		fatal:        make(chan error, 1),
	}
	if n > 1 {
		g.merge = newFeedMerger(func(i int) time.Time { return g.slots()[i].LastFrameTime() })
	}
	for i := 0; i < n; i++ {
		ing, err := g.newSlotIngest(i, cfg)
		if err != nil {
			return nil, err
		}
		g.conns = append(g.conns, ing)
	}
	return g, nil
}

// newSlotIngest builds slot i's socket for cfg, with the group hooks wired.
func (g *FeedGroup) newSlotIngest(i int, cfg IngestConfig) (*Ingest, error) {
	var dialer *websocket.Dialer
	if g.pinned {
		dialer = smartconnect.NewPinnedDialer(i)
	}
	ing, err := newIngest(cfg, dialer)
	if err != nil {
		return nil, err
	}
	if g.url != "" {
		ing.ws.SetURL(g.url)
	}
	g.wireConn(i, ing)
	return ing, nil
}

// Size is the number of sockets in the group.
func (g *FeedGroup) Size() int { return len(g.up) }

// slots returns the current socket of every slot.
func (g *FeedGroup) slots() []*Ingest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*Ingest(nil), g.conns...)
}

// Start connects every socket and streams merged ticks into tickCh. Blocks
// until ctx is cancelled (returns nil), ForceReconnect, or no socket is left
// (returns an error wrapping ErrFeedLost).
func (g *FeedGroup) Start(ctx context.Context, tickCh chan<- model.Tick) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Hooks may have been set after NewGroup; rewire before connecting.
	for i, ing := range g.slots() {
		g.wireConn(i, ing)
	}

	results := make(chan connResult, len(g.up))
	for i := range g.up {
		go func(i int) { results <- connResult{i, g.runSlot(runCtx, i, tickCh)} }(i)
	}
	go g.watchStalls(runCtx)

	var lastErr error
	for remaining := len(g.up); remaining > 0; {
		select {
		case <-ctx.Done():
			cancel()
			g.drain(results, remaining)
			return nil
		case err := <-g.fatal:
			cancel()
			g.drain(results, remaining)
			return err
		case r := <-results:
			remaining--
			if r.err != nil && (lastErr == nil || !errors.Is(r.err, smartconnect.ErrConnLimit)) {
				lastErr = r.err
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return lastErr
}

// runSlot runs slot i until ctx ends or the slot can no longer help: a 429
// (connection limit) or an auth rejection. Any other loss rebuilds the socket
// with every subscription it held, after a growing pause.
func (g *FeedGroup) runSlot(ctx context.Context, i int, tickCh chan<- model.Tick) error {
	delay := g.restartDelay
	for {
		ing := g.slots()[i]
		err := ing.Start(ctx, tickCh)
		g.setUp(i, false)
		switch {
		case err == nil || ctx.Err() != nil:
			return nil
		case errors.Is(err, smartconnect.ErrConnLimit):
			log.Printf("[ws] feed socket %d rejected: connection limit (%d per client code) — continuing on the others", i, MaxFeedConns)
			return err
		case errors.Is(err, smartconnect.ErrAuthRejected):
			log.Printf("[ws] feed socket %d: auth rejected — session needs a re-login", i)
			return err
		}
		log.Printf("[ws] feed socket %d gave up: %v — rebuilding in %v", i, err, delay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, slotRestartMaxDelay)

		cfg := g.cfg
		cfg.TokenList = nil
		cfg.Extra = ing.ws.Subscriptions() // configured + dynamic tokens it held
		next, nerr := g.newSlotIngest(i, cfg)
		if nerr != nil {
			return fmt.Errorf("%w: rebuild socket %d: %w", ErrFeedLost, i, nerr)
		}
		g.mu.Lock()
		g.conns[i] = next
		g.mu.Unlock()
	}
}

// connResult is how one slot ended.
type connResult struct {
	conn int
	err  error
}

// drain waits for the remaining slots to stop after runCtx was cancelled.
func (g *FeedGroup) drain(results <-chan connResult, remaining int) {
	for ; remaining > 0; remaining-- {
		<-results
	}
}

// wireConn installs the group hooks on socket i.
func (g *FeedGroup) wireConn(i int, ing *Ingest) {
	ing.OnReconnect = g.OnReconnect
	ing.OnTick = g.OnTick
	ing.OnIngested = g.OnIngested
	ing.OnDrop = g.OnDrop
	ing.OnSeqGap = g.OnSeqGap
	ing.OnClockSkew = g.OnClockSkew
	ing.OnConnState = func(connected bool) { g.setUp(i, connected) }
	if g.merge != nil {
		ing.accept = func(tick *model.Tick, seq int64) bool {
			ok, from := g.merge.accept(i, tick, seq)
			if from >= 0 {
				log.Printf("[ws] ⚠️  feed socket %d silent — primary moved %d → %d", from, from, i)
				if g.OnFailover != nil {
					g.OnFailover(from, i)
				}
			}
			if !ok {
				if g.OnDuplicate != nil {
					g.OnDuplicate()
				}
				return false
			}
			if g.OnDelivered != nil {
				g.OnDelivered(i)
			}
			return true
		}
	}
}

// setUp records socket i's state and reports group-level transitions.
func (g *FeedGroup) setUp(i int, connected bool) {
	g.mu.Lock()
	if g.up[i] == connected {
		g.mu.Unlock()
		return
	}
	g.up[i] = connected
	before := g.nUp
	if connected {
		g.nUp++
	} else {
		g.nUp--
	}
	after := g.nUp
	g.mu.Unlock()

	if g.OnConnsUp != nil {
		g.OnConnsUp(after)
	}
	if g.OnConnState != nil && (before == 0) != (after == 0) {
		g.OnConnState(after > 0)
	}
}

func (g *FeedGroup) isUp(i int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.up[i]
}

// watchStalls redials any open socket that went silent during market hours.
func (g *FeedGroup) watchStalls(ctx context.Context) {
	d := stallDetector{
		threshold:  connStallThreshold,
		interval:   connRedialInterval,
		marketOpen: g.marketOpen,
		lastRedial: make([]time.Time, len(g.up)),
	}
	t := time.NewTicker(stallCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := g.now()
		for i, ing := range g.slots() {
			if !g.isUp(i) {
				continue // reconnecting or gone: its own retry loop owns it
			}
			if silent, ok := d.shouldRedial(i, now, ing.LastFrameTime()); ok {
				reason := fmt.Sprintf("no frame for %v", silent.Truncate(100*time.Millisecond))
				log.Printf("[ws] ⚠️  feed socket %d stalled (%s) — redialling", i, reason)
				if g.OnRedial != nil {
					g.OnRedial(i, "stall")
				}
				ing.Redial(reason)
			}
		}
	}
}

// stallDetector decides when a socket is stalled. Pure logic, injectable time.
type stallDetector struct {
	threshold  time.Duration
	interval   time.Duration
	marketOpen func(time.Time) bool
	lastRedial []time.Time
}

// shouldRedial reports whether socket i, last heard from at lastFrame, must
// be redialled at now, and how long it has been silent. Records the redial.
func (d *stallDetector) shouldRedial(i int, now, lastFrame time.Time) (time.Duration, bool) {
	if lastFrame.IsZero() || (d.marketOpen != nil && !d.marketOpen(now)) {
		return 0, false
	}
	silent := now.Sub(lastFrame)
	if silent < d.threshold {
		return silent, false
	}
	if !d.lastRedial[i].IsZero() && now.Sub(d.lastRedial[i]) < d.interval {
		return silent, false
	}
	d.lastRedial[i] = now
	return silent, true
}

// ForceReconnect ends Start with ErrFeedLost so the session loop re-logins
// and builds new sockets (used by the stale-feed watchdog). Non-blocking.
func (g *FeedGroup) ForceReconnect(reason string) {
	select {
	case g.fatal <- fmt.Errorf("%w: forced reconnect: %s", ErrFeedLost, reason):
	default: // a fatal error is already pending
	}
}

// LastFrameTime is the newest frame time across sockets.
func (g *FeedGroup) LastFrameTime() time.Time {
	var newest time.Time
	for _, ing := range g.slots() {
		if t := ing.LastFrameTime(); t.After(newest) {
			newest = t
		}
	}
	return newest
}

// SubscribeTokens subscribes tokens on every socket. Each socket stores the
// subscription and resends it on reconnect, so it succeeds if any socket took
// it live.
func (g *FeedGroup) SubscribeTokens(mode int, tokens []smartconnect.TokenListEntry) error {
	slots := g.slots()
	var errs []error
	for i, ing := range slots {
		if err := ing.SubscribeTokens(mode, tokens); err != nil {
			errs = append(errs, fmt.Errorf("socket %d: %w", i, err))
		}
	}
	if len(errs) == len(slots) {
		return errors.Join(errs...)
	}
	return nil
}
