package exitwatch

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// fakeBus is an in-memory bus: keys, streams, and a record of publishes.
// Subscriptions get a channel the test feeds through deliver.
type fakeBus struct {
	mu        sync.Mutex
	keys      map[string][]byte
	streams   map[string][]streamEntry
	published []busMsg
	subs      map[string]chan busMsg
}

func newFakeBus() *fakeBus {
	return &fakeBus{keys: map[string][]byte{}, streams: map[string][]streamEntry{}, subs: map[string]chan busMsg{}}
}

func (f *fakeBus) sub(name string) (<-chan busMsg, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan busMsg, 8)
	f.subs[name] = ch
	return ch, func() {}
}

func (f *fakeBus) Subscribe(_ context.Context, channels ...string) (<-chan busMsg, func()) {
	return f.sub(channels[0])
}

func (f *fakeBus) PSubscribe(_ context.Context, pattern string) (<-chan busMsg, func()) {
	return f.sub(pattern)
}

func (f *fakeBus) deliver(sub string, m busMsg) {
	f.mu.Lock()
	ch := f.subs[sub]
	f.mu.Unlock()
	ch <- m
}

func (f *fakeBus) Publish(_ context.Context, channel, payload string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, busMsg{Channel: channel, Payload: payload})
	return nil
}

func (f *fakeBus) Get(_ context.Context, key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.keys[key]
	return v, ok, nil
}

func (f *fakeBus) Set(_ context.Context, key string, val []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[key] = val
	return nil
}

// XRange returns the whole stream; tests read from the start.
func (f *fakeBus) XRange(_ context.Context, stream, _ string) ([]streamEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams[stream], nil
}

func busService(b bus) *Service {
	return &Service{
		bus:      b,
		posCh:    make(chan []model.PositionContext, 1),
		candleCh: make(chan model.TFCandle, 8),
	}
}

func TestBus_InitialPositionsFromKey(t *testing.T) {
	fb := newFakeBus()
	set := model.PositionContextSet{Positions: []model.PositionContext{{Strategy: "NIFTY50_SR", Side: "CALL", FNOToken: "45001"}}}
	b, _ := json.Marshal(set)
	fb.keys[posContextKey] = b
	svc := busService(fb)

	svc.loadInitialPositions(context.Background())
	select {
	case ps := <-svc.posCh:
		if len(ps) != 1 || ps[0].FNOToken != "45001" {
			t.Fatalf("positions = %+v", ps)
		}
	default:
		t.Fatal("no positions delivered")
	}

	// A missing key delivers nothing and does not block.
	svc2 := busService(newFakeBus())
	svc2.loadInitialPositions(context.Background())
	if len(svc2.posCh) != 0 {
		t.Fatal("missing key delivered positions")
	}
}

func TestBus_PositionUpdatesFromChannel(t *testing.T) {
	fb := newFakeBus()
	svc := busService(fb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.subscribePositions(ctx)

	b, _ := json.Marshal(model.PositionContextSet{})
	for {
		fb.mu.Lock()
		_, ready := fb.subs[posContextChannel]
		fb.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	fb.deliver(posContextChannel, busMsg{Channel: posContextChannel, Payload: string(b)})
	select {
	case ps := <-svc.posCh:
		if len(ps) != 0 {
			t.Fatalf("flat set delivered %d positions", len(ps))
		}
	case <-time.After(time.Second):
		t.Fatal("update not delivered")
	}
}

func TestBus_ExitRequestPublished(t *testing.T) {
	fb := newFakeBus()
	svc := busService(fb)
	svc.ExitRequest(model.ExitRequest{Strategy: "NIFTY50_SR", Side: "CALL", FNOToken: "45001", Reason: "test"})
	if len(fb.published) != 1 || fb.published[0].Channel != model.ExitRequestChannel {
		t.Fatalf("published = %+v", fb.published)
	}
	var r model.ExitRequest
	if err := json.Unmarshal([]byte(fb.published[0].Payload), &r); err != nil || r.FNOToken != "45001" {
		t.Fatalf("payload %q: %v", fb.published[0].Payload, err)
	}
}

// readCandles skips forming candles and continues after the last ID read.
func TestBus_ReadCandles(t *testing.T) {
	fb := newFakeBus()
	closed, _ := json.Marshal(model.TFCandle{Token: "99926000", TF: 60, Close: 2400000})
	forming, _ := json.Marshal(model.TFCandle{Token: "99926000", TF: 60, Forming: true})
	fb.streams["candle:60s:NSE:99926000"] = []streamEntry{{ID: "1-0", Data: string(closed)}, {ID: "2-0", Data: string(forming)}}
	svc := busService(fb)

	next := svc.readCandles(context.Background(), "NSE:99926000", "0-0")
	if next != "(2-0" {
		t.Fatalf("next = %q, want (2-0", next)
	}
	if len(svc.candleCh) != 1 {
		t.Fatalf("delivered %d candles, want 1 (forming skipped)", len(svc.candleCh))
	}
}
