package ws

import (
	"errors"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

func TestParseTick_ExchangeTimeIsEventTS(t *testing.T) {
	recv := time.Date(2026, 9, 23, 4, 0, 0, 500_000_000, time.UTC)
	exMs := recv.Add(-40*time.Millisecond).UnixNano() / int64(time.Millisecond)

	tick, err := parseTick(map[string]interface{}{
		"token":              "26000",
		"exchange_type":      1,
		"last_traded_price":  int64(2500000),
		"exchange_timestamp": exMs,
	}, recv)
	if err != nil {
		t.Fatalf("parseTick: %v", err)
	}

	wantEvent := time.Unix(0, exMs*int64(time.Millisecond)).UTC()
	if !tick.EventTS.Equal(wantEvent) {
		t.Errorf("EventTS = %v, want exchange time %v", tick.EventTS, wantEvent)
	}
	if !tick.TickTS.Equal(recv) {
		t.Errorf("TickTS = %v, want local receive time %v", tick.TickTS, recv)
	}
	if !tick.CanonicalTS().Equal(wantEvent) {
		t.Errorf("CanonicalTS = %v, want exchange time %v", tick.CanonicalTS(), wantEvent)
	}
}

func TestParseTick_NoExchangeTime(t *testing.T) {
	recv := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	tick, err := parseTick(map[string]interface{}{
		"token":             "26000",
		"exchange_type":     1,
		"last_traded_price": int64(2500000),
	}, recv)
	if err != nil {
		t.Fatalf("parseTick: %v", err)
	}
	if !tick.EventTS.IsZero() {
		t.Errorf("EventTS = %v, want zero", tick.EventTS)
	}
	if !tick.TickTS.Equal(recv) {
		t.Errorf("TickTS = %v, want %v", tick.TickTS, recv)
	}
}

func TestSequenceGap(t *testing.T) {
	cases := []struct {
		name      string
		last, cur int64
		want      int64
	}{
		{"first observation", 0, 57, 0},
		{"contiguous", 10, 11, 0},
		{"one missing", 10, 12, 1},
		{"many missing", 10, 20, 9},
		{"duplicate", 10, 10, 0},
		{"backwards/reset", 10, 3, 0},
	}
	for _, tc := range cases {
		if got := sequenceGap(tc.last, tc.cur); got != tc.want {
			t.Errorf("%s: sequenceGap(%d,%d) = %d, want %d", tc.name, tc.last, tc.cur, got, tc.want)
		}
	}
}

func TestSeqTracker_PerTokenAndReset(t *testing.T) {
	tr := newSeqTracker()
	if g := tr.observe("A", 5); g != 0 {
		t.Fatalf("first A gap = %d", g)
	}
	if g := tr.observe("B", 100); g != 0 {
		t.Fatalf("first B gap = %d", g)
	}
	if g := tr.observe("A", 6); g != 0 {
		t.Fatalf("A 5->6 gap = %d", g)
	}
	if g := tr.observe("A", 9); g != 2 {
		t.Fatalf("A 6->9 gap = %d, want 2", g)
	}
	if g := tr.observe("B", 101); g != 0 {
		t.Fatalf("B 100->101 gap = %d", g)
	}

	tr.reset()
	if g := tr.observe("A", 1); g != 0 {
		t.Fatalf("after reset gap = %d, want 0", g)
	}
	if g := tr.observe("A", 2); g != 0 {
		t.Fatalf("after reset 1->2 gap = %d", g)
	}
}

func TestSanitizeEventTS_ClampsImplausibleExchangeTime(t *testing.T) {
	recv := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		event   time.Time
		clamped bool
	}{
		{"within skew", recv.Add(-maxEventSkew + time.Millisecond), false},
		{"far future", recv.Add(maxEventSkew + time.Second), true},
		{"far past", recv.Add(-time.Hour), true},
		{"missing", time.Time{}, false},
	}
	for _, tc := range cases {
		tick := model.Tick{TickTS: recv, EventTS: tc.event}
		if got := sanitizeEventTS(&tick); got != tc.clamped {
			t.Errorf("%s: clamped = %v, want %v", tc.name, got, tc.clamped)
		}
		switch {
		case tc.clamped && !tick.EventTS.Equal(recv):
			t.Errorf("%s: EventTS = %v, want recv %v", tc.name, tick.EventTS, recv)
		case !tc.clamped && !tick.EventTS.Equal(tc.event):
			t.Errorf("%s: EventTS changed to %v", tc.name, tick.EventTS)
		}
	}
}

func newWiredIngest(t *testing.T, tickCh chan model.Tick) *Ingest {
	t.Helper()
	ing, err := New(IngestConfig{AuthToken: "a", APIKey: "k", ClientCode: "c", FeedToken: "f"})
	if err != nil {
		t.Fatal(err)
	}
	ing.wire(tickCh)
	return ing
}

func TestOnData_FutureExchangeTimeIsClampedAndCounted(t *testing.T) {
	tickCh := make(chan model.Tick, 1)
	ing := newWiredIngest(t, tickCh)
	skews := 0
	ing.OnClockSkew = func() { skews++ }

	future := time.Now().Add(time.Hour).UnixMilli()
	ing.ws.OnData(map[string]interface{}{"token": "26000", "exchange_type": 1, "last_traded_price": int64(1), "exchange_timestamp": future})

	got := <-tickCh
	if skews != 1 {
		t.Fatalf("clock skew count = %d, want 1", skews)
	}
	if !got.EventTS.Equal(got.TickTS) {
		t.Fatalf("EventTS = %v, want recv time %v", got.EventTS, got.TickTS)
	}
}

func TestWire_ConnStateFollowsSocket(t *testing.T) {
	ing := newWiredIngest(t, make(chan model.Tick, 1))
	var states []bool
	ing.OnConnState = func(up bool) { states = append(states, up) }

	ing.ws.OnClose()
	if len(states) != 1 || states[0] {
		t.Fatalf("states after close = %v, want [false]", states)
	}
}

func TestWire_FatalSocketErrorEndsSession(t *testing.T) {
	ing := newWiredIngest(t, make(chan model.Tick, 1))

	ing.ws.OnError("Max retry attempt reached", "still retrying") // warning only
	select {
	case <-ing.fatal:
		t.Fatal("max-retry warning must not end the session")
	default:
	}

	ing.ws.OnError(smartconnect.ErrCodeAuthRejected, "401")
	select {
	case err := <-ing.fatal:
		if !errors.Is(err, ErrFeedLost) {
			t.Fatalf("fatal err = %v, want ErrFeedLost", err)
		}
	default:
		t.Fatal("auth rejection did not end the session")
	}
}

// A future-dated exchange time within maxEventSkew is still clamped to
// recv + maxFutureSkew so one token cannot run ahead of real time.
func TestSanitizeEventTS_FutureWithinSkewClampedToReorderTolerance(t *testing.T) {
	recv := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	tick := model.Tick{TickTS: recv, EventTS: recv.Add(3 * time.Second)}
	if !sanitizeEventTS(&tick) {
		t.Fatal("future-dated EventTS not reported as clamped")
	}
	if want := recv.Add(maxFutureSkew); !tick.EventTS.Equal(want) {
		t.Fatalf("EventTS = %v, want recv+maxFutureSkew %v", tick.EventTS, want)
	}

	ok := model.Tick{TickTS: recv, EventTS: recv.Add(maxFutureSkew)}
	if sanitizeEventTS(&ok) || !ok.EventTS.Equal(recv.Add(maxFutureSkew)) {
		t.Fatalf("EventTS at the tolerance changed: %v", ok.EventTS)
	}
}

// ForceReconnect (stale-feed watchdog) ends the session with ErrFeedLost so
// the session loop re-logins and builds a new socket.
func TestForceReconnect_EndsSessionWithFeedLost(t *testing.T) {
	ing := newWiredIngest(t, make(chan model.Tick, 1))
	ing.ForceReconnect("stale feed")
	select {
	case err := <-ing.fatal:
		if !errors.Is(err, ErrFeedLost) {
			t.Fatalf("err = %v, want ErrFeedLost", err)
		}
	default:
		t.Fatal("ForceReconnect did not end the session")
	}
	ing.ForceReconnect("again") // must not block when already pending
}
