//go:build drills

package drills

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"trading-systemv1/internal/marketdata/wssim"
	"trading-systemv1/internal/model"
)

// tickServer is a killable stand-in for cmd/tickserver: it streams
// model.Tick JSON every 20ms to each client while up, and answers 503
// while down.
type tickServer struct {
	srv   *httptest.Server
	down  atomic.Bool
	mu    sync.Mutex
	conns map[*websocket.Conn]struct{}
}

func newTickServer(t *testing.T) *tickServer {
	ts := &tickServer{conns: map[*websocket.Conn]struct{}{}}
	up := websocket.Upgrader{}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ts.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ts.mu.Lock()
		ts.conns[c] = struct{}{}
		ts.mu.Unlock()
		defer func() {
			ts.mu.Lock()
			delete(ts.conns, c)
			ts.mu.Unlock()
			c.Close()
		}()
		for i := 0; ; i++ {
			now := time.Now().UTC()
			tk := model.Tick{Token: "99926000", Exchange: "NSE", Price: 2_500_000 + int64(i%50), TickTS: now}
			if err := c.WriteMessage(websocket.TextMessage, tk.JSON()); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func (ts *tickServer) url() string { return "ws" + strings.TrimPrefix(ts.srv.URL, "http") }

// kill drops every live connection and refuses new ones.
func (ts *tickServer) kill() {
	ts.down.Store(true)
	ts.mu.Lock()
	for c := range ts.conns {
		c.Close()
	}
	ts.mu.Unlock()
}

func (ts *tickServer) revive() { ts.down.Store(false) }

// Drill W1 — tick WebSocket killed mid-stream (staging ingest, wssim).
//
// Expected: ingest notices the drop, fires OnReconnect (→ mdengine_ws_reconnects_total),
// retries with exponential backoff capped at MaxReconnectDelay, and ticks
// resume on the same tickCh once the source is back, with no restart.
// Ticks sent while down are lost (the feed has no replay).
func TestDrill_WSKill_IngestReconnects(t *testing.T) {
	ts := newTickServer(t)
	ing, err := wssim.New(wssim.Config{URL: ts.url(), ReconnectDelay: 100 * time.Millisecond, MaxReconnectDelay: 400 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var reconnects, ingested atomic.Int64
	ing.OnReconnect = func() { reconnects.Add(1) }
	ing.OnIngested = func(*model.Tick) { ingested.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tickCh := make(chan model.Tick, 4096)
	go ing.Start(ctx, tickCh)

	waitTicks := func(what string, n int64, within time.Duration) {
		t.Helper()
		start := ingested.Load()
		deadline := time.Now().Add(within)
		for ingested.Load()-start < n {
			if time.Now().After(deadline) {
				t.Fatalf("%s: fewer than %d ticks within %s", what, n, within)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitTicks("baseline", 10, 2*time.Second)

	ts.kill()
	time.Sleep(1500 * time.Millisecond) // several backoff rounds: 100, 200, 400, 400ms
	if reconnects.Load() < 2 {
		t.Fatalf("reconnect attempts while down = %d, want >= 2", reconnects.Load())
	}
	stalled := ingested.Load()
	time.Sleep(200 * time.Millisecond)
	if ingested.Load() != stalled {
		t.Fatal("ticks still arriving while the source is down")
	}

	revived := time.Now()
	ts.revive()
	waitTicks("after revive", 10, 3*time.Second)
	t.Logf("reconnect attempts=%d, ticks resumed %s after source came back (backoff cap 400ms)",
		reconnects.Load(), time.Since(revived).Round(10*time.Millisecond))
}
