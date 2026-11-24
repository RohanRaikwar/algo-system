package orderexec

import (
	"strings"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// restingSig is a real BUY for "above 123" capped at 124.
func restingSig() model.Signal {
	s := realSig(model.ActionBuy)
	s.EntryTriggerPaise, s.EntryLimitPaise = 12300, 12400
	return s
}

type drops struct {
	mu  sync.Mutex
	why []string
}

func (d *drops) listen(_ model.Signal, reason string) {
	d.mu.Lock()
	d.why = append(d.why, reason)
	d.mu.Unlock()
}

func (d *drops) n() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.why)
}

// Price still under the trigger: one STOPLOSS_LIMIT BUY at the exchange,
// the call returns without waiting, and the fill settles it in the
// background with the protective stop.
func TestResting_StopEntryBelowTrigger(t *testing.T) {
	f := &fakeAPI{}
	oe := stopExecutor(t, f)
	oe.UpdateLTP(model.Tick{Token: "111", Price: 12100})
	oe.ExecuteSignal(restingSig())

	if f.placedCount() != 1 {
		t.Fatalf("placed %d orders, want 1", f.placedCount())
	}
	p := f.placedAt(0)
	if p["variety"] != stopVariety || p["ordertype"] != "STOPLOSS_LIMIT" || p["transactiontype"] != "BUY" ||
		p["triggerprice"] != "123.00" || p["price"] != "124.00" {
		t.Fatalf("entry params %v", p)
	}
	e, ok := entryFor(oe, restingSig())
	if !ok || !e.Pending || !e.Resting || e.Variety != stopVariety || e.OrderID == "" {
		t.Fatalf("entry %+v, want pending resting STOPLOSS", e)
	}
	if len(f.modifies()) != 0 {
		t.Fatal("resting entry was re-priced")
	}

	f.setRow(map[string]any{"orderid": e.OrderID, "orderstatus": "complete", "averageprice": 123.05, "filledshares": "75"})
	waitFor(t, "fill settles", func() bool { r, ok := entryFor(oe, restingSig()); return ok && !r.Pending && r.SLOrderID != "" })
	r, _ := entryFor(oe, restingSig())
	if r.Price != 12305 || r.Resting {
		t.Fatalf("settled entry %+v", r)
	}
}

// Price already at or above the trigger: a plain LIMIT at the cap, never
// chased above it.
func TestResting_LimitAtCapAboveTrigger(t *testing.T) {
	f := &fakeAPI{}
	oe := stopExecutor(t, f)
	oe.cfg.ExecMode = ExecChase
	oe.UpdateLTP(model.Tick{Token: "111", Price: 12600})
	oe.ExecuteSignal(restingSig())
	p := f.placedAt(0)
	if p["variety"] != "NORMAL" || p["ordertype"] != "LIMIT" || p["price"] != "124.00" || p["triggerprice"] != nil {
		t.Fatalf("entry params %v", p)
	}
	time.Sleep(20 * time.Millisecond)
	if len(f.modifies()) != 0 {
		t.Fatalf("resting entry re-priced: %v", f.modifies())
	}
	endResting(t, f, oe, restingSig())
}

// endResting cancels a test's resting entry at the fake broker and waits
// for its background follower to settle it.
func endResting(t *testing.T, f *fakeAPI, oe *OrderExecutor, s model.Signal) {
	t.Helper()
	e, _ := entryFor(oe, s)
	f.setRow(map[string]any{"orderid": e.OrderID, "orderstatus": "cancelled"})
	waitFor(t, "resting entry settled", func() bool { _, ok := entryFor(oe, s); return !ok })
}

// An exit while the entry rests unfilled cancels it with its variety; no
// SELL is sent and the strategy hears the drop.
func TestResting_ExitCancelsUnfilledEntry(t *testing.T) {
	f := &fakeAPI{}
	oe := stopExecutor(t, f)
	var d drops
	oe.SetEntryDropListener(d.listen)
	oe.UpdateLTP(model.Tick{Token: "111", Price: 12100})
	oe.ExecuteSignal(restingSig())
	e, _ := entryFor(oe, restingSig())

	oe.ExecuteSignal(realSig(model.ActionExit))
	waitFor(t, "entry dropped", func() bool { _, ok := entryFor(oe, restingSig()); return !ok })
	want := []string{"place:STOPLOSS:BUY", "cancel:" + e.OrderID}
	if got := f.calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("broker calls %v, want %v", got, want)
	}
	waitFor(t, "drop heard", func() bool { return d.n() == 1 })
}

// Part filled before the exit's cancel: the filled part is adopted and sold.
func TestResting_PartialFillThenExitSellsPart(t *testing.T) {
	f := &fakeAPI{}
	oe := stopExecutor(t, f)
	oe.UpdateLTP(model.Tick{Token: "111", Price: 12350})
	s := restingSig()
	s.EntryTriggerPaise = 0 // plain LIMIT
	oe.ExecuteSignal(s)
	e, _ := entryFor(oe, s)
	f.setRow(map[string]any{"orderid": e.OrderID, "orderstatus": "open", "averageprice": 123.50, "filledshares": "25"})

	f.autoStatus, f.autoAvg = "complete", 125.00 // the SELL fills
	oe.ExecuteSignal(realSig(model.ActionExit))
	waitFor(t, "part sold", func() bool {
		for _, c := range f.calls() {
			if c == "place:NORMAL:SELL" {
				return true
			}
		}
		return false
	})
	f.mu.Lock()
	var sold map[string]any
	for _, p := range f.placed {
		if p["transactiontype"] == "SELL" && p["variety"] == "NORMAL" {
			sold = p
		}
	}
	f.mu.Unlock()
	if sold["quantity"] != "25" {
		t.Fatalf("sold %v, want the 25 filled", sold["quantity"])
	}
}

// A resting entry still unfilled when its window ends is cancelled.
func TestResting_ExpiresUnfilled(t *testing.T) {
	f := &fakeAPI{}
	oe := stopExecutor(t, f)
	var d drops
	oe.SetEntryDropListener(d.listen)
	oe.UpdateLTP(model.Tick{Token: "111", Price: 12100})
	s := restingSig()
	s.EntryExpiresAt = time.Now().Add(20 * time.Millisecond)
	oe.ExecuteSignal(s)
	waitFor(t, "expired entry dropped", func() bool { _, ok := entryFor(oe, s); return !ok && d.n() == 1 })
	n := 0
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "cancel:") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("calls %v, want one cancel", f.calls())
	}
}

// Paper: a price already above the cap is no fill; under the trigger the
// paper entry fills at the trigger.
func TestResting_Paper(t *testing.T) {
	oe := liveExecutor(t, &fakeAPI{})
	oe.mu.Lock()
	oe.live = false
	oe.mu.Unlock()
	var d drops
	oe.SetEntryDropListener(d.listen)
	var fills []int64
	oe.SetFillListener(func(r FillReport) { fills = append(fills, r.FillPricePaise) })

	oe.UpdateLTP(model.Tick{Token: "111", Price: 12600})
	oe.ExecuteSignal(restingSig())
	if _, ok := entryFor(oe, restingSig()); ok || d.n() != 1 {
		t.Fatalf("paper entry above cap: entry kept or no drop (%d)", d.n())
	}
	oe.UpdateLTP(model.Tick{Token: "111", Price: 12100})
	oe.ExecuteSignal(restingSig())
	if len(fills) != 1 || fills[0] != 12300 {
		t.Fatalf("paper fills %v, want [12300]", fills)
	}
}

// Exit in stop mode: the resting stop is modified into the exit — no
// cancel, no second SELL order.
func TestExit_ModifiesStop(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.cfg.ExitModifiesStop = true
	oe.ExecuteSignal(realSig(model.ActionBuy))
	e, _ := entryFor(oe, realSig(model.ActionBuy))
	oe.UpdateLTP(model.Tick{Token: "111", Price: 0})
	oe.ExecuteSignal(realSig(model.ActionExit))

	want := []string{"place:NORMAL:BUY", "place:STOPLOSS:SELL", "modify:MARKET:0"}
	if got := f.calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("broker calls %v, want %v", got, want)
	}
	m := f.modifies()[0]
	if m["orderid"] != e.SLOrderID || m["variety"] != stopVariety || m["triggerprice"] != "0" {
		t.Fatalf("modify %v", m)
	}
	if _, ok := entryFor(oe, realSig(model.ActionBuy)); ok {
		t.Fatal("position still open after exit")
	}
}

// The modified stop rests as a LIMIT at bid − ticks; a fill closes it.
func TestExit_ModifiesStopToLimit(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0, fillAfterModifies: 1}
	oe := stopExecutor(t, f)
	oe.cfg.ExitModifiesStop = true
	oe.ExecuteSignal(realSig(model.ActionBuy))
	oe.UpdateLTP(model.Tick{Token: "111", Price: 10500})
	oe.ExecuteSignal(realSig(model.ActionExit))
	m := f.modifies()
	if len(m) != 1 || m[0]["ordertype"] != "LIMIT" || m[0]["price"] != "104.90" {
		t.Fatalf("modifies %v, want one LIMIT at 104.90", m)
	}
	if _, ok := entryFor(oe, realSig(model.ActionBuy)); ok {
		t.Fatal("position still open after exit")
	}
}

// The stop already filled: the modify is refused, the old path reads the
// book and sells nothing.
func TestExit_ModifyRefusedFallsBack(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.cfg.ExitModifiesStop = true
	oe.ExecuteSignal(realSig(model.ActionBuy))
	e, _ := entryFor(oe, realSig(model.ActionBuy))
	f.setRow(map[string]any{"orderid": e.SLOrderID, "orderstatus": "complete", "averageprice": 89.80, "filledshares": "75"})
	oe.ExecuteSignal(realSig(model.ActionExit))
	for _, c := range f.calls() {
		if c == "place:NORMAL:SELL" {
			t.Fatalf("sold after the stop filled: %v", f.calls())
		}
	}
	if _, ok := entryFor(oe, realSig(model.ActionBuy)); ok {
		t.Fatal("position still open")
	}
}

// An Active entry (no trigger) is a NORMAL LIMIT at the cap even with the
// premium below the call's price: it buys now.
func TestResting_ActiveEntryLimitNow(t *testing.T) {
	f := &fakeAPI{}
	oe := stopExecutor(t, f)
	oe.UpdateLTP(model.Tick{Token: "111", Price: 11800})
	s := restingSig()
	s.EntryTriggerPaise = 0
	oe.ExecuteSignal(s)
	p := f.placedAt(0)
	if p["variety"] != "NORMAL" || p["ordertype"] != "LIMIT" || p["price"] != "124.00" || p["triggerprice"] != nil {
		t.Fatalf("entry params %v", p)
	}
	endResting(t, f, oe, s)
}
