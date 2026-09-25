package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/heartbeat"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"

	goredis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

// ActiveConfig holds the current indicator display configuration.
type ActiveConfig struct {
	Entries []IndicatorEntry `json:"entries"`
}

// IndicatorEntry represents a single indicator in the active config.
type IndicatorEntry struct {
	Name  string `json:"name"`
	TF    int    `json:"tf"`
	Color string `json:"color,omitempty"`
}

// Hub manages WebSocket clients and Redis PubSub fan-out.
// It acts as a compositor, delegating to focused components:
//   - PubSubRouter: Redis subscription + message routing
//   - Broadcaster: envelope construction + client-filtered fan-out
//   - ConfigStore: active indicator config CRUD + broadcast
type Hub struct {
	Rdb        *goredis.Client
	TFs        []int
	Tokens     []string
	Indicators []string

	// Optional SQLite reader for candle backfill when Redis is trimmed
	CandleReader model.CandleReader

	mu      sync.RWMutex
	clients map[*Client]bool
	latest  map[string]latestEntry
	seq     int64

	// Per-channel monotonic sequence numbers for gap detection
	channelSeqs map[string]int64

	// epoch identifies the current sequence space. It changes on process
	// start and whenever the Redis subscription is re-established (messages
	// may have been lost), telling clients to discard stored seqs and resync.
	// Guarded by mu.
	epoch string

	// Debounced epoch bumps: one Redis blip re-confirms many channels on two
	// subscriptions; requestEpochBump coalesces them into one bump.
	epochBumpDebounce time.Duration // 0 = defaultEpochBumpDebounce
	bumpMu            sync.Mutex
	bumpPending       bool

	// Shares one snapshot build between concurrent SUBSCRIBEs for the same
	// (symbol, tf, indicators, limit).
	snapFlight snapshotFlight

	// Per-channel replay buffers for gap backfill
	replayBufs map[string]*ReplayBuffer

	activeConfig ActiveConfig

	// End-to-end latency tracker (Point 8)
	Latency    *LatencyTracker
	Heartbeats *heartbeat.Aggregator

	// Sub-components
	Router      *PubSubRouter
	Broadcaster *Broadcaster
	ConfigStore *ConfigStore
}

type latestEntry struct {
	Data json.RawMessage
	TS   time.Time
	Seq  int64 // per-channel seq for gap detection
}

// NewHub creates a new Hub for managing WS clients and PubSub.
func NewHub(rdb *goredis.Client, tfs []int, tokens, indicators []string, candleReader model.CandleReader) *Hub {
	// Start with empty active config — indicators are added dynamically by the frontend
	h := &Hub{
		Rdb:          rdb,
		TFs:          tfs,
		Tokens:       tokens,
		Indicators:   indicators,
		CandleReader: candleReader,
		clients:      make(map[*Client]bool),
		latest:       make(map[string]latestEntry),
		channelSeqs:  make(map[string]int64),
		replayBufs:   make(map[string]*ReplayBuffer),
		epoch:        newEpoch(),
		Latency:      NewLatencyTracker(10000), // 10k sample ring buffer
		activeConfig: ActiveConfig{
			Entries: []IndicatorEntry{},
		},
	}
	// Wire sub-components
	h.Router = NewPubSubRouter(h)
	h.Broadcaster = NewBroadcaster(h)
	h.ConfigStore = NewConfigStore(h, rdb)

	// Restore active config from Redis (if previously persisted)
	h.ConfigStore.Load(context.Background())

	return h
}

// newEpoch returns a unique sequence-space identifier: start time + random suffix.
// The start-ms hex prefix orders epochs; clients compare it to ignore frames
// from an epoch older than the one they adopted.
func newEpoch() string {
	return newEpochAt(time.Now().UnixMilli())
}

func newEpochAt(ms int64) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x-%x", ms, time.Now().UnixNano())
	}
	return fmt.Sprintf("%x-%s", ms, hex.EncodeToString(b[:]))
}

// newEpochAfter returns an epoch whose start ms is strictly greater than old's,
// so epochs within one process are totally ordered by their prefix.
func newEpochAfter(old string) string {
	ms := time.Now().UnixMilli()
	if prev := epochStartMs(old); ms <= prev {
		ms = prev + 1
	}
	return newEpochAt(ms)
}

// epochStartMs parses the hex start-ms prefix of an epoch (0 if malformed).
func epochStartMs(epoch string) int64 {
	prefix, _, _ := strings.Cut(epoch, "-")
	ms, err := strconv.ParseInt(prefix, 16, 64)
	if err != nil {
		return 0
	}
	return ms
}

// Epoch returns the current sequence epoch.
func (h *Hub) Epoch() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.epoch
}

// helloMessage builds the {"type":"hello","epoch":...} control message.
func helloMessage(epoch, reason string) []byte {
	msg, _ := json.Marshal(map[string]interface{}{
		"type":   "hello",
		"epoch":  epoch,
		"reason": reason,
	})
	return msg
}

// BumpEpoch starts a new sequence epoch and pushes a hello to every client so
// they reset stored seqs and resync. Used when upstream messages may have been
// lost (e.g. Redis PubSub resubscribe). Seqs keep counting; the epoch change
// alone signals the discontinuity.
func (h *Hub) BumpEpoch(reason string) {
	h.mu.Lock()
	old := h.epoch
	h.epoch = newEpochAfter(old)
	epoch := h.epoch
	h.mu.Unlock()

	log.Printf("[api_gateway] seq epoch bumped %s -> %s (%s)", old, epoch, reason)

	msg := helloMessage(epoch, reason)
	h.mu.RLock()
	for client := range h.clients {
		client.SafeSend(msg)
	}
	h.mu.RUnlock()
}

const defaultEpochBumpDebounce = 500 * time.Millisecond

// requestEpochBump schedules a BumpEpoch after the debounce window; requests
// arriving while one is pending are absorbed, so one upstream blip (seen by
// both the explicit and pattern subscriptions, once per channel) bumps once.
func (h *Hub) requestEpochBump(reason string) {
	h.bumpMu.Lock()
	defer h.bumpMu.Unlock()
	if h.bumpPending {
		return
	}
	h.bumpPending = true
	d := h.epochBumpDebounce
	if d <= 0 {
		d = defaultEpochBumpDebounce
	}
	time.AfterFunc(d, func() {
		h.bumpMu.Lock()
		h.bumpPending = false
		h.bumpMu.Unlock()
		h.BumpEpoch(reason)
	})
}

// GetActiveConfig delegates to ConfigStore.
func (h *Hub) GetActiveConfig() ActiveConfig {
	return h.ConfigStore.Get()
}

// SetActiveConfig delegates to ConfigStore.
func (h *Hub) SetActiveConfig(cfg ActiveConfig) {
	h.ConfigStore.Set(cfg)
}

// Run starts the PubSub subscription loop. Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	channels := h.buildChannels()
	if len(channels) == 0 {
		log.Println("[api_gateway] WARNING: no channels to subscribe to")
		h.Router.RunPattern(ctx)
		return
	}

	go h.Router.RunPattern(ctx)
	h.Router.RunExplicit(ctx)
}

func (h *Hub) buildChannels() []string {
	var channels []string
	// Indicator channels are subscribed via wildcard pattern (pub:ind:*)
	// to support dynamic indicator additions without explicit resubscribe.
	for _, tf := range h.TFs {
		for _, tok := range h.Tokens {
			ch := fmt.Sprintf("pub:candle:%ds:%s", tf, tok)
			channels = append(channels, ch)
		}
	}
	for _, tok := range h.Tokens {
		ch := fmt.Sprintf("pub:candle:1s:%s", tok)
		channels = append(channels, ch)
	}
	return channels
}

// broadcast delegates to Broadcaster for performance-optimized fan-out.
func (h *Hub) broadcast(channel string, data []byte) {
	h.Broadcaster.Broadcast(channel, data)
}

// HandleWS upgrades an HTTP connection to WebSocket and registers the client.
func (h *Hub) HandleWS(w interface{ Header() map[string][]string }, r interface{ URL() string }) {
	// This is called via the handler wrapper in main.go
}

// HandleWSRequest handles WebSocket upgrade from standard http types.
func (h *Hub) HandleWSRequest(conn *websocket.Conn, lastTS string) {
	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
		hub:  h,
		subs: make(map[string]*ClientSubscription),
		filters: ClientFilters{
			TFs:    h.TFs,
			Tokens: h.Tokens,
		},
	}

	conn.EnableWriteCompression(true)

	count := h.registerClient(client)

	log.Printf("[api_gateway] ws client connected (%d total)", count)

	go client.sendInitialState(lastTS)
	go client.writePump()
	go client.readPump()
}

// registerClient queues a hello carrying the current epoch and adds the client
// to the fan-out set under one lock, so the hello precedes every sequenced
// envelope the client receives. Returns the new client count.
func (h *Hub) registerClient(c *Client) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.SafeSend(helloMessage(h.epoch, "connect"))
	h.clients[c] = true
	return len(h.clients)
}

// RemoveClient removes a client from the hub.
// Uses sync.Once to safely close the send channel — prevents panic if both
// readPump and writePump trigger removal concurrently.
func (h *Hub) RemoveClient(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.closeOnce.Do(func() { close(c.send) })
}

// GetLatestAll returns snapshot of all latest channel data.
func (h *Hub) GetLatestAll() map[string]json.RawMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cp := make(map[string]json.RawMessage, len(h.latest))
	for k, v := range h.latest {
		cp[k] = v.Data
	}
	return cp
}

// GetReplayRange returns buffered envelopes for a channel in [fromSeq, toSeq].
// Used by the /api/missed REST endpoint for client gap backfill.
func (h *Hub) GetReplayRange(channel string, fromSeq, toSeq int64) [][]byte {
	h.mu.RLock()
	rb, exists := h.replayBufs[channel]
	h.mu.RUnlock()
	if !exists {
		return nil
	}
	entries := rb.Range(fromSeq, toSeq)
	result := make([][]byte, len(entries))
	for i, e := range entries {
		result[i] = e.Data
	}
	return result
}

// GetChannelSeq returns the current sequence number for a channel.
func (h *Hub) GetChannelSeq(channel string) int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.channelSeqs[channel]
}

// ClientCount returns the number of connected WS clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// StartMetricsBroadcast sends system metrics to all WS clients every 2s.
func (h *Hub) StartMetricsBroadcast(ctx context.Context, start time.Time) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			m := CollectMetrics(start)
			if v, ok := ReadIndicatorLatency(ctx, h.Rdb); ok {
				m.IndicatorMs = v
			}
			if h.Latency != nil {
				m.LatencyP50, m.LatencyP95, m.LatencyP99 = h.Latency.Percentiles()
				m.LatencySamples = h.Latency.Count()
			}
			FillServiceMetrics(ctx, &m, h.Rdb, h.Heartbeats)
			envelope, _ := json.Marshal(map[string]interface{}{
				"type":         "metrics",
				"metrics":      m,
				"marketOpen":   markethours.IsMarketOpen(now),
				"marketStatus": markethours.StatusString(now),
			})
			h.mu.RLock()
			for client := range h.clients {
				client.SafeSend(envelope)
			}
			h.mu.RUnlock()
		}
	}
}

// MissedResponse is the /api/missed payload. Complete is false when the replay
// buffer no longer holds the whole requested range (or the client's epoch is
// stale); the client must then fall back to a full SNAPSHOT resync.
type MissedResponse struct {
	Channel    string            `json:"channel"`
	Epoch      string            `json:"epoch"`
	CurrentSeq int64             `json:"current_seq"`
	OldestSeq  int64             `json:"oldest_seq"`
	Count      int               `json:"count"`
	Complete   bool              `json:"complete"`
	Messages   []json.RawMessage `json:"messages"`
}

// buildMissedResponse collects buffered envelopes in [fromSeq, toSeq] and
// reports whether the range is complete. clientEpoch may be empty (legacy
// clients); when set it must match the current epoch for the range to count.
func buildMissedResponse(h *Hub, channel string, fromSeq, toSeq int64, clientEpoch string) MissedResponse {
	h.mu.RLock()
	rb := h.replayBufs[channel]
	epoch := h.epoch
	currentSeq := h.channelSeqs[channel]
	h.mu.RUnlock()

	resp := MissedResponse{
		Channel:    channel,
		Epoch:      epoch,
		CurrentSeq: currentSeq,
		Messages:   []json.RawMessage{},
	}
	if rb == nil {
		return resp
	}
	resp.OldestSeq = rb.OldestSeq()
	for _, e := range rb.Range(fromSeq, toSeq) {
		resp.Messages = append(resp.Messages, json.RawMessage(e.Data))
	}
	resp.Count = len(resp.Messages)
	// Per-channel seqs are contiguous, so a full range has exactly to-from+1 entries.
	resp.Complete = (clientEpoch == "" || clientEpoch == epoch) &&
		toSeq >= fromSeq && int64(resp.Count) == toSeq-fromSeq+1
	return resp
}

// liveStateChannels maps full-state channels included in SNAPSHOT to the Redis
// key stratengine writes alongside each publish (fallback after a gateway
// restart, before the first publish reaches this process).
var liveStateChannels = []struct{ channel, key string }{
	{"pub:orders", "orders:live"},
	{"pub:pnl", "pnl:summary"},
}

// snapshotSignalCount is how many recent pub:signal envelopes a SNAPSHOT
// carries, so a signal lost to a slow-client disconnect is recovered.
const snapshotSignalCount = 20

// attachLiveState fills the snapshot's epoch, orders, P&L and recent signals. Values come from
// hub.latest (read under the same lock as the epoch, so epoch + seq + data are
// consistent); channels with no latest entry use fallback(key) with seq 0.
// Invalid JSON is skipped so it cannot break marshaling of the snapshot.
func (h *Hub) attachLiveState(snap *SnapshotResponse, fallback func(key string) ([]byte, error)) {
	vals := make([][]byte, len(liveStateChannels))
	snap.LiveSeqs = make(map[string]int64, len(liveStateChannels))

	h.mu.RLock()
	snap.Epoch = h.epoch
	for i, lc := range liveStateChannels {
		if e, ok := h.latest[lc.channel]; ok {
			vals[i] = e.Data
			snap.LiveSeqs[lc.channel] = e.Seq
		}
	}
	// Signal envelopes are pushed under h.mu, so these match snap.Epoch.
	if rb := h.replayBufs["pub:signal"]; rb != nil {
		for _, e := range rb.Last(snapshotSignalCount) {
			snap.Signals = append(snap.Signals, json.RawMessage(e.Data))
		}
	}
	h.mu.RUnlock()

	for i, lc := range liveStateChannels {
		if vals[i] == nil && fallback != nil {
			if b, err := fallback(lc.key); err == nil {
				vals[i] = b
				snap.LiveSeqs[lc.channel] = 0
			}
		}
		if vals[i] == nil || !json.Valid(vals[i]) {
			delete(snap.LiveSeqs, lc.channel)
			continue
		}
		raw := json.RawMessage(vals[i])
		switch lc.channel {
		case "pub:orders":
			snap.Orders = &raw
		case "pub:pnl":
			snap.PnL = &raw
		}
	}
}

// Idle channel eviction. Dynamic channels (ticks, candles, indicators per
// token/tf) would otherwise grow latest/channelSeqs/replayBufs without bound.
const (
	idleChannelTTL      = 30 * time.Minute
	idleChannelInterval = 5 * time.Minute
)

// isStickyChannel reports channels that are never evicted: fixed full-state
// channels whose latest value must survive long quiet periods.
func isStickyChannel(ch string) bool {
	switch ch {
	case "pub:orders", "pub:pnl", "pub:signal", "pub:strike":
		return true
	}
	return strings.HasPrefix(ch, "pub:analyst:")
}

// sweepIdleChannels evicts non-sticky channels with no message since now-idle
// from latest, channelSeqs and replayBufs. Returns the number evicted.
//
// Seq semantics: an evicted channel that becomes active again restarts at
// channel_seq 1 within the same epoch. Seq 1 only ever means "channel
// (re)created", and eviction requires the channel to have been silent, so no
// messages are lost across the restart; clients accept seq 1 after a higher
// stored seq as a restart rather than a regression. A client asking
// /api/missed for an evicted range gets complete=false and resyncs.
func (h *Hub) sweepIdleChannels(now time.Time, idle time.Duration) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for ch, e := range h.latest {
		if isStickyChannel(ch) || now.Sub(e.TS) < idle {
			continue
		}
		delete(h.latest, ch)
		delete(h.channelSeqs, ch)
		delete(h.replayBufs, ch)
		n++
	}
	return n
}

// StartIdleSweep periodically evicts idle channels. Blocks until ctx is cancelled.
func (h *Hub) StartIdleSweep(ctx context.Context) {
	ticker := time.NewTicker(idleChannelInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if n := h.sweepIdleChannels(now.UTC(), idleChannelTTL); n > 0 {
				log.Printf("[api_gateway] evicted %d idle channels", n)
			}
		}
	}
}
