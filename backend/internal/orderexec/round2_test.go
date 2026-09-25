package orderexec

import (
	"strings"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

// Partial fill then cancel: the filled part is a live position.
func TestFillResult_CancelledWithFillsIsNotRejected(t *testing.T) {
	book := bookWith(map[string]any{"orderid": "O1", "orderstatus": "cancelled", "filledshares": "25", "averageprice": 100.0})
	f, err := confirmFill(func() (map[string]any, error) { return book, nil }, "O1", []time.Duration{0})
	if err != nil {
		t.Fatal(err)
	}
	if f.Rejected() || f.FilledQty != 25 || !f.terminal() {
		t.Fatalf("want terminal partial fill of 25, got %+v rejected=%v", f, f.Rejected())
	}
}

func TestRealBuy_PartialFillThenCancel_TracksFilledQty(t *testing.T) {
	f := &fakeAPI{autoStatus: "cancelled", autoAvg: 100.0, autoFilled: "25"}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok || e.Quantity != 25 {
		t.Fatalf("want entry of 25 filled, got %+v ok=%v", e, ok)
	}
}

func TestGTTPrices_RoundedToTick(t *testing.T) {
	if got := gttTargetPaise(10003, 5); got != 10150 { // 10003*1.015=10153.04 → floor to 5 → 10150
		t.Fatalf("target %d, want 10150", got)
	}
	trig, lim := gttStopLossPaise(10003, 1000, 5)
	if trig != 9005 || lim != 8955 { // 9003 → ceil 9005; limit 50 below → 8955
		t.Fatalf("stop trigger/limit %d/%d, want 9005/8955", trig, lim)
	}
	if trig%5 != 0 || lim%5 != 0 {
		t.Fatal("stop prices must be tick multiples")
	}
}

// Order-book reads are spaced to respect the broker rate limit, and never
// return data fetched before the call began.
func TestBookReader_SpacesCallsAndNeverServesStale(t *testing.T) {
	calls := 0
	var mu sync.Mutex
	r := newBookReader(func() (map[string]any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return bookWith(), nil
	}, 30*time.Millisecond)

	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := r.get(); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 60*time.Millisecond {
		t.Fatalf("3 reads took %s, want ≥60ms spacing", el)
	}
	if calls != 3 {
		t.Fatalf("each sequential read must fetch fresh, got %d fetches", calls)
	}
}

func TestAlerts_ReachAlerter(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	var got []string
	var mu sync.Mutex
	oe.SetAlerter(func(msg string) { mu.Lock(); got = append(got, msg); mu.Unlock() })
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.mu.Lock()
	oe.sessionOK = false
	oe.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionExit)) // real position, no session → alert
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "close manually") {
		t.Fatalf("operator alert not delivered, got %v", got)
	}
}
