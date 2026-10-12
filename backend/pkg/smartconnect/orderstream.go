package smartconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// OrderStreamURL is Angel One's order-status push socket. It only sends
// order updates; orders are still placed over REST.
const OrderStreamURL = "wss://tns.angelone.in/smart-order-update"

const (
	orderStreamPing      = 10 * time.Second
	orderStreamReadLimit = 3 * orderStreamPing
	orderStreamMaxDelay  = 30 * time.Second
	orderStreamLimitWait = 60 * time.Second // after a 429 (3 sockets per client code)
)

// OrderStream follows Angel One's order-status socket and hands every order
// update to OnUpdate. It reconnects forever (until ctx ends), reading a
// fresh token from TokenFunc on every dial.
type OrderStream struct {
	// TokenFunc returns the session JWT, with or without "Bearer ".
	TokenFunc func() string
	// OnUpdate receives the orderData object of each push that names an
	// order. Its keys match an order-book row. Called on the read goroutine;
	// must not block.
	OnUpdate func(orderData map[string]any)
	// OnState reports connect (true) and disconnect (false). Optional.
	OnState func(connected bool)
	// OnError receives the error that ended each failed or dropped session.
	// Optional; called on the Run goroutine, must not block.
	OnError func(err error)

	url      string
	dialer   *websocket.Dialer
	ping     time.Duration
	minDelay time.Duration
	maxDelay time.Duration
	limit    time.Duration
}

// NewOrderStream returns a stream for the production URL.
func NewOrderStream(tokenFunc func() string, onUpdate func(map[string]any)) *OrderStream {
	return &OrderStream{
		TokenFunc: tokenFunc,
		OnUpdate:  onUpdate,
		url:       OrderStreamURL,
		dialer:    NewPinnedDialer(0),
		ping:      orderStreamPing,
		minDelay:  time.Second,
		maxDelay:  orderStreamMaxDelay,
		limit:     orderStreamLimitWait,
	}
}

// SetURL points the stream at another server (tests).
func (o *OrderStream) SetURL(u string) { o.url = u }

// Run connects and follows updates until ctx is done.
func (o *OrderStream) Run(ctx context.Context) {
	delay := o.minDelay
	for ctx.Err() == nil {
		start := time.Now()
		err := o.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if o.OnError != nil && err != nil {
			o.OnError(err)
		}
		wait := delay
		switch {
		case errors.Is(err, ErrConnLimit):
			wait = o.limit
			log.Printf("[orderstream] ❌ connection limit reached (3 per client code): %v — retrying in %s", err, wait)
		case errors.Is(err, ErrAuthRejected):
			log.Printf("[orderstream] ❌ token rejected: %v — retrying with a fresh token in %s", err, wait)
		default:
			log.Printf("[orderstream] ⚠️  disconnected: %v — reconnecting in %s", err, wait)
		}
		// A session that stayed up a while starts the backoff over.
		if time.Since(start) > 2*o.maxDelay {
			delay = o.minDelay
		} else if delay *= 2; delay > o.maxDelay {
			delay = o.maxDelay
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// session runs one connection until it fails or ctx ends.
func (o *OrderStream) session(ctx context.Context) error {
	tok := ""
	if o.TokenFunc != nil {
		tok = o.TokenFunc()
	}
	if tok == "" {
		return fmt.Errorf("%w: no session token", ErrAuthRejected)
	}
	if !strings.HasPrefix(tok, "Bearer ") {
		tok = "Bearer " + tok
	}
	header := http.Header{}
	header.Set("Authorization", tok)
	conn, resp, err := o.dialer.DialContext(ctx, o.url, header)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return fmt.Errorf("%w: %s", ErrAuthRejected, resp.Status)
			case http.StatusTooManyRequests:
				return fmt.Errorf("%w: %s", ErrConnLimit, resp.Status)
			}
		}
		return err
	}
	defer conn.Close()

	extend := func() { _ = conn.SetReadDeadline(time.Now().Add(3 * o.ping)) }
	extend()
	conn.SetPongHandler(func(string) error { extend(); return nil })
	conn.SetPingHandler(func(data string) error {
		extend()
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})

	log.Printf("[orderstream] ✅ connected")
	o.state(true)
	defer o.state(false)

	// Heartbeat: Angel expects a "ping" every 10s and answers "pong". This
	// goroutine is the only data writer.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(o.ping)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				conn.Close()
				return
			case <-t.C:
				_ = conn.SetWriteDeadline(time.Now().Add(o.ping))
				if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
					conn.Close() // unblocks the reader
					return
				}
			}
		}
	}()
	defer func() { close(done); wg.Wait() }()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		extend()
		o.handle(msg)
	}
}

func (o *OrderStream) state(up bool) {
	if o.OnState != nil {
		o.OnState(up)
	}
}

// handle decodes one push. Plain json.Unmarshal (numbers as float64), the
// same as the REST order book, so callers parse both the same way.
func (o *OrderStream) handle(msg []byte) {
	text := strings.TrimSpace(string(msg))
	if text == "" || text == "pong" {
		return
	}
	var push struct {
		Status    string         `json:"order-status"`
		Code      string         `json:"status-code"`
		Error     string         `json:"error-message"`
		OrderData map[string]any `json:"orderData"`
	}
	if err := json.Unmarshal(msg, &push); err != nil {
		log.Printf("[orderstream] unreadable message %q: %v", truncate(text, 200), err)
		return
	}
	if push.Error != "" {
		log.Printf("[orderstream] server error %s %s: %s", push.Code, push.Status, push.Error)
	}
	if push.OrderData == nil {
		return
	}
	if id, _ := push.OrderData["orderid"].(string); id == "" {
		return // AB00 connect message carries an empty order
	}
	if o.OnUpdate != nil {
		o.OnUpdate(push.OrderData)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
