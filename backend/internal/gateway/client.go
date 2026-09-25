package gateway

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// pongWait is how long the server waits for any read activity (data or pong)
	// before considering the client dead and closing the connection.
	pongWait = 60 * time.Second

	// pingInterval is how often the server sends a WS-level ping frame.
	// Must be less than pongWait so the client has time to respond.
	// 25s gives a comfortable margin: 2 pings fit within 60s pongWait.
	pingInterval = 25 * time.Second

	// writeWait is the deadline for any write operation (data frames + ping frames).
	writeWait = 15 * time.Second

	// dropLogInterval rate-limits the slow-client drop log per client.
	dropLogInterval = 10 * time.Second
)

// Client represents a single WebSocket peer.
type Client struct {
	conn      *websocket.Conn
	send      chan []byte
	hub       *Hub
	filters   ClientFilters
	closeOnce sync.Once // guards close(send) to prevent double-close panic

	// Slow-client drop accounting: total drops, and drop count / unix-nano
	// time at the last log line (for rate-limited logging).
	dropped       atomic.Uint64
	droppedLogged atomic.Uint64
	lastDropLogNs atomic.Int64

	// Latest-value-wins slot per full-state channel (orders, P&L) for frames
	// that did not fit in send. Guarded by pendMu, which is also held while
	// enqueueing full-state frames so a pending frame is never re-queued
	// behind a newer one on the same channel.
	pendMu  sync.Mutex
	pending map[string][]byte

	// Per-client subscriptions: key = "symbol:tf"
	subMu sync.RWMutex
	subs  map[string]*ClientSubscription
}

// ClientFilters allows per-client subscription filtering.
type ClientFilters struct {
	TFs        []int    `json:"tfs"`
	Tokens     []string `json:"tokens"`
	Indicators []string `json:"indicators"`
}

func (c *Client) sendInitialState(lastTS string) {
	c.hub.mu.RLock()
	defer c.hub.mu.RUnlock()

	var cutoff time.Time
	if lastTS != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, lastTS); err == nil {
			cutoff = parsed
		}
	}

	for channel, entry := range c.hub.latest {
		if !cutoff.IsZero() && !entry.TS.After(cutoff) {
			continue
		}

		envelope, _ := json.Marshal(map[string]interface{}{
			"channel": channel,
			"data":    json.RawMessage(entry.Data),
			"ts":      entry.TS.Format(time.RFC3339Nano),
			"initial": true,
		})
		c.SafeSend(envelope)
	}
}

// SafeSend attempts to send a message to the client without blocking.
// It recovers from panics if the channel is already closed.
func (c *Client) SafeSend(msg []byte) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	select {
	case c.send <- msg:
		return true
	default:
		c.recordDrop()
		return false
	}
}

// SendLatest enqueues a full-state frame. If the queue is full the frame is
// parked as the channel's pending value (replacing any older one) instead of
// disconnecting the client; writePump re-queues it once there is room. A newer
// frame that fits supersedes the pending one.
func (c *Client) SendLatest(channel string, msg []byte) {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	if c.SafeSend(msg) {
		delete(c.pending, channel)
		return
	}
	if c.pending == nil {
		c.pending = make(map[string][]byte)
	}
	c.pending[channel] = msg
}

// flushPending moves parked full-state frames into the send queue (FIFO, so
// they follow any older frame of the same channel still queued). Frames that
// still do not fit stay pending. A frame is only parked while send is full,
// so writePump always has another iteration in which to flush it.
func (c *Client) flushPending() {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for ch, msg := range c.pending {
		if !c.trySend(msg) {
			return
		}
		delete(c.pending, ch)
	}
}

// trySend is SafeSend without drop accounting.
func (c *Client) trySend(msg []byte) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// Dropped returns the number of messages dropped because the send queue was full.
func (c *Client) Dropped() uint64 {
	return c.dropped.Load()
}

// recordDrop counts a dropped message and logs at most once per dropLogInterval.
func (c *Client) recordDrop() {
	total := c.dropped.Add(1)
	now := time.Now().UnixNano()
	last := c.lastDropLogNs.Load()
	if now-last < int64(dropLogInterval) || !c.lastDropLogNs.CompareAndSwap(last, now) {
		return
	}
	since := total - c.droppedLogged.Swap(total)
	remote := ""
	if c.conn != nil {
		remote = c.conn.RemoteAddr().String()
	}
	log.Printf("[api_gateway] slow ws client %s: dropped %d msgs (total %d), send queue full (cap %d)",
		remote, since, total, cap(c.send))
}

// disconnectSlow drops a client that could not take a lossless event frame. Removing
// it closes send (writePump sends a close frame); closing conn unblocks both
// pumps. The client reconnects, gets a hello and resyncs.
func (c *Client) disconnectSlow(channel string) {
	remote := ""
	if c.conn != nil {
		remote = c.conn.RemoteAddr().String()
	}
	log.Printf("[api_gateway] disconnecting slow ws client %s: send queue full for lossless %s", remote, channel)
	c.hub.RemoveClient(c)
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))

			// Write coalescing: use NextWriter to batch queued messages
			// into a single WebSocket frame with newline separators
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(msg)

			// Drain any queued messages into the same write
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
			c.flushPending()
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) readPump() {
	defer func() {
		c.hub.RemoveClient(c)
		c.conn.Close()
		log.Println("[api_gateway] ws client disconnected")
	}()

	c.conn.SetReadLimit(4096) // Increased for SUBSCRIBE messages
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		// Reset read deadline on ANY successful read — not just WS pong.
		// This keeps the connection alive as long as the client is sending
		// any data (JSON pings, SUBSCRIBEs, filter updates, etc.).
		c.conn.SetReadDeadline(time.Now().Add(pongWait))

		// Parse message type
		var base struct {
			Type string `json:"type"`
			Ping int64  `json:"ping"`
		}
		if json.Unmarshal(msg, &base) != nil {
			continue
		}

		switch base.Type {
		case "SUBSCRIBE":
			var subMsg SubscribeMsg
			if err := json.Unmarshal(msg, &subMsg); err != nil {
				SendError(c, "", "invalid SUBSCRIBE: "+err.Error())
				continue
			}
			go c.handleSubscribe(subMsg)

		case "UNSUBSCRIBE":
			var unsubMsg UnsubscribeMsg
			if err := json.Unmarshal(msg, &unsubMsg); err != nil {
				continue
			}
			c.handleUnsubscribe(unsubMsg)

		default:
			// Handle ping/pong (backward compat)
			if base.Ping > 0 {
				pong, _ := json.Marshal(map[string]interface{}{
					"type":      "pong",
					"ping":      base.Ping,
					"server_ts": time.Now().UnixMilli(),
				})
				c.SafeSend(pong)
				continue
			}
			// Legacy: filter update
			var filters ClientFilters
			if json.Unmarshal(msg, &filters) == nil {
				c.filters = filters
			}
		}
	}
}

// handleSubscribe processes a SUBSCRIBE message from the client.
func (c *Client) handleSubscribe(msg SubscribeMsg) {
	if msg.Symbol == "" || msg.TF <= 0 {
		SendError(c, msg.ReqID, "symbol and tf are required")
		return
	}

	// Resolve indicator entries with composite (name, tf) identity
	indEntries := ResolveIndEntries(msg.Indicators, msg.TF)

	sub := &ClientSubscription{
		Symbol:     msg.Symbol,
		TF:         msg.TF,
		Indicators: msg.Indicators,
		IndEntries: indEntries,
	}

	// Store subscription
	c.subMu.Lock()
	if c.subs == nil {
		c.subs = make(map[string]*ClientSubscription)
	}
	c.subs[sub.SubKey()] = sub
	c.subMu.Unlock()

	indNames := make([]string, len(indEntries))
	for i, e := range indEntries {
		indNames[i] = e.Key()
	}
	log.Printf("[subscribe] client subscribed: symbol=%s tf=%d indicators=%v",
		msg.Symbol, msg.TF, indNames)

	// Check if indengine needs new indicators
	ctx := context.Background()
	hasNew := publishNewIndicators(ctx, c.hub.Rdb, c.hub, msg.Indicators)

	// Build and send snapshot
	candleLimit := msg.History.Candles
	if candleLimit <= 0 {
		candleLimit = 500
	}

	// Concurrent SUBSCRIBEs for the same view share one build.
	shared, err := c.hub.snapFlight.Do(snapshotKey(sub, candleLimit), func() (*SnapshotResponse, error) {
		// Always wait for indicator streams to have data before sending snapshot.
		// New indicators need longer timeout (full recomputation by indengine),
		// known indicators need shorter timeout (just verifying stream readiness).
		if len(sub.IndEntries) > 0 {
			timeout := 3 * time.Second
			if hasNew {
				timeout = 8 * time.Second
				log.Printf("[subscribe] waiting for indengine to compute new indicators...")
			}
			waitForIndicators(ctx, c.hub.Rdb, sub, timeout)
		}
		return BuildSnapshotFromRedis(ctx, c.hub.Rdb, sub, candleLimit, c.hub.CandleReader)
	})
	if err != nil {
		SendError(c, msg.ReqID, "snapshot build failed: "+err.Error())
		return
	}
	snapCopy := *shared // shared build is read-only; per-client fields go on the copy
	snap := &snapCopy
	snap.ReqID = msg.ReqID
	c.hub.attachLiveState(snap, func(key string) ([]byte, error) {
		return c.hub.Rdb.Get(ctx, key).Bytes()
	})

	SendJSON(c, snap)
	log.Printf("[subscribe] sent snapshot: symbol=%s tf=%d candles=%d indicators=%d",
		msg.Symbol, msg.TF, len(snap.Candles), len(snap.Indicators))
}

// handleUnsubscribe removes a subscription.
func (c *Client) handleUnsubscribe(msg UnsubscribeMsg) {
	sub := &ClientSubscription{Symbol: msg.Symbol, TF: msg.TF}
	c.subMu.Lock()
	delete(c.subs, sub.SubKey())
	c.subMu.Unlock()

	log.Printf("[subscribe] client unsubscribed: symbol=%s tf=%d", msg.Symbol, msg.TF)
}

// matchesChannel checks if a PubSub channel matches any of this client's subscriptions.
// Returns true if the client should receive this message.
func (c *Client) matchesChannel(channel string) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()

	if len(c.subs) == 0 {
		// No subscriptions — legacy mode, receive everything
		return true
	}

	parsed := parseChannel(channel)
	if parsed == nil {
		return true // non-data channel (metrics, config) — always deliver
	}

	// Tick channels are always delivered — all clients need live FNO LTP
	// for P&L calculation and stoploss tracking.
	if parsed.chType == "tick" {
		return true
	}

	symbol := parsed.exchange + ":" + parsed.token
	for _, sub := range c.subs {
		if sub.Symbol != symbol {
			continue
		}
		// Candle channel — must match subscription's main TF
		if parsed.chType == "candle" {
			if sub.TF == parsed.tf {
				return true
			}
			continue
		}
		// Indicator channel — check against IndEntries by both name AND TF
		if parsed.chType == "indicator" {
			for _, entry := range sub.IndEntries {
				if entry.Name == parsed.indName && entry.TF == parsed.tf {
					return true
				}
			}
		}
	}
	return false
}

// parsedChannel holds the parsed components of a Redis PubSub channel name.
type parsedChannel struct {
	chType   string // "candle", "indicator", "tick"
	indName  string // for indicator channels: "SMA_9", "EMA_4"
	tf       int    // timeframe in seconds
	exchange string // "NSE"
	token    string // "99926000"
}

// parseChannel parses a PubSub channel like "pub:candle:60s:NSE:99926000"
// or "pub:ind:SMA_9:60s:NSE:99926000".
func parseChannel(channel string) *parsedChannel {
	parts := strings.Split(channel, ":")
	if len(parts) < 4 {
		return nil
	}

	// pub:candle:60s:NSE:99926000  (5 parts)
	if parts[0] == "pub" && parts[1] == "candle" && len(parts) >= 5 {
		tf := parseTFStr(parts[2])
		return &parsedChannel{
			chType:   "candle",
			tf:       tf,
			exchange: parts[3],
			token:    parts[4],
		}
	}

	// pub:ind:SMA_9:60s:NSE:99926000  (6 parts)
	if parts[0] == "pub" && parts[1] == "ind" && len(parts) >= 6 {
		tf := parseTFStr(parts[3])
		return &parsedChannel{
			chType:   "indicator",
			indName:  parts[2],
			tf:       tf,
			exchange: parts[4],
			token:    parts[5],
		}
	}

	// pub:tick:NSE:99926000  (4 parts)
	if parts[0] == "pub" && parts[1] == "tick" && len(parts) >= 4 {
		return &parsedChannel{
			chType:   "tick",
			exchange: parts[2],
			token:    parts[3],
		}
	}

	return nil
}

// parseTFStr parses "60s" → 60.
func parseTFStr(s string) int {
	s = strings.TrimSuffix(s, "s")
	n := 0
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int(ch-'0')
		}
	}
	return n
}
