package wssim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"trading-systemv1/internal/model"
)

func TestHandleMessage_SourceTimeIsEventTSAndLocalTimeIsTickTS(t *testing.T) {
	ing := &Ingest{}
	var seen []model.Tick
	ing.OnIngested = func(tick *model.Tick) { seen = append(seen, *tick) }

	src := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	recv := src.Add(15 * time.Millisecond)
	tickCh := make(chan model.Tick, 1)

	ing.handleMessage([]byte(`{"token":"2885","exchange":"NSE","price":185005000,"qty":10,"tick_ts":"`+
		src.Format(time.RFC3339Nano)+`"}`), recv, tickCh)

	got := <-tickCh
	if !got.EventTS.Equal(src) {
		t.Fatalf("EventTS = %v, want sim source time %v", got.EventTS, src)
	}
	if !got.TickTS.Equal(recv) {
		t.Fatalf("TickTS = %v, want local receive time %v", got.TickTS, recv)
	}
	if len(seen) != 1 || !seen[0].EventTS.Equal(src) || !seen[0].TickTS.Equal(recv) {
		t.Fatalf("OnIngested saw %+v, want one tick with EventTS=src TickTS=recv", seen)
	}
}

func TestHandleMessage_ExplicitEventTSWins(t *testing.T) {
	ing := &Ingest{}
	ev := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	recv := ev.Add(time.Second)
	tickCh := make(chan model.Tick, 1)

	ing.handleMessage([]byte(`{"token":"1","price":1,"tick_ts":"`+ev.Add(500*time.Millisecond).Format(time.RFC3339Nano)+
		`","event_ts":"`+ev.Format(time.RFC3339Nano)+`"}`), recv, tickCh)

	got := <-tickCh
	if !got.EventTS.Equal(ev) || !got.TickTS.Equal(recv) {
		t.Fatalf("EventTS=%v TickTS=%v, want %v / %v", got.EventTS, got.TickTS, ev, recv)
	}
}

func TestHandleMessage_DropAndInvalidMessages(t *testing.T) {
	ing := &Ingest{}
	ingested, drops := 0, 0
	ing.OnIngested = func(*model.Tick) { ingested++ }
	ing.OnDrop = func() { drops++ }
	tickCh := make(chan model.Tick, 1)
	now := time.Now().UTC()

	ing.handleMessage([]byte(`not json`), now, tickCh)
	ing.handleMessage([]byte(`{"token":""}`), now, tickCh)
	ing.handleMessage([]byte(`{"token":"A"}`), now, tickCh)
	ing.handleMessage([]byte(`{"token":"A"}`), now, tickCh) // channel full

	if ingested != 2 {
		t.Fatalf("ingested = %d, want 2 (invalid messages not counted)", ingested)
	}
	if drops != 1 {
		t.Fatalf("drops = %d, want 1", drops)
	}
	if got := <-tickCh; !got.EventTS.IsZero() {
		t.Fatalf("EventTS = %v, want zero when sim sent no source time", got.EventTS)
	}
}

func TestHandleMessage_ImplausibleSourceTimeClampedToRecv(t *testing.T) {
	ing := &Ingest{}
	skews := 0
	ing.OnClockSkew = func() { skews++ }
	recv := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	tickCh := make(chan model.Tick, 2)

	ing.handleMessage([]byte(`{"token":"1","price":1,"tick_ts":"`+recv.Add(maxEventSkew+time.Second).Format(time.RFC3339Nano)+`"}`), recv, tickCh)
	ing.handleMessage([]byte(`{"token":"1","price":1,"tick_ts":"`+recv.Add(-time.Second).Format(time.RFC3339Nano)+`"}`), recv, tickCh)

	if got := <-tickCh; !got.EventTS.Equal(recv) {
		t.Fatalf("future EventTS = %v, want clamped to recv %v", got.EventTS, recv)
	}
	if got := <-tickCh; !got.EventTS.Equal(recv.Add(-time.Second)) {
		t.Fatalf("plausible EventTS changed: %v", got.EventTS)
	}
	if skews != 1 {
		t.Fatalf("clock skew count = %d, want 1", skews)
	}
}

func TestRunOnce_ReportsConnState(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c.Close() // drop immediately
	}))
	defer srv.Close()

	ing, _ := New(Config{URL: "ws" + strings.TrimPrefix(srv.URL, "http")})
	var states []bool
	ing.OnConnState = func(c bool) { states = append(states, c) }

	if err := ing.runOnce(context.Background(), make(chan model.Tick, 1)); err == nil {
		t.Fatal("expected disconnect error")
	}
	if len(states) != 2 || !states[0] || states[1] {
		t.Fatalf("conn states = %v, want [true false]", states)
	}
}

func TestHandleMessage_FutureWithinSkewClampedToReorderTolerance(t *testing.T) {
	ing := &Ingest{}
	skews := 0
	ing.OnClockSkew = func() { skews++ }
	recv := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	tickCh := make(chan model.Tick, 1)

	ing.handleMessage([]byte(`{"token":"1","price":1,"tick_ts":"`+recv.Add(3*time.Second).Format(time.RFC3339Nano)+`"}`), recv, tickCh)

	if got := <-tickCh; !got.EventTS.Equal(recv.Add(maxFutureSkew)) {
		t.Fatalf("EventTS = %v, want recv+maxFutureSkew %v", got.EventTS, recv.Add(maxFutureSkew))
	}
	if skews != 1 {
		t.Fatalf("clock skew count = %d, want 1", skews)
	}
}
