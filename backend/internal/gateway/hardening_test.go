package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// countHellos drains c for d and returns the number of hello messages seen.
func countHellos(t *testing.T, c *Client, d time.Duration) int {
	t.Helper()
	n := 0
	deadline := time.After(d)
	for {
		select {
		case msg := <-c.send:
			var m map[string]interface{}
			if json.Unmarshal(msg, &m) == nil && m["type"] == "hello" {
				n++
			}
		case <-deadline:
			return n
		}
	}
}

// Item 1: go-redis reconnects internally and re-sends subscription
// confirmations on the same channel; a repeat confirmation must bump the epoch.
func TestRouteBumpsEpochOnInternalReconnect(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 32)
	oldEpoch := h.Epoch()

	ch := make(chan interface{}, 16)
	conn := pubsubConn{ch: ch, close: func() error { return nil }, confirmed: []string{"pub:ind:*"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Router.route(ctx, "pattern", conn)

	// Remaining initial confirmations: first sighting, no bump.
	ch <- &goredis.Subscription{Kind: "psubscribe", Channel: "pub:tick:*", Count: 2}
	if n := countHellos(t, c, 80*time.Millisecond); n != 0 {
		t.Fatalf("initial confirmations must not bump, got %d hellos", n)
	}

	// Internal reconnect: every channel is re-confirmed. One bump total.
	ch <- &goredis.Subscription{Kind: "psubscribe", Channel: "pub:ind:*", Count: 1}
	ch <- &goredis.Subscription{Kind: "psubscribe", Channel: "pub:tick:*", Count: 2}
	if n := countHellos(t, c, 150*time.Millisecond); n != 1 {
		t.Fatalf("reconnect should cause exactly one hello, got %d", n)
	}
	if h.Epoch() == oldEpoch {
		t.Fatal("epoch must change after internal reconnect")
	}
}

// Item 1: explicit + pattern subscriptions reconnecting together bump once.
func TestRequestEpochBumpCoalesces(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 32)
	h.requestEpochBump("explicit")
	h.requestEpochBump("pattern")
	if n := countHellos(t, c, 150*time.Millisecond); n != 1 {
		t.Fatalf("coalesced bumps: got %d hellos, want 1", n)
	}
}

// Item 2 (server side): epochs are ordered by their ms prefix, strictly increasing.
func TestBumpEpochStrictlyIncreasesStartMs(t *testing.T) {
	h := newTestHub()
	for i := 0; i < 5; i++ {
		old := h.Epoch()
		h.BumpEpoch("test")
		if epochStartMs(h.Epoch()) <= epochStartMs(old) {
			t.Fatalf("epoch start ms must increase: %s -> %s", old, h.Epoch())
		}
	}
}

// Item 3: a lossless event frame that cannot be enqueued disconnects the client.
// Full-state channels (orders, P&L) are latest-value-wins instead; see
// TestFullStateChannelKeepsLatestWhenQueueFull.
func TestLosslessChannelDisconnectsSlowClient(t *testing.T) {
	for _, ch := range []string{"pub:signal"} {
		h := newTestHub()
		c := addTestClient(h, 1)
		c.SafeSend([]byte("filler"))

		h.broadcast(ch, []byte(`{"x":1}`))

		if h.ClientCount() != 0 {
			t.Fatalf("%s: slow client must be disconnected", ch)
		}
		<-c.send // filler
		if _, ok := <-c.send; ok {
			t.Fatalf("%s: send channel must be closed", ch)
		}
	}
}

func TestLossyChannelKeepsSlowClient(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 1)
	c.SafeSend([]byte("filler"))

	h.broadcast("pub:tick:NSE:1", []byte(`{"price":1}`))

	if h.ClientCount() != 1 {
		t.Fatal("tick drop must not disconnect the client")
	}
	if c.Dropped() != 1 {
		t.Fatalf("dropped: got %d, want 1", c.Dropped())
	}
}

// Item 4: snapshots carry orders + P&L with their channel seqs.
func TestAttachLiveStatePrefersHubLatest(t *testing.T) {
	h := newTestHub()
	h.broadcast("pub:orders", []byte(`{"orders":[{"id":"a"}]}`))
	h.broadcast("pub:orders", []byte(`{"orders":[{"id":"b"}]}`))

	var asked []string
	fallback := func(key string) ([]byte, error) {
		asked = append(asked, key)
		if key == "pnl:summary" {
			return []byte(`{"realized_pnl":100}`), nil
		}
		return nil, errors.New("nil")
	}

	snap := &SnapshotResponse{}
	h.attachLiveState(snap, fallback)

	if snap.Epoch != h.Epoch() {
		t.Fatalf("epoch: got %q want %q", snap.Epoch, h.Epoch())
	}
	if snap.Orders == nil || string(*snap.Orders) != `{"orders":[{"id":"b"}]}` {
		t.Fatalf("orders: got %v", snap.Orders)
	}
	if snap.PnL == nil || string(*snap.PnL) != `{"realized_pnl":100}` {
		t.Fatalf("pnl: got %v", snap.PnL)
	}
	if snap.LiveSeqs["pub:orders"] != 2 || snap.LiveSeqs["pub:pnl"] != 0 {
		t.Fatalf("liveSeqs: got %v", snap.LiveSeqs)
	}
	if len(asked) != 1 || asked[0] != "pnl:summary" {
		t.Fatalf("fallback should only be used for missing state, asked %v", asked)
	}
}

func TestAttachLiveStateSkipsInvalidJSON(t *testing.T) {
	h := newTestHub()
	snap := &SnapshotResponse{}
	h.attachLiveState(snap, func(string) ([]byte, error) { return []byte("not json"), nil })
	if snap.Orders != nil || snap.PnL != nil {
		t.Fatal("invalid JSON must not be attached")
	}
	if _, err := json.Marshal(snap); err != nil {
		t.Fatalf("snapshot must stay marshalable: %v", err)
	}
}

// Item 6: concurrent snapshot requests for the same key share one build.
func TestSnapshotFlightSharesBuild(t *testing.T) {
	var g snapshotFlight
	var builds int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]*SnapshotResponse, 10)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, _ := g.Do("k", func() (*SnapshotResponse, error) {
				atomic.AddInt32(&builds, 1)
				<-release
				return &SnapshotResponse{Symbol: "X"}, nil
			})
			results[i] = s
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if builds != 1 {
		t.Fatalf("builds: got %d, want 1", builds)
	}
	for _, r := range results {
		if r == nil || r.Symbol != "X" {
			t.Fatalf("every caller must get the shared snapshot, got %v", r)
		}
	}
	// After completion a new call builds again.
	g.Do("k", func() (*SnapshotResponse, error) { atomic.AddInt32(&builds, 1); return &SnapshotResponse{}, nil })
	if builds != 2 {
		t.Fatalf("builds after completion: got %d, want 2", builds)
	}
}

func TestSnapshotKeyDependsOnIndicators(t *testing.T) {
	a := &ClientSubscription{Symbol: "NSE:1", TF: 60, IndEntries: []IndEntry{{"SMA_9", 60}, {"EMA_4", 60}}}
	b := &ClientSubscription{Symbol: "NSE:1", TF: 60, IndEntries: []IndEntry{{"EMA_4", 60}, {"SMA_9", 60}}}
	c := &ClientSubscription{Symbol: "NSE:1", TF: 60, IndEntries: []IndEntry{{"SMA_9", 60}}}
	if snapshotKey(a, 500) != snapshotKey(b, 500) {
		t.Fatal("indicator order must not change the key")
	}
	if snapshotKey(a, 500) == snapshotKey(c, 500) || snapshotKey(a, 500) == snapshotKey(a, 100) {
		t.Fatal("different indicators / limits must have different keys")
	}
}

// Item 7: idle channels are evicted; sticky state channels are kept; an
// evicted channel restarts at channel_seq 1.
func TestSweepIdleChannels(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 32)
	tick := "pub:tick:NSE:1"
	for i := 0; i < 3; i++ {
		h.broadcast(tick, []byte(`{"price":1}`))
	}
	h.broadcast("pub:orders", []byte(`{"orders":[]}`))
	h.broadcast("pub:analyst:state", []byte(`{}`))

	if n := h.sweepIdleChannels(time.Now(), 30*time.Minute); n != 0 {
		t.Fatalf("fresh channels must not be evicted, evicted %d", n)
	}
	if n := h.sweepIdleChannels(time.Now().Add(31*time.Minute), 30*time.Minute); n != 1 {
		t.Fatalf("evicted: got %d, want 1", n)
	}
	h.mu.RLock()
	_, inLatest := h.latest[tick]
	_, inSeqs := h.channelSeqs[tick]
	_, inBufs := h.replayBufs[tick]
	_, ordersKept := h.latest["pub:orders"]
	h.mu.RUnlock()
	if inLatest || inSeqs || inBufs {
		t.Fatal("idle channel must be removed from latest, channelSeqs and replayBufs")
	}
	if !ordersKept {
		t.Fatal("sticky state channel must not be evicted")
	}

	for len(c.send) > 0 {
		<-c.send
	}
	h.broadcast(tick, []byte(`{"price":2}`))
	if m := recvJSON(t, c); m["channel_seq"].(float64) != 1 {
		t.Fatalf("evicted channel must restart at seq 1, got %v", m["channel_seq"])
	}
}
