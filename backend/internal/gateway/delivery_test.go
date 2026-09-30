package gateway

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func envelopeSeq(t *testing.T, raw []byte) (string, int64) {
	t.Helper()
	var m struct {
		Channel    string `json:"channel"`
		ChannelSeq int64  `json:"channel_seq"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("invalid envelope %s: %v", raw, err)
	}
	return m.Channel, m.ChannelSeq
}

// A client that cannot keep up with a full-state channel is not disconnected:
// only the newest pending frame per channel is kept and delivered once the
// queue has room, behind anything already queued.
func TestFullStateChannelKeepsLatestWhenQueueFull(t *testing.T) {
	for _, ch := range []string{"pub:orders", "pub:pnl", "pub:strike", "pub:analyst:NSE:99926000"} {
		h := newTestHub()
		c := addTestClient(h, 1)
		c.SafeSend([]byte("filler"))

		for i := 1; i <= 3; i++ {
			h.broadcast(ch, []byte(fmt.Sprintf(`{"v":%d}`, i)))
		}
		if h.ClientCount() != 1 {
			t.Fatalf("%s: full-state overflow must not disconnect the client", ch)
		}

		if got := string(<-c.send); got != "filler" {
			t.Fatalf("%s: queue head: got %q", ch, got)
		}
		c.flushPending()
		gotCh, seq := envelopeSeq(t, <-c.send)
		if gotCh != ch || seq != 3 {
			t.Fatalf("%s: pending frame: got %s seq %d, want seq 3", ch, gotCh, seq)
		}
		c.flushPending()
		select {
		case m := <-c.send:
			t.Fatalf("%s: only the latest frame may be delivered, got extra %s", ch, m)
		default:
		}
	}
}

// A pending frame is superseded (not delivered later, out of order) when a
// newer frame on the same channel fits in the queue.
func TestFullStatePendingSupersededByQueuedFrame(t *testing.T) {
	h := newTestHub()
	c := addTestClient(h, 1)
	c.SafeSend([]byte("filler"))
	h.broadcast("pub:orders", []byte(`{"v":1}`)) // pending
	<-c.send                                     // queue drains without flush
	h.broadcast("pub:orders", []byte(`{"v":2}`)) // fits
	_, seq := envelopeSeq(t, <-c.send)
	if seq != 2 {
		t.Fatalf("got seq %d, want 2", seq)
	}
	c.flushPending()
	select {
	case m := <-c.send:
		t.Fatalf("stale pending frame delivered after newer one: %s", m)
	default:
	}
}

// /api/missed reads current_seq under the hub lock; the replay buffer must
// already hold that seq, or the response is needlessly incomplete.
func TestReplayBufferHoldsCurrentSeq(t *testing.T) {
	h := newTestHub()
	const ch = "pub:signal"
	const n = 20000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			h.broadcast(ch, []byte(`{"p":1}`))
		}
	}()
	for {
		h.mu.RLock()
		cur := h.channelSeqs[ch]
		rb := h.replayBufs[ch]
		held := cur == 0 || (rb != nil && len(rb.Range(cur, cur)) == 1)
		h.mu.RUnlock()
		if !held {
			t.Fatalf("current_seq %d not yet in replay buffer", cur)
		}
		if cur >= n {
			break
		}
	}
	wg.Wait()
}

func TestReplayBuffer_Last(t *testing.T) {
	rb := NewReplayBuffer(3)
	if got := rb.Last(2); len(got) != 0 {
		t.Fatalf("empty: got %d entries", len(got))
	}
	for i := int64(1); i <= 5; i++ {
		rb.Push(i, []byte{byte(i)})
	}
	got := rb.Last(2)
	if len(got) != 2 || got[0].Seq != 4 || got[1].Seq != 5 {
		t.Fatalf("Last(2): got %+v", got)
	}
	if got := rb.Last(10); len(got) != 3 || got[0].Seq != 3 {
		t.Fatalf("Last(10): got %+v", got)
	}
}

// SNAPSHOT carries the most recent signal envelopes (with seqs), so a signal
// lost to a slow-client disconnect is recovered on resync.
func TestAttachLiveStateIncludesRecentSignals(t *testing.T) {
	h := newTestHub()
	for i := 1; i <= snapshotSignalCount+5; i++ {
		h.broadcast("pub:signal", []byte(fmt.Sprintf(`{"n":%d}`, i)))
	}
	snap := &SnapshotResponse{}
	h.attachLiveState(snap, nil)

	if len(snap.Signals) != snapshotSignalCount {
		t.Fatalf("signals: got %d, want %d", len(snap.Signals), snapshotSignalCount)
	}
	ch, first := envelopeSeq(t, snap.Signals[0])
	_, last := envelopeSeq(t, snap.Signals[len(snap.Signals)-1])
	if ch != "pub:signal" || first != 6 || last != int64(snapshotSignalCount+5) {
		t.Fatalf("signals range: %s %d..%d", ch, first, last)
	}
	if _, err := json.Marshal(snap); err != nil {
		t.Fatalf("snapshot must stay marshalable: %v", err)
	}
}

// Ticks keep no replay buffer; candle/indicator channels keep one only
// while a client subscribes to them, plus replayGrace after it leaves.
func TestReplayOnlyForSubscribedChannels(t *testing.T) {
	h := newTestHub()
	const tick, ind5, ind60, opt = "pub:tick:NFO:40712", "pub:ind:EMA_9:300s:NSE:99926000",
		"pub:ind:EMA_9:60s:NSE:99926000", "pub:ind:EMA_9:300s:NFO:40712"
	c := &Client{send: make(chan []byte, 64), hub: h, subs: map[string]*ClientSubscription{
		"NSE:99926000:300": {Symbol: "NSE:99926000", TF: 300, IndEntries: []IndEntry{{"EMA_9", 300}}},
	}}
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
	has := func(ch string) bool {
		h.mu.RLock()
		defer h.mu.RUnlock()
		_, ok := h.replayBufs[ch]
		return ok
	}
	for _, ch := range []string{tick, ind5, ind60, opt, "pub:signal"} {
		h.broadcast(ch, []byte(`{"v":1}`))
	}
	if has(tick) || !has(ind5) || has(ind60) || has(opt) || !has("pub:signal") {
		t.Fatalf("buffers: tick=%v ind5m=%v ind1m=%v option=%v signal=%v, want only ind5m and signal",
			has(tick), has(ind5), has(ind60), has(opt), has("pub:signal"))
	}
	if h.channelSeqs[tick] != 1 || h.channelSeqs[ind60] != 1 {
		t.Fatal("unbuffered channels must still count seq")
	}

	// Chart disconnects: the buffer survives replayGrace for its backfill…
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	h.broadcast(ind5, []byte(`{"v":2}`))
	if got := h.GetReplayRange(ind5, 1, 2); len(got) != 2 {
		t.Fatalf("within grace: %d buffered, want 2", len(got))
	}
	// …then goes.
	h.mu.Lock()
	h.replayWantedAt[ind5] = time.Now().Add(-replayGrace - time.Second)
	h.mu.Unlock()
	h.broadcast(ind5, []byte(`{"v":3}`))
	if has(ind5) {
		t.Fatal("buffer kept after grace with no subscriber")
	}
}
