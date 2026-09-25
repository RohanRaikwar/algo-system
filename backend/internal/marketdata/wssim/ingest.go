// Package wssim provides a WebSocket ingest client that connects to a custom/test
// WebSocket server (e.g. cmd/tickserver) and feeds ticks into the mdengine pipeline.
//
// The expected JSON message format on the wire is identical to model.Tick:
//
//	{"token":"2885","exchange":"NSE","price":185005000,"qty":10,"tick_ts":"..."}
//
// This is a drop-in replacement for internal/marketdata/ws but without any
// Angel One / SmartConnect dependency — useful for offline testing or custom feeds.
package wssim

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"time"

	"trading-systemv1/internal/model"

	"github.com/gorilla/websocket"
)

// maxEventSkew bounds how far a source timestamp may be from local receipt time
// before it is replaced by the receive time (same rule as the production ingest).
const maxEventSkew = 5 * time.Second

// maxFutureSkew caps how far ahead of local receipt a plausible event time may
// be (EventTS = min(EventTS, TickTS+maxFutureSkew)). It matches the
// aggregator's 1s bucket-level reorder tolerance, so a token whose clock runs
// ahead cannot open buckets that real time has not reached.
const maxFutureSkew = 1 * time.Second

// Config holds configuration for the simulated WS ingest.
type Config struct {
	// URL of the tick WebSocket server, e.g. "ws://localhost:9001/ws"
	URL string

	// ReconnectDelay is the initial delay before reconnection attempts.
	// Defaults to 2 seconds if zero.
	ReconnectDelay time.Duration

	// MaxReconnectDelay caps the exponential backoff. Defaults to 30s.
	MaxReconnectDelay time.Duration
}

func (c *Config) defaults() {
	if c.ReconnectDelay == 0 {
		c.ReconnectDelay = 2 * time.Second
	}
	if c.MaxReconnectDelay == 0 {
		c.MaxReconnectDelay = 30 * time.Second
	}
}

// Ingest connects to a plain-JSON WebSocket tick server and pushes model.Tick
// values into tickCh.  Same external interface as internal/marketdata/ws.Ingest.
type Ingest struct {
	cfg Config

	// Optional hooks (same semantics as internal/marketdata/ws.Ingest).
	OnReconnect func()                 // called each time a reconnection happens
	OnIngested  func(tick *model.Tick) // called for every parsed tick (TicksTotal, E2E latency)
	OnDrop      func()                 // called when tickCh is full and the tick is dropped
	OnClockSkew func()                 // called when an implausible source timestamp was replaced
	OnConnState func(connected bool)   // called on connect and on disconnect
}

// New creates a new Ingest.  Returns an error if the URL is unparseable.
func New(cfg Config) (*Ingest, error) {
	cfg.defaults()
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, err
	}
	return &Ingest{cfg: cfg}, nil
}

// Start connects to the custom WebSocket and streams ticks into tickCh.
// Blocks until ctx is cancelled.  Reconnects automatically on disconnect.
func (ing *Ingest) Start(ctx context.Context, tickCh chan<- model.Tick) error {
	delay := ing.cfg.ReconnectDelay

	for {
		// Check context before each attempt
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		err := ing.runOnce(ctx, tickCh)
		if err == nil {
			// Context cancelled cleanly
			return nil
		}

		log.Printf("[wssim] disconnected (%v), reconnecting in %s...", err, delay)
		if ing.OnReconnect != nil {
			ing.OnReconnect()
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}

		// Exponential backoff
		delay *= 2
		if delay > ing.cfg.MaxReconnectDelay {
			delay = ing.cfg.MaxReconnectDelay
		}
	}
}

// runOnce makes a single connection attempt and reads until disconnect or ctx cancel.
func (ing *Ingest) runOnce(ctx context.Context, tickCh chan<- model.Tick) error {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, ing.cfg.URL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	if ing.OnConnState != nil {
		ing.OnConnState(true)
		defer ing.OnConnState(false)
	}

	log.Printf("[wssim] connected to %s", ing.cfg.URL)

	// Async context watcher — closes the connection when ctx is cancelled.
	go func() {
		<-ctx.Done()
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
		conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			// Check if it's a context cancellation
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return err
		}

		ing.handleMessage(raw, time.Now().UTC(), tickCh)
	}
}

// handleMessage parses one wire message and pushes it into tickCh (non-blocking).
// The sim server's timestamp is the source ("exchange") time and becomes EventTS
// unless the message carries an explicit event_ts; TickTS is always the local
// receive time recvTS, matching the production ws ingest.
func (ing *Ingest) handleMessage(raw []byte, recvTS time.Time, tickCh chan<- model.Tick) {
	var tick model.Tick
	if err := json.Unmarshal(raw, &tick); err != nil {
		log.Printf("[wssim] parse error: %v (raw: %s)", err, raw)
		return
	}

	if tick.Token == "" {
		log.Printf("[wssim] skipping tick with empty token")
		return
	}

	if tick.EventTS.IsZero() {
		tick.EventTS = tick.TickTS
	}
	tick.TickTS = recvTS
	if d := tick.EventTS.Sub(recvTS); !tick.EventTS.IsZero() && (d > maxFutureSkew || d < -maxEventSkew) {
		if d > maxEventSkew || d < -maxEventSkew {
			tick.EventTS = recvTS
		} else {
			tick.EventTS = recvTS.Add(maxFutureSkew) // min(EventTS, recv + tolerance)
		}
		if ing.OnClockSkew != nil {
			ing.OnClockSkew()
		}
	}

	if ing.OnIngested != nil {
		ing.OnIngested(&tick)
	}

	select {
	case tickCh <- tick:
	default:
		if ing.OnDrop != nil {
			ing.OnDrop()
		} else {
			log.Println("[wssim] tickCh full, dropping tick")
		}
	}
}
