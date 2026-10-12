package orderexec

import (
	"strings"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// risingQuotes is an ask that moves up one tick per read.
type risingQuotes struct {
	mu       sync.Mutex
	bid, ask int64
}

func (q *risingQuotes) BidAsk(string, time.Time) (int64, int64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	bid, ask := q.bid, q.ask
	q.bid -= 5
	q.ask += 5
	return bid, ask, true
}

func chaseExecutor(t *testing.T, f *fakeAPI) *OrderExecutor {
	t.Helper()
	oe := liveExecutor(t, f)
	oe.cfg.ExecMode = ExecChase
	oe.cfg.ChaseReprices = 3
	oe.cfg.ChaseMaxBps = 300
	old := chasePollDelays
	chasePollDelays = []time.Duration{0}
	t.Cleanup(func() { chasePollDelays = old })
	oe.UpdateLTP(model.Tick{Token: "111", Price: 10000})
	return oe
}

func TestChase_EntryIsMarketableLimit(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.10}
	oe := chaseExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	p := f.placedAt(0)
	if p["ordertype"] != "LIMIT" || p["price"] != "100.10" { // LTP 10000 + 2 ticks
		t.Fatalf("entry %v, want LIMIT @ 100.10", p)
	}
	if len(f.modifies()) != 0 {
		t.Fatal("filled order was re-priced")
	}
}

func TestChase_ReplacesUntilFilled(t *testing.T) {
	f := &fakeAPI{autoStatus: "open", fillAfterModifies: 2}
	oe := chaseExecutor(t, f)
	oe.SetQuoteSource(&risingQuotes{bid: 9990, ask: 10000})
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	m := f.modifies()
	if len(m) != 2 {
		t.Fatalf("re-priced %d times, want 2", len(m))
	}
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || e.Pending || e.Quantity != 75 {
		t.Fatalf("entry %+v ok=%v, want filled 75", e, ok)
	}
	if e.Price != 10020 { // ask reads 10000, 10005, 10010 (+2 ticks): filled at the 2nd re-price
		t.Fatalf("fill price %d, want 10020", e.Price)
	}
}

func TestChase_UnfilledEntryCancelled(t *testing.T) {
	f := &fakeAPI{autoStatus: "open"}
	oe := chaseExecutor(t, f)
	oe.SetQuoteSource(&risingQuotes{bid: 9990, ask: 10000})
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := len(f.modifies()); n != 3 {
		t.Fatalf("re-priced %d times, want ChaseReprices=3", n)
	}
	if !strings.Contains(strings.Join(f.calls(), ","), "cancel:OID-1") {
		t.Fatalf("entry not cancelled: %v", f.calls())
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("cancelled unfilled entry left a position")
	}
}

func TestChase_PartialEntryKept(t *testing.T) {
	f := &fakeAPI{autoStatus: "open", autoFilled: "25", autoAvg: 100.10}
	oe := chaseExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || e.Quantity != 25 {
		t.Fatalf("entry %+v ok=%v, want the 25 that filled", e, ok)
	}
}

// A runaway price is not chased past ChaseMaxBps.
func TestChase_EntryCapStopsChasing(t *testing.T) {
	f := &fakeAPI{autoStatus: "open"}
	oe := chaseExecutor(t, f)
	oe.cfg.ChaseMaxBps = 10 // 0.1% of ~100 = 10 paise: second re-price is over
	oe.SetQuoteSource(&risingQuotes{bid: 9990, ask: 10000})
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := len(f.modifies()); n != 2 {
		t.Fatalf("re-priced %d times, want 2 before the cap", n)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("capped entry left a position")
	}
}

// An exit that will not fill as LIMIT ends as MARKET.
func TestChase_ExitEndsAsMarket(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := chaseExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.autoStatus = "open"
	f.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	m := f.modifies()
	if len(m) == 0 || m[len(m)-1]["ordertype"] != "MARKET" {
		t.Fatalf("modifies %v, want last one MARKET", m)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("position open after MARKET exit filled")
	}
}

func TestChase_NoPrice(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := chaseExecutor(t, f)
	oe.mu.Lock()
	delete(oe.ltp, "111")
	oe.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if f.placedCount() != 0 {
		t.Fatal("BUY sent with no price")
	}
	// An exit with no price still goes out, as MARKET.
	oe.cfg.ExecMode = ExecMarket
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.cfg.ExecMode = ExecChase
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if p := f.placedAt(f.placedCount() - 1); p["transactiontype"] != "SELL" || p["ordertype"] != "MARKET" {
		t.Fatalf("exit %v, want MARKET SELL", p)
	}
}

// Stop protection and chase together: the protective stop is cancelled
// before the chased exit.
func TestChase_WithStop(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.10}
	oe := stopExecutor(t, f)
	oe.cfg.ExecMode, oe.cfg.ChaseReprices = ExecChase, 3
	old := chasePollDelays
	chasePollDelays = []time.Duration{0}
	t.Cleanup(func() { chasePollDelays = old })
	oe.UpdateLTP(model.Tick{Token: "111", Price: 10000})
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	got := strings.Join(f.calls(), ",")
	if !strings.Contains(got, "place:NORMAL:BUY,place:STOPLOSS:SELL,cancel:OID-2,place:NORMAL:SELL") {
		t.Fatalf("calls %s", got)
	}
}
