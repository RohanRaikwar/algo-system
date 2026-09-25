package orderexec

import (
	"strings"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

func paperSig(name string, action strategy.Action, side strategy.PositionSide) strategy.Signal {
	return strategy.Signal{StrategyName: name, Action: action, Side: side, Token: "99926000", Exchange: "NSE", Qty: 75}
}

func realSide(action strategy.Action, side strategy.PositionSide) strategy.Signal {
	s := realSig(action)
	s.Side = side
	return s
}

// A paper strategy's exit must not clear the side a real position holds.
func TestPaperExit_DoesNotUnblockRealOppositeBuy(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSide(strategy.ActionBuy, strategy.SideCall)) // real CALL open
	oe.ExecuteSignal(paperSig("PAPER_S", strategy.ActionBuy, strategy.SideCall))
	oe.ExecuteSignal(paperSig("PAPER_S", strategy.ActionExit, strategy.SideCall))
	oe.ExecuteSignal(realSide(strategy.ActionBuy, strategy.SidePut))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("real PUT BUY sent while real CALL open (placed %d)", n)
	}
}

// A paper position must not block the real strategy's entries.
func TestPaperPosition_DoesNotBlockRealEntry(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(paperSig("PAPER_S", strategy.ActionBuy, strategy.SideCall))
	oe.ExecuteSignal(realSide(strategy.ActionBuy, strategy.SidePut))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("real PUT BUY blocked by a paper CALL (placed %d)", n)
	}
}

// One strategy still cannot hold CALL and PUT at once.
func TestSameStrategy_OppositeSideBlocked(t *testing.T) {
	oe := exitTestExecutor(10)
	oe.ExecuteSignal(paperSig("PAPER_S", strategy.ActionBuy, strategy.SideCall))
	oe.ExecuteSignal(paperSig("PAPER_S", strategy.ActionBuy, strategy.SidePut))
	if _, ok := entryFor(oe, paperSig("PAPER_S", strategy.ActionBuy, strategy.SidePut)); ok {
		t.Fatal("same strategy opened PUT while holding CALL")
	}
}

// Paper signals never reach the broker, so they must not use up the real
// strategy's rate limit.
func TestPaperSignals_DoNotConsumeRateLimit(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.rl = NewRateLimiter(1, time.Minute)
	oe.ExecuteSignal(paperSig("PAPER_S", strategy.ActionBuy, strategy.SideCall))
	oe.ExecuteSignal(paperSig("PAPER_S", strategy.ActionExit, strategy.SideCall))
	oe.ExecuteSignal(realSide(strategy.ActionBuy, strategy.SideCall))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("real BUY rate-limited by paper signals (placed %d)", n)
	}
}

// Inline (order-path) book reads jump ahead of background pollers.
func TestBookReader_InlineReadsHavePriority(t *testing.T) {
	r := newBookReader(func() (map[string]any, error) { return bookWith(), nil }, 60*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = r.getBackground() }()
	}
	time.Sleep(5 * time.Millisecond)
	start := time.Now()
	if _, err := r.get(); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 150*time.Millisecond {
		t.Fatalf("inline read waited %s behind background pollers", el)
	}
	wg.Wait()
}

// A pending BUY restored on a later day can't be settled from the (day-
// scoped) order book: settle it from broker positions instead, with an alert.
func TestResume_StalePendingBuy_HeldAtBroker_KeptAndAlerted(t *testing.T) {
	f := &fakeAPI{positions: []map[string]any{{"symboltoken": "111", "tradingsymbol": "NIFTY_CE", "netqty": "75"}}}
	oe := liveExecutor(t, f)
	alerts := alertSink(oe)
	s := realSig(strategy.ActionBuy)
	oe.storeEntry(s, OrderRecord{ClientOrderID: "k1", Symbol: "NIFTY_CE", Token: "111", Quantity: 75, Real: true, Pending: true,
		Timestamp: time.Now().Add(-26 * time.Hour), StrategyName: s.StrategyName, PositionSide: string(s.Side)})
	oe.ResumeSettlement()
	e, ok := entryFor(oe, s)
	if !ok || e.Pending {
		t.Fatalf("stale pending BUY held at broker must be kept as open, got %+v ok=%v", e, ok)
	}
	if !strings.Contains(alerts(), "previous day") {
		t.Fatalf("want alert, got %q", alerts())
	}
}

func TestResume_StalePendingBuy_NotHeld_Dropped(t *testing.T) {
	f := &fakeAPI{}
	oe := liveExecutor(t, f)
	s := realSig(strategy.ActionBuy)
	oe.storeEntry(s, OrderRecord{ClientOrderID: "k1", Symbol: "NIFTY_CE", Token: "111", Quantity: 75, Real: true, Pending: true,
		Timestamp: time.Now().Add(-26 * time.Hour), StrategyName: s.StrategyName, PositionSide: string(s.Side)})
	oe.ResumeSettlement()
	if _, ok := entryFor(oe, s); ok {
		t.Fatal("stale pending BUY not held at broker must be dropped")
	}
}
