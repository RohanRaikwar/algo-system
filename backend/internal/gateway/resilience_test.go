package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// newTestHub builds a Hub without Redis for unit tests.
func newTestHub() *Hub {
	h := &Hub{
		clients:     make(map[*Client]bool),
		latest:      make(map[string]latestEntry),
		channelSeqs: make(map[string]int64),
		replayBufs:  make(map[string]*ReplayBuffer),
		epoch:       newEpoch(),

		epochBumpDebounce: 20 * time.Millisecond,
	}
	h.Router = NewPubSubRouter(h)
	h.Broadcaster = NewBroadcaster(h)
	return h
}

func addTestClient(h *Hub, queue int) *Client {
	c := &Client{send: make(chan []byte, queue), hub: h, subs: map[string]*ClientSubscription{}}
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
	return c
}

func recvJSON(t *testing.T, c *Client) map[string]interface{} {
	t.Helper()
	select {
	case msg := <-c.send:
		var m map[string]interface{}
		if err := json.Unmarshal(msg, &m); err != nil {
			t.Fatalf("invalid JSON: %v\nraw: %s", err, msg)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func TestNewEpochUnique(t *testing.T) {
	a, b := newEpoch(), newEpoch()
	if a == "" || a == b {
		t.Fatalf("epochs must be non-empty and unique: %q %q", a, b)
	}
}

func TestBroadcastIncludesEpoch(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 8)

	h.broadcast("pub:tick:NSE:1", []byte(`{"price":1}`))
	m := recvJSON(t, c)

	if m["epoch"] != h.Epoch() || h.Epoch() == "" {
		t.Fatalf("epoch: got %v, want %q", m["epoch"], h.Epoch())
	}
	if m["channel_seq"].(float64) != 1 {
		t.Fatalf("channel_seq: got %v", m["channel_seq"])
	}
}

func TestBumpEpochNotifiesClients(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 8)
	old := h.Epoch()

	h.BumpEpoch("test")

	if h.Epoch() == old {
		t.Fatal("epoch did not change")
	}
	m := recvJSON(t, c)
	if m["type"] != "hello" || m["epoch"] != h.Epoch() {
		t.Fatalf("expected hello with new epoch, got %v", m)
	}
}

func TestRegisterClientSendsHelloFirst(t *testing.T) {
	h := newTestHub()
	h.broadcast("pub:tick:NSE:1", []byte(`{"price":1}`))
	c := &Client{send: make(chan []byte, 8), hub: h, subs: map[string]*ClientSubscription{}}

	if n := h.registerClient(c); n != 1 {
		t.Fatalf("client count: got %d, want 1", n)
	}
	h.broadcast("pub:tick:NSE:1", []byte(`{"price":2}`))

	m := recvJSON(t, c)
	if m["type"] != "hello" || m["epoch"] != h.Epoch() || m["reason"] != "connect" {
		t.Fatalf("first message should be hello with epoch, got %v", m)
	}
	if next := recvJSON(t, c); next["epoch"] != h.Epoch() {
		t.Fatalf("data envelope after hello should carry epoch, got %v", next)
	}
}

func TestMissedResponseCompleteness(t *testing.T) {
	h := newTestHub()
	ch := "pub:tick:NSE:1"
	for i := 0; i < 600; i++ { // replay buffer holds 500
		h.broadcast(ch, []byte(`{"price":1}`))
	}

	full := buildMissedResponse(h, ch, 550, 560, "")
	if !full.Complete || full.Count != 11 || full.Epoch != h.Epoch() {
		t.Fatalf("expected complete range of 11, got complete=%v count=%d epoch=%q", full.Complete, full.Count, full.Epoch)
	}

	evicted := buildMissedResponse(h, ch, 50, 560, "")
	if evicted.Complete {
		t.Fatal("range partially evicted from replay buffer must be incomplete")
	}
	if evicted.OldestSeq != 101 {
		t.Fatalf("oldest_seq: got %d, want 101", evicted.OldestSeq)
	}

	stale := buildMissedResponse(h, ch, 550, 560, "some-old-epoch")
	if stale.Complete {
		t.Fatal("epoch mismatch must be incomplete")
	}

	unknown := buildMissedResponse(h, "pub:tick:NSE:none", 1, 2, "")
	if unknown.Complete {
		t.Fatal("unknown channel must be incomplete")
	}
}

func TestRunSubscriptionResubscribesAndBumpsEpoch(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 8)
	oldEpoch := h.Epoch()

	var calls int32
	second := make(chan interface{}, 1)
	source := func(ctx context.Context) (pubsubConn, error) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			ch := make(chan interface{})
			close(ch) // simulate PubSub channel dying
			return pubsubConn{ch: ch, close: func() error { return nil }}, nil
		case 2:
			return pubsubConn{}, errors.New("redis down")
		default:
			second <- &goredis.Message{Channel: "pub:orders", Payload: `{"orders":[]}`}
			return pubsubConn{ch: second, close: func() error { return nil }}, nil
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.Router.runSubscription(ctx, "test", source, time.Millisecond, 5*time.Millisecond)
		close(done)
	}()

	// Expect the routed message and a (debounced) hello for the epoch bump.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		m := recvJSON(t, c)
		if m["type"] == "hello" {
			got["hello"] = true
		} else if m["channel"] == "pub:orders" {
			got["msg"] = true
		}
	}
	if !got["hello"] || !got["msg"] {
		t.Fatalf("expected hello and routed message, got %v", got)
	}
	if h.Epoch() == oldEpoch {
		t.Fatal("epoch must change after resubscribe")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("source calls: got %d, want 3", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runSubscription did not exit on ctx cancel")
	}
}

func TestSafeSendCountsDrops(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 1)

	if !c.SafeSend([]byte("a")) {
		t.Fatal("first send should succeed")
	}
	if c.SafeSend([]byte("b")) || c.SafeSend([]byte("c")) {
		t.Fatal("sends to a full queue must be dropped")
	}
	if got := c.Dropped(); got != 2 {
		t.Fatalf("dropped: got %d, want 2", got)
	}
}
