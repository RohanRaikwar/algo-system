package smartconnect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// orderStreamServer is a fake Angel order-status socket. Each accepted
// connection gets the AB00 hello, then the messages queued in push.
type orderStreamServer struct {
	*httptest.Server
	mu      sync.Mutex
	auths   []string
	status  atomic.Int32 // non-zero: reject the upgrade with this HTTP status
	pings   atomic.Int32
	silent  atomic.Bool // accept, then never send or answer
	push    chan string
	conns   atomic.Int32
	dropAll chan struct{}
}

func newOrderStreamServer(t *testing.T) *orderStreamServer {
	s := &orderStreamServer{push: make(chan string, 16), dropAll: make(chan struct{}, 1)}
	up := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		s.mu.Unlock()
		if st := s.status.Load(); st != 0 {
			http.Error(w, "rejected", int(st))
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.conns.Add(1)
		defer c.Close()
		if s.silent.Load() {
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}
		}
		var wmu sync.Mutex
		write := func(m string) error {
			wmu.Lock()
			defer wmu.Unlock()
			return c.WriteMessage(websocket.TextMessage, []byte(m))
		}
		_ = write(`{"user-id":"C1","status-code":"200","order-status":"AB00","error-message":"","orderData":{"orderid":"","orderstatus":""}}`)
		go func() {
			for {
				_, m, err := c.ReadMessage()
				if err != nil {
					return
				}
				if string(m) == "ping" {
					s.pings.Add(1)
					_ = write("pong")
				}
			}
		}()
		for {
			select {
			case m := <-s.push:
				if write(m) != nil {
					return
				}
			case <-s.dropAll:
				return
			case <-r.Context().Done():
				return
			}
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *orderStreamServer) authHeaders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

func newTestOrderStream(srv *orderStreamServer, tok func() string, onUpdate func(map[string]any)) *OrderStream {
	o := NewOrderStream(tok, onUpdate)
	o.dialer = websocket.DefaultDialer
	o.SetURL("ws" + strings.TrimPrefix(srv.URL, "http"))
	o.minDelay, o.maxDelay, o.limit = time.Millisecond, 5*time.Millisecond, 5*time.Millisecond
	return o
}

func runStream(t *testing.T, o *OrderStream) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { o.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Run did not stop after ctx cancel")
		}
	})
}

func TestOrderStream_DeliversUpdatesSkipsHello(t *testing.T) {
	srv := newOrderStreamServer(t)
	var mu sync.Mutex
	var got []map[string]any
	o := newTestOrderStream(srv, func() string { return "jwt1" }, func(row map[string]any) {
		mu.Lock()
		got = append(got, row)
		mu.Unlock()
	})
	runStream(t, o)
	srv.push <- `{"user-id":"C1","status-code":"200","order-status":"AB05","error-message":"","orderData":{"orderid":"O1","orderstatus":"complete","filledshares":"75","averageprice":101.25,"ordertag":"T1"}}`
	waitFor(t, "update", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	mu.Lock()
	row := got[0]
	mu.Unlock()
	if row["orderid"] != "O1" || row["orderstatus"] != "complete" || row["averageprice"] != 101.25 || row["filledshares"] != "75" {
		t.Fatalf("row = %v", row)
	}
	if h := srv.authHeaders(); h[0] != "Bearer jwt1" {
		t.Fatalf("Authorization = %q, want Bearer jwt1", h[0])
	}
}

func TestOrderStream_KeepsExistingBearerPrefix(t *testing.T) {
	srv := newOrderStreamServer(t)
	o := newTestOrderStream(srv, func() string { return "Bearer jwt1" }, nil)
	runStream(t, o)
	waitFor(t, "dial", func() bool { return len(srv.authHeaders()) > 0 })
	if h := srv.authHeaders()[0]; h != "Bearer jwt1" {
		t.Fatalf("Authorization = %q", h)
	}
}

func TestOrderStream_SendsPing(t *testing.T) {
	srv := newOrderStreamServer(t)
	o := newTestOrderStream(srv, func() string { return "jwt" }, nil)
	o.ping = 5 * time.Millisecond
	runStream(t, o)
	waitFor(t, "pings", func() bool { return srv.pings.Load() >= 2 })
}

func TestOrderStream_SilentServerReconnects(t *testing.T) {
	srv := newOrderStreamServer(t)
	srv.silent.Store(true)
	o := newTestOrderStream(srv, func() string { return "jwt" }, nil)
	o.ping = 5 * time.Millisecond // read deadline 15ms
	runStream(t, o)
	waitFor(t, "reconnect", func() bool { return srv.conns.Load() >= 2 })
}

func TestOrderStream_AuthRejectedRetriesWithFreshToken(t *testing.T) {
	srv := newOrderStreamServer(t)
	srv.status.Store(http.StatusUnauthorized)
	var n atomic.Int32
	o := newTestOrderStream(srv, func() string {
		if n.Add(1) == 1 {
			return "old"
		}
		return "new"
	}, nil)
	var up atomic.Bool
	o.OnState = func(c bool) {
		if c {
			up.Store(true)
		}
	}
	runStream(t, o)
	waitFor(t, "rejected dial", func() bool { return len(srv.authHeaders()) >= 1 })
	srv.status.Store(0)
	waitFor(t, "connected", up.Load)
	h := srv.authHeaders()
	if h[0] != "Bearer old" || h[len(h)-1] != "Bearer new" {
		t.Fatalf("auth headers = %v", h)
	}
}

func TestOrderStream_ConnLimitRetries(t *testing.T) {
	srv := newOrderStreamServer(t)
	srv.status.Store(http.StatusTooManyRequests)
	o := newTestOrderStream(srv, func() string { return "jwt" }, nil)
	runStream(t, o)
	waitFor(t, "retries after 429", func() bool { return len(srv.authHeaders()) >= 3 })
	if srv.conns.Load() != 0 {
		t.Fatal("connected despite 429")
	}
}

func TestOrderStream_ReconnectsAfterDrop(t *testing.T) {
	srv := newOrderStreamServer(t)
	var states []bool
	var mu sync.Mutex
	o := newTestOrderStream(srv, func() string { return "jwt" }, nil)
	o.OnState = func(c bool) { mu.Lock(); states = append(states, c); mu.Unlock() }
	runStream(t, o)
	waitFor(t, "first conn", func() bool { return srv.conns.Load() == 1 })
	srv.dropAll <- struct{}{}
	waitFor(t, "second conn", func() bool { return srv.conns.Load() == 2 })
	waitFor(t, "states", func() bool { mu.Lock(); defer mu.Unlock(); return len(states) >= 3 })
	mu.Lock()
	defer mu.Unlock()
	if !states[0] || states[1] || !states[2] {
		t.Fatalf("states = %v, want up, down, up", states)
	}
}

func TestOrderStream_OnErrorReportsFailedSession(t *testing.T) {
	srv := newOrderStreamServer(t)
	srv.status.Store(http.StatusUnauthorized)
	o := newTestOrderStream(srv, func() string { return "jwt" }, nil)
	var mu sync.Mutex
	var errs []error
	o.OnError = func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}
	runStream(t, o)
	waitFor(t, "error reported", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(errs) > 0
	})
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(errs[0], ErrAuthRejected) {
		t.Fatalf("err = %v, want ErrAuthRejected", errs[0])
	}
}
