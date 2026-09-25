package gateway

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
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
	const ch = "pub:tick:NSE:1"
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
