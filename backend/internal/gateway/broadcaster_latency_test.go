package gateway

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestExtractTickTS(t *testing.T) {
	want := time.Date(2026, 9, 24, 9, 30, 1, 123456789, time.UTC)
	data := model.Tick{Token: "99926000", Exchange: "NSE", Price: 2306310, TickTS: want}.JSON()

	got := extractTickTS(data)
	if !got.Equal(want) {
		t.Fatalf("extractTickTS = %v, want %v", got, want)
	}
}

func TestExtractTickTS_NoField(t *testing.T) {
	// Candle payloads carry "ts" (bucket start), which must not count.
	if got := extractTickTS([]byte(`{"ts":"2026-09-24T09:30:00Z","open":1}`)); !got.IsZero() {
		t.Fatalf("expected zero time for payload without tick_ts, got %v", got)
	}
	if got := extractTickTS([]byte(`{"tick_ts":"not-a-time"}`)); !got.IsZero() {
		t.Fatalf("expected zero time for bad tick_ts, got %v", got)
	}
}

func TestBroadcast_RecordsLatencyOnlyForTicks(t *testing.T) {
	h := &Hub{
		clients:     map[*Client]bool{},
		channelSeqs: map[string]int64{},
		latest:      map[string]latestEntry{},
		replayBufs:  map[string]*ReplayBuffer{},
		Latency:     NewLatencyTracker(100),
	}
	b := NewBroadcaster(h)

	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	b.Broadcast("pub:candle:3600s:NSE:99926000", []byte(`{"ts":"`+old+`"}`))
	if n := h.Latency.Count(); n != 0 {
		t.Fatalf("candle broadcast recorded %d latency samples, want 0", n)
	}

	tick := model.Tick{Token: "99926000", Exchange: "NSE", TickTS: time.Now().UTC()}.JSON()
	b.Broadcast("pub:tick:NSE:99926000", tick)
	if n := h.Latency.Count(); n != 1 {
		t.Fatalf("tick broadcast recorded %d latency samples, want 1", n)
	}
	if p50, _, _ := h.Latency.Percentiles(); p50 > 1000 {
		t.Fatalf("tick latency %vms, want well under 1s", p50)
	}
}
