package smartconnect

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestHandleError_SingleReconnectAtATime verifies that concurrent handleError
// calls (readLoop + heartbeatLoop failing together) run only one reconnect loop,
// and that the loop keeps retrying past maxRetryAttempt (warning once) until closed.
func TestHandleError_SingleReconnectAtATime(t *testing.T) {
	s, err := NewSmartWebSocketV3("auth", "api", "client", "feed", 3, 0, 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.retryDelay = time.Millisecond

	var dials atomic.Int32
	s.Dialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dials.Add(1) == 6 {
				go s.CloseConnection() // end the session after a few more attempts
			}
			time.Sleep(5 * time.Millisecond) // keep the reconnect in flight
			return nil, errors.New("dial refused (test)")
		},
	}
	var maxRetryErrors atomic.Int32
	s.OnError = func(code, msg string) { maxRetryErrors.Add(1) }

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleError(0, errors.New("boom"))
		}()
	}
	wg.Wait()

	if got := dials.Load(); got < 6 || got > 7 {
		t.Fatalf("dial attempts = %d, want ~6 (one loop, retrying past maxRetryAttempt until closed)", got)
	}
	if got := maxRetryErrors.Load(); got != 1 {
		t.Fatalf("OnError calls = %d, want 1 (max-retry warning, not per attempt)", got)
	}
}

// wsTestServer is a local websocket endpoint that tracks live server-side connections.
type wsTestServer struct {
	*httptest.Server
	accepted atomic.Int32
	open     atomic.Int32
	msgs     atomic.Int32
	status   atomic.Int32 // non-zero: reject the upgrade with this HTTP status
}

func newWSTestServer(t *testing.T) *wsTestServer {
	ts := &wsTestServer{}
	up := websocket.Upgrader{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if st := ts.status.Load(); st != 0 {
			http.Error(w, "rejected", int(st))
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ts.accepted.Add(1)
		ts.open.Add(1)
		defer ts.open.Add(-1)
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
			ts.msgs.Add(1)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newTestSocket(t *testing.T, ts *wsTestServer) *SmartWebSocketV3 {
	s, err := NewSmartWebSocketV3("auth", "api", "client", "feed", 3, 0, 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.url = "ws" + strings.TrimPrefix(ts.URL, "http")
	s.retryDelay = time.Millisecond
	t.Cleanup(s.CloseConnection)
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (s *SmartWebSocketV3) testGen() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// A stale error from a superseded connection must not trigger a second
// reconnect that swaps out the healthy socket.
func TestHandleError_StaleGenerationIsNoop(t *testing.T) {
	ts := newWSTestServer(t)
	s := newTestSocket(t, ts)
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	gen1 := s.testGen()

	s.handleError(gen1, errors.New("read error on conn 1"))
	waitFor(t, "reconnect", func() bool { return ts.accepted.Load() == 2 })
	gen2 := s.testGen()
	if gen2 == gen1 {
		t.Fatal("generation did not advance on reconnect")
	}

	// Late error from the old connection's loops.
	s.handleError(gen1, errors.New("stale read error"))
	time.Sleep(50 * time.Millisecond)

	if got := ts.accepted.Load(); got != 2 {
		t.Fatalf("server accepted %d connections, want 2 (stale error must not reconnect)", got)
	}
	if got := s.testGen(); got != gen2 {
		t.Fatalf("generation changed %d -> %d on stale error", gen2, got)
	}
	waitFor(t, "old connection closed", func() bool { return ts.open.Load() == 1 })
}

// Reconnect must close the old connection and stop its heartbeat.
func TestHandleError_ClosesOldConnAndStopsHeartbeat(t *testing.T) {
	ts := newWSTestServer(t)
	s := newTestSocket(t, ts)
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	done1 := s.connDone
	s.mu.Unlock()

	s.handleError(s.testGen(), errors.New("ping write failed"))

	select {
	case <-done1:
	default:
		t.Fatal("old connection's done channel not closed: its heartbeat keeps running")
	}
	waitFor(t, "exactly one live server connection", func() bool {
		return ts.accepted.Load() == 2 && ts.open.Load() == 1
	})
}

// A failed resubscribe is a failed attempt: the socket is retired and retried.
func TestHandleError_ResubscribeFailureRetries(t *testing.T) {
	ts := newWSTestServer(t)
	s := newTestSocket(t, ts)
	var calls atomic.Int32
	s.resubscribe = func() error {
		if calls.Add(1) == 1 {
			return errors.New("resubscribe failed (test)")
		}
		return nil
	}

	s.handleError(0, errors.New("boom"))

	if got := calls.Load(); got != 2 {
		t.Fatalf("resubscribe calls = %d, want 2 (first failure retried)", got)
	}
	waitFor(t, "one live connection after retry", func() bool {
		return ts.accepted.Load() == 2 && ts.open.Load() == 1
	})
}

// An auth rejection on reconnect means the session/token is dead: surface it
// instead of retrying with the same credentials.
func TestHandleError_AuthRejectedSurfaces(t *testing.T) {
	ts := newWSTestServer(t)
	ts.status.Store(http.StatusUnauthorized)
	s := newTestSocket(t, ts)
	var codes []string
	var mu sync.Mutex
	s.OnError = func(code, msg string) { mu.Lock(); codes = append(codes, code); mu.Unlock() }

	done := make(chan struct{})
	go func() { s.handleError(0, errors.New("boom")); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect loop kept retrying after auth rejection")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(codes) != 1 || codes[0] != ErrCodeAuthRejected {
		t.Fatalf("OnError codes = %v, want [%s]", codes, ErrCodeAuthRejected)
	}
}

// When reconnects keep failing for retryDuration, give up and surface it so the
// session owner can re-login.
func TestHandleError_RetryDurationExhaustedSurfaces(t *testing.T) {
	s, _ := NewSmartWebSocketV3("auth", "api", "client", "feed", 1000, 0, 0, 1, 0)
	s.retryDelay = time.Millisecond
	s.retryDuration = 30 * time.Millisecond
	s.Dialer = &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dial refused (test)")
	}}
	var code atomic.Value
	s.OnError = func(c, msg string) { code.Store(c) }

	done := make(chan struct{})
	go func() { s.handleError(0, errors.New("boom")); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect loop did not give up after retryDuration")
	}
	if got, _ := code.Load().(string); got != ErrCodeRetryExhausted {
		t.Fatalf("OnError code = %q, want %q", got, ErrCodeRetryExhausted)
	}
}

func TestRetryBackoff_Capped(t *testing.T) {
	s, _ := NewSmartWebSocketV3("auth", "api", "client", "feed", 5, 1, 5, 2, 30)
	if got := s.retryBackoff(1); got != 5*time.Second {
		t.Fatalf("backoff(1) = %v, want 5s", got)
	}
	if got := s.retryBackoff(3); got != 20*time.Second {
		t.Fatalf("backoff(3) = %v, want 20s", got)
	}
	if got := s.retryBackoff(40); got != maxRetryDelay {
		t.Fatalf("backoff(40) = %v, want cap %v", got, maxRetryDelay)
	}
}

// OnOpen re-subscribing the same tokens on every reconnect must not grow the
// resubscribe map.
func TestSubscribe_DedupesTokens(t *testing.T) {
	ts := newWSTestServer(t)
	s := newTestSocket(t, ts)
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	tl := []TokenListEntry{{ExchangeType: NSE_CM, Tokens: []string{"26000", "26009"}}}
	for i := 0; i < 3; i++ {
		if err := s.Subscribe("x", ModeLTP, tl); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	got := len(s.inputRequestMap[ModeLTP][NSE_CM])
	s.mu.Unlock()
	if got != 2 {
		t.Fatalf("stored tokens = %d, want 2", got)
	}
}

// newSilentWSServer accepts websocket connections and then neither reads nor
// writes: a half-open peer (no data, no pong replies).
func newSilentWSServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var accepted atomic.Int32
	done := make(chan struct{})
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted.Add(1)
		defer c.Close()
		<-done
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })
	return srv, &accepted
}

// A socket that stops delivering anything (not even pongs) must hit the read
// deadline and go through the reconnect path instead of blocking forever.
func TestReadDeadline_SilentServerTriggersReconnect(t *testing.T) {
	srv, accepted := newSilentWSServer(t)
	s, err := NewSmartWebSocketV3("auth", "api", "client", "feed", 3, 0, 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	s.retryDelay = time.Millisecond
	s.readTimeout = 100 * time.Millisecond
	var closes atomic.Int32
	s.OnClose = func() { closes.Add(1) }
	t.Cleanup(s.CloseConnection)

	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reconnect after read deadline", func() bool { return accepted.Load() >= 2 && closes.Load() >= 1 })
}

// stallConn simulates a peer whose TCP window is full: once stalled, Write
// blocks until the write deadline (forever without one).
type stallConn struct {
	net.Conn
	stalled  atomic.Bool
	mu       sync.Mutex
	deadline time.Time
}

func (c *stallConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}

func (c *stallConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetDeadline(t)
}

func (c *stallConn) Write(b []byte) (int, error) {
	if !c.stalled.Load() {
		return c.Conn.Write(b)
	}
	for c.stalled.Load() {
		c.mu.Lock()
		d := c.deadline
		c.mu.Unlock()
		if !d.IsZero() && time.Now().After(d) {
			return 0, errors.New("i/o timeout (stalled peer)")
		}
		time.Sleep(time.Millisecond)
	}
	return c.Conn.Write(b)
}

// Subscribe writes under s.mu; without a write deadline a wedged peer would
// hold the lock (and any reconnect waiting on it) forever.
func TestSubscribe_WriteDeadlineOnStalledPeer(t *testing.T) {
	ts := newWSTestServer(t)
	s := newTestSocket(t, ts)
	s.writeTimeout = 50 * time.Millisecond
	var sc *stallConn
	s.Dialer = &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			sc = &stallConn{Conn: c}
			return sc, nil
		},
	}
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	sc.stalled.Store(true)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Subscribe("x", ModeLTP, []TokenListEntry{{ExchangeType: NSE_CM, Tokens: []string{"26000"}}})
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Subscribe on a stalled peer returned nil")
		}
	case <-time.After(2 * time.Second):
		sc.stalled.Store(false)
		t.Fatal("Subscribe blocked on a stalled peer (no write deadline)")
	}
}

// newRecordingWSServer is a websocket endpoint that records every text frame.
func newRecordingWSServer(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var msgs []string
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			msgs = append(msgs, string(b))
			mu.Unlock()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), msgs...)
	}
}

// Tokens subscribed while the socket is down (Conn nil: the send fails, the
// request is only stored) must be sent once a connection is established —
// OnOpen alone subscribes only the configured list.
func TestConnect_SendsSubscriptionsQueuedWhileDisconnected(t *testing.T) {
	srv, msgs := newRecordingWSServer(t)
	s, err := NewSmartWebSocketV3("auth", "api", "client", "feed", 3, 0, 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	t.Cleanup(s.CloseConnection)

	if err := s.Subscribe("dynamic_fno", ModeLTP, []TokenListEntry{{ExchangeType: NSE_FO, Tokens: []string{"57710"}}}); err == nil {
		t.Fatal("Subscribe with no connection returned nil")
	}
	s.OnOpen = func() {
		s.Subscribe("ohlc_ingest", ModeLTP, []TokenListEntry{{ExchangeType: NSE_CM, Tokens: []string{"26000"}}})
	}
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "queued dynamic token sent after connect", func() bool {
		for _, m := range msgs() {
			if strings.Contains(m, `"57710"`) {
				return true
			}
		}
		return false
	})
}

// LastFrameTime lets the session owner tell a dead socket from a live one
// that simply carries no ticks: it advances on every received frame.
func TestLastFrameTime_AdvancesOnReceivedFrames(t *testing.T) {
	send := make(chan struct{})
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		<-send
		c.WriteMessage(websocket.TextMessage, []byte("pong"))
		c.ReadMessage() // hold the connection open
	}))
	t.Cleanup(srv.Close)
	s, err := NewSmartWebSocketV3("auth", "api", "client", "feed", 3, 0, 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	t.Cleanup(s.CloseConnection)

	if !s.LastFrameTime().IsZero() {
		t.Fatal("LastFrameTime before any connection should be zero")
	}
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	atConnect := s.LastFrameTime()
	if atConnect.IsZero() {
		t.Fatal("LastFrameTime not set on connect")
	}
	time.Sleep(20 * time.Millisecond)
	close(send)
	waitFor(t, "LastFrameTime to advance on a received frame", func() bool {
		return s.LastFrameTime().After(atConnect.Add(10 * time.Millisecond))
	})
}
