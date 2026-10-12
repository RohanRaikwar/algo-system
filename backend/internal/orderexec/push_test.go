package orderexec

import (
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

// A terminal push already cached ends confirmFill without reading the book.
func TestPush_TerminalSkipsBookRead(t *testing.T) {
	var p pushBook
	p.put("O1", fillResult{Status: "complete", FilledQty: 75, AvgPricePaise: 10125})
	reads := 0
	book := func() (map[string]any, error) { reads++; return map[string]any{"status": true}, nil }
	f, err := confirmFill(book, &p, "O1", []time.Duration{time.Hour})
	if err != nil || f.Status != "complete" || f.AvgPricePaise != 10125 {
		t.Fatalf("got %+v %v", f, err)
	}
	if reads != 0 {
		t.Fatalf("read the book %d times, want 0", reads)
	}
}

// A push arriving mid-wait wakes the waiter at once.
func TestPush_WakesWaiter(t *testing.T) {
	var p pushBook
	done := make(chan fillResult)
	go func() {
		f, _ := confirmFill(func() (map[string]any, error) { return map[string]any{"status": true}, nil },
			&p, "O1", []time.Duration{time.Hour})
		done <- f
	}()
	time.Sleep(5 * time.Millisecond)
	p.put("O1", fillResult{Status: "rejected", Text: "margin"})
	select {
	case f := <-done:
		if f.Status != "rejected" {
			t.Fatalf("status %q", f.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not woken by push")
	}
}

// Non-terminal pushes are never trusted: the book decides.
func TestPush_NonTerminalIgnored(t *testing.T) {
	f := &fakeAPI{}
	oe := liveExecutor(t, f)
	oe.HandleOrderUpdate(map[string]any{"orderid": "O1", "orderstatus": "open"})
	if _, ok := oe.pushes.terminal("O1"); ok {
		t.Fatal("non-terminal push cached")
	}
	f.setRow(map[string]any{"orderid": "O1", "orderstatus": "open"})
	got, err := confirmFill(oe.readOrderBook, &oe.pushes, "O1", []time.Duration{0})
	if err != nil || got.Status != "open" {
		t.Fatalf("got %+v %v, want book status open", got, err)
	}
}

// The first terminal status wins; a later push cannot rewrite it.
func TestPush_FirstTerminalWins(t *testing.T) {
	var p pushBook
	if !p.put("O1", fillResult{Status: "complete"}) {
		t.Fatal("first put not stored")
	}
	if p.put("O1", fillResult{Status: "cancelled"}) {
		t.Fatal("second put stored")
	}
	if f, _ := p.terminal("O1"); f.Status != "complete" {
		t.Fatalf("status %q, want complete", f.Status)
	}
}

// A stop fill pushed by the socket closes the position at once, from the
// push alone (the REST book still shows the stop resting), with no SELL.
func TestPush_StopFillClosesPosition(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	var mu sync.Mutex
	var fills []FillReport
	oe.SetFillListener(func(r FillReport) { mu.Lock(); fills = append(fills, r); mu.Unlock() })
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	before := len(f.calls())

	oe.HandleOrderUpdate(map[string]any{"orderid": e.SLOrderID, "orderstatus": "complete", "averageprice": 89.80, "filledshares": "75"})

	waitFor(t, "position closed", func() bool { _, ok := entryFor(oe, realSig(strategy.ActionBuy)); return !ok })
	if c := f.calls(); len(c) != before {
		t.Fatalf("broker calls after push: %v", c[before:])
	}
	mu.Lock()
	defer mu.Unlock()
	if last := fills[len(fills)-1]; last.Direction != "SELL" || last.FillPricePaise != 8980 || last.Qty != 75 {
		t.Fatalf("exit fill %+v, want SELL 75 @ 8980", last)
	}
}

// A stop cancelled after a partial fill books the part sold and protects
// the rest with a new stop.
func TestPush_StopPartialCancelRearms(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))

	oe.HandleOrderUpdate(map[string]any{"orderid": e.SLOrderID, "orderstatus": "cancelled", "averageprice": 89.80, "filledshares": "25"})

	waitFor(t, "stop re-placed", func() bool {
		r, ok := entryFor(oe, realSig(strategy.ActionBuy))
		return ok && r.SLOrderID != "" && r.SLOrderID != e.SLOrderID
	})
	r, _ := entryFor(oe, realSig(strategy.ActionBuy))
	if r.Quantity != 50 {
		t.Fatalf("quantity %d, want 50", r.Quantity)
	}
	if stops := f.placedStops(); len(stops) != 2 || stops[1]["quantity"] != "50" {
		t.Fatalf("stops %v, want a second stop for 50", stops)
	}
}

// A push for an order that is not the position's current stop (an old stop,
// or an unknown order) changes nothing.
func TestPush_OtherOrderLeavesPosition(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	before := len(f.calls())

	oe.HandleOrderUpdate(map[string]any{"orderid": "SOMEONE-ELSE", "orderstatus": "complete", "averageprice": 50.0, "filledshares": "75"})
	oe.sweepStop(oe.positionKey(realSig(strategy.ActionBuy)), "OLD-STOP")

	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); !ok {
		t.Fatal("position closed by an unrelated push")
	}
	if c := f.calls(); len(c) != before {
		t.Fatalf("broker calls after push: %v", c[before:])
	}
}

// A background settler waiting on an order is settled by a push without
// waiting for its next poll.
func TestPush_SettlesBackgroundPoll(t *testing.T) {
	f := &fakeAPI{}
	oe := liveExecutor(t, f)
	asyncPollEvery = time.Hour // only the push can settle it inside the test
	got := make(chan fillResult, 1)
	oe.pollOrderAsync("k", "SELL O9", "O9", matchByID("O9"), func(_ string, r fillResult) { got <- r }, nil)
	time.Sleep(5 * time.Millisecond)
	oe.HandleOrderUpdate(map[string]any{"orderid": "O9", "orderstatus": "complete", "averageprice": 120.5, "filledshares": "75"})
	select {
	case r := <-got:
		if r.Status != "complete" || r.AvgPricePaise != 12050 {
			t.Fatalf("settled %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("push did not settle the background poll")
	}
}

// With no pushes at all, chase/confirm behave exactly as before: the
// book is read and decides.
func TestPush_NoSocketUsesBook(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); !ok {
		t.Fatal("entry not stored")
	}
	f.mu.Lock()
	reads := f.bookReads
	f.mu.Unlock()
	if reads == 0 {
		t.Fatal("book never read without pushes")
	}
}

// An entry fill pushed before the inline confirm reads the book is taken
// from the push: no order-book read is needed to store the position.
func TestPush_EntryFillFromPush(t *testing.T) {
	f := &fakeAPI{}
	oe := liveExecutor(t, f)
	f.placeGate = make(chan struct{})
	go func() {
		// PlaceOrder hands out OID-1; push its fill before the confirm runs.
		oe.HandleOrderUpdate(map[string]any{"orderid": "OID-1", "orderstatus": "complete", "averageprice": 100.0, "filledshares": "75"})
		close(f.placeGate)
	}()
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || e.Pending || e.Price != 10000 {
		t.Fatalf("entry %+v ok=%v, want filled at 10000", e, ok)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bookReads != 0 {
		t.Fatalf("book read %d times, want 0", f.bookReads)
	}
}
