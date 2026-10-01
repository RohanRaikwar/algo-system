package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"trading-systemv1/internal/model"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

func quote(token string, seq int64, price int64) map[string]interface{} {
	return map[string]interface{}{
		"token": token, "exchange_type": 1, "last_traded_price": price,
		"sequence_number": seq, "exchange_timestamp": time.Now().UnixMilli(),
	}
}

func newTestGroup(t *testing.T, n int, tickCh chan model.Tick) *FeedGroup {
	t.Helper()
	g, err := NewGroup(IngestConfig{AuthToken: "a", APIKey: "k", ClientCode: "c", FeedToken: "f"}, n, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, ing := range g.conns {
		g.wireConn(i, ing)
		ing.wire(tickCh)
	}
	return g
}

// Ticks pass only from the primary socket while it is live: the standby's
// copies (and its own conflated index prices) are dropped.
func TestFeedGroup_PrimaryOnlyWhileLive(t *testing.T) {
	tickCh := make(chan model.Tick, 16)
	g := newTestGroup(t, 2, tickCh)
	var dups, from0, from1 int
	g.OnDuplicate = func() { dups++ }
	g.OnDelivered = func(c int) {
		if c == 0 {
			from0++
		} else {
			from1++
		}
	}
	g.merge.lastFrame = func(int) time.Time { return time.Now() } // every socket live
	a, b := g.conns[0].ws, g.conns[1].ws

	a.OnData(quote("26000", 100, 1))
	b.OnData(quote("26000", 100, 1))
	b.OnData(quote("99926000", 0, 22297_60)) // index: no seq, socket-specific value
	a.OnData(quote("99926000", 0, 22297_65))
	a.OnData(quote("26000", 101, 2))

	if got := len(tickCh); got != 3 {
		t.Fatalf("ticks passed = %d, want 3 (socket 0 only)", got)
	}
	if dups != 2 || from0 != 3 || from1 != 0 {
		t.Fatalf("dups=%d from0=%d from1=%d, want 2/3/0", dups, from0, from1)
	}
}

// A silent primary hands over to the next socket that delivers; right after
// the switch, ticks older than the last accepted one for a token are dropped.
func TestFeedMerger_FailoverAndSwitchGuard(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	frames := []time.Time{now, now}
	m := newFeedMerger(func(i int) time.Time { return frames[i] })
	m.now = func() time.Time { return now }
	tk := func(token string, ev time.Time) *model.Tick {
		return &model.Tick{Exchange: "NSE", Token: token, EventTS: ev}
	}

	if ok, sw := m.accept(0, tk("1", now), 10); !ok || sw != -1 {
		t.Fatalf("primary tick: ok=%v switched=%d", ok, sw)
	}
	if ok, _ := m.accept(1, tk("1", now), 10); ok {
		t.Fatal("standby copy passed while primary live")
	}

	now = now.Add(2 * time.Second) // primary silent > primaryFailover
	frames[1] = now
	if ok, sw := m.accept(1, tk("1", now.Add(-3*time.Second)), 9); ok || sw != 0 {
		t.Fatalf("stale tick after switch: ok=%v switched=%d, want dropped, switched from 0", ok, sw)
	}
	if m.Primary() != 1 {
		t.Fatalf("primary = %d, want 1", m.Primary())
	}
	if ok, _ := m.accept(1, tk("1", now), 11); !ok {
		t.Fatal("newer tick from new primary dropped")
	}
	if ok, _ := m.accept(1, tk("idx", now), 0); !ok {
		t.Fatal("first no-seq tick for a token dropped")
	}
	// The old primary recovering does not take ticks back while 1 is live.
	frames[0] = now
	if ok, _ := m.accept(0, tk("1", now), 12); ok {
		t.Fatal("old primary took over while new primary live")
	}
	// After the guard window an older exchange time from the primary passes
	// (raw exchange times can be off; sanitizeEventTS handles them later).
	now = now.Add(switchGuard)
	frames[1] = now
	if ok, _ := m.accept(1, tk("1", now.Add(-time.Minute)), 13); !ok {
		t.Fatal("primary tick dropped outside the switch guard")
	}
}

// A primary that still sends frames but runs behind (standby has delivered
// seqs the primary has not reached for lagFailover) hands over; one that
// catches up in time does not.
func TestFeedMerger_LaggingPrimaryFailsOver(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	m := newFeedMerger(func(int) time.Time { return now }) // both sockets "live"
	m.now = func() time.Time { return now }
	tk := &model.Tick{Exchange: "NSE", Token: "1"}

	m.accept(0, tk, 100)
	m.accept(1, tk, 100)                   // copy, standby level
	if ok, _ := m.accept(1, tk, 105); ok { // standby ahead: probe opens
		t.Fatal("standby passed before the lag window")
	}
	now = now.Add(500 * time.Millisecond)
	m.accept(0, tk, 105) // primary catches up: probe closes
	now = now.Add(time.Second)
	if ok, _ := m.accept(1, tk, 106); ok || m.Primary() != 0 {
		t.Fatal("failover although the primary caught up")
	}

	now = now.Add(lagFailover) // primary never reached 106
	if ok, sw := m.accept(1, tk, 107); !ok || sw != 0 || m.Primary() != 1 {
		t.Fatalf("lagging primary kept: ok=%v switched=%d primary=%d", ok, sw, m.Primary())
	}
}

// A primary that never connected (e.g. its slot got 429) hands over at once.
func TestFeedMerger_NeverConnectedPrimary(t *testing.T) {
	m := newFeedMerger(func(i int) time.Time {
		if i == 0 {
			return time.Time{}
		}
		return time.Now()
	})
	if ok, sw := m.accept(1, &model.Tick{Exchange: "NSE", Token: "1"}, 5); !ok || sw != 0 {
		t.Fatalf("ok=%v switched=%d, want accepted, switched from 0", ok, sw)
	}
}

// A single-socket group has no dedup: every packet passes as before.
func TestFeedGroup_SingleSocketNoDedup(t *testing.T) {
	tickCh := make(chan model.Tick, 4)
	g := newTestGroup(t, 1, tickCh)
	g.conns[0].ws.OnData(quote("26000", 5, 1))
	g.conns[0].ws.OnData(quote("26000", 5, 1))
	if len(tickCh) != 2 {
		t.Fatalf("ticks = %d, want 2", len(tickCh))
	}
}

func TestFeedGroup_ConnStateIsGroupLevel(t *testing.T) {
	g := newTestGroup(t, 2, make(chan model.Tick, 1))
	var states []bool
	var ups []int
	g.OnConnState = func(up bool) { states = append(states, up) }
	g.OnConnsUp = func(n int) { ups = append(ups, n) }

	g.setUp(0, true)
	g.setUp(1, true)
	g.setUp(0, false)
	g.setUp(0, false) // repeat: no change
	g.setUp(1, false)

	if len(states) != 2 || !states[0] || states[1] {
		t.Fatalf("group states = %v, want [true false]", states)
	}
	if want := []int{1, 2, 1, 0}; len(ups) != len(want) || ups[0] != 1 || ups[1] != 2 || ups[2] != 1 || ups[3] != 0 {
		t.Fatalf("conns up = %v, want %v", ups, want)
	}
}

func TestStallDetector(t *testing.T) {
	open := true
	d := stallDetector{
		threshold: 5 * time.Second, interval: 15 * time.Second,
		marketOpen: func(time.Time) bool { return open },
		lastRedial: make([]time.Time, 2),
	}
	t0 := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)

	if _, ok := d.shouldRedial(0, t0.Add(4*time.Second), t0); ok {
		t.Fatal("redial before threshold")
	}
	if _, ok := d.shouldRedial(0, t0.Add(5*time.Second), t0); !ok {
		t.Fatal("no redial at threshold")
	}
	if _, ok := d.shouldRedial(0, t0.Add(10*time.Second), t0); ok {
		t.Fatal("redial again inside the rate limit")
	}
	if _, ok := d.shouldRedial(1, t0.Add(10*time.Second), t0); !ok {
		t.Fatal("rate limit must be per socket")
	}
	if _, ok := d.shouldRedial(0, t0.Add(21*time.Second), t0); !ok {
		t.Fatal("no redial after the rate limit passed")
	}
	open = false
	if _, ok := d.shouldRedial(1, t0.Add(time.Hour), t0); ok {
		t.Fatal("redial outside market hours")
	}
	if _, ok := d.shouldRedial(1, t0, time.Time{}); ok {
		t.Fatal("redial before first connect")
	}
}

// feedServer accepts the first `accept` upgrades and answers later ones with
// 429, like Angel past its per-client socket limit.
func feedServer(t *testing.T, accept int32) (*httptest.Server, *atomic.Int32) {
	var n, open atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) > accept {
			http.Error(w, "too many", http.StatusTooManyRequests)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		open.Add(1)
		defer open.Add(-1)
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &open
}

func pointAt(g *FeedGroup, srv *httptest.Server) {
	g.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	for _, ing := range g.conns {
		ing.ws.SetURL(g.url)
	}
}

// A 429 on the second socket drops it; the group keeps running on the first.
func TestFeedGroup_ConnLimitKeepsRemainingSocket(t *testing.T) {
	srv, open := feedServer(t, 1)
	g, err := NewGroup(IngestConfig{AuthToken: "a", APIKey: "k", ClientCode: "c", FeedToken: "f"}, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	pointAt(g, srv)
	var ups atomic.Int32
	g.OnConnsUp = func(n int) { ups.Store(int32(n)) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Start(ctx, make(chan model.Tick, 1)) }()

	deadline := time.Now().Add(2 * time.Second)
	for open.Load() != 1 || ups.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("open=%d up=%d, want 1/1", open.Load(), ups.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("group ended on a 429 of one socket: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start after cancel = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

// When every socket is rejected the group ends with ErrFeedLost, so the
// session loop backs off and re-logins.
func TestFeedGroup_AllSocketsLostEndsWithFeedLost(t *testing.T) {
	srv, _ := feedServer(t, 0)
	g, err := NewGroup(IngestConfig{AuthToken: "a", APIKey: "k", ClientCode: "c", FeedToken: "f"}, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	pointAt(g, srv)
	select {
	case err := <-func() chan error {
		c := make(chan error, 1)
		go func() { c <- g.Start(context.Background(), make(chan model.Tick, 1)) }()
		return c
	}():
		if !errors.Is(err, ErrFeedLost) || !errors.Is(err, smartconnect.ErrConnLimit) {
			t.Fatalf("err = %v, want ErrFeedLost wrapping ErrConnLimit", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return when every socket was rejected")
	}
}

func TestFeedGroup_ForceReconnectEndsStart(t *testing.T) {
	srv, _ := feedServer(t, 2)
	g, err := NewGroup(IngestConfig{AuthToken: "a", APIKey: "k", ClientCode: "c", FeedToken: "f"}, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	pointAt(g, srv)
	done := make(chan error, 1)
	go func() { done <- g.Start(context.Background(), make(chan model.Tick, 1)) }()
	time.Sleep(50 * time.Millisecond)
	g.ForceReconnect("stale feed")
	select {
	case err := <-done:
		if !errors.Is(err, ErrFeedLost) {
			t.Fatalf("err = %v, want ErrFeedLost", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ForceReconnect did not end Start")
	}
}

func TestNewGroup_ClampsSize(t *testing.T) {
	cfg := IngestConfig{AuthToken: "a", APIKey: "k", ClientCode: "c", FeedToken: "f"}
	for _, c := range []struct{ in, want int }{{0, 1}, {2, 2}, {9, MaxFeedConns}} {
		g, err := NewGroup(cfg, c.in, nil)
		if err != nil {
			t.Fatal(err)
		}
		if g.Size() != c.want {
			t.Fatalf("NewGroup(%d).Size() = %d, want %d", c.in, g.Size(), c.want)
		}
	}
}

// A socket whose first dial fails is rebuilt while the group runs, and the
// rebuilt socket subscribes every token the old one held (incl. dynamic ones).
func TestFeedGroup_RebuildsLostSocketWithSubscriptions(t *testing.T) {
	var reqs atomic.Int32
	msgs := make(chan string, 16)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqs.Add(1) == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, m, err := c.ReadMessage()
			if err != nil {
				return
			}
			msgs <- string(m)
		}
	}))
	t.Cleanup(srv.Close)

	g, err := NewGroup(IngestConfig{
		AuthToken: "a", APIKey: "k", ClientCode: "c", FeedToken: "f",
		SubscribeMode: smartconnect.ModeQuote,
		TokenList:     []smartconnect.TokenListEntry{{ExchangeType: 1, Tokens: []string{"99926000"}}},
	}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.restartDelay = 10 * time.Millisecond
	pointAt(g, srv)
	if err := g.SubscribeTokens(smartconnect.ModeSnapQuote, []smartconnect.TokenListEntry{{ExchangeType: 2, Tokens: []string{"40679"}}}); err == nil {
		t.Fatal("subscribe before connect reported success")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Start(ctx, make(chan model.Tick, 1))

	got := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for !(got["99926000"] && got["40679"]) {
		select {
		case m := <-msgs:
			for _, tok := range []string{"99926000", "40679"} {
				if strings.Contains(m, tok) {
					got[tok] = true
				}
			}
		case <-deadline:
			t.Fatalf("rebuilt socket subscribed %v, want both tokens", got)
		}
	}
}
