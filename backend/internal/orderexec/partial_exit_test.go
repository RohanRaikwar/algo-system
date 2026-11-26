package orderexec

import (
	"testing"

	"trading-systemv1/internal/model"
)

// A paper BUY sized in lots, a partial SELL of 2 lots, then the rest.
func TestPaperLotsAndPartialExit(t *testing.T) {
	oe := exitTestExecutor(10)
	oe.lotSizeOf = func(string) (int64, error) { return 65, nil }
	oe.UpdateLTP(model.Tick{Token: "12345", Price: 5000})
	var l fillLog
	oe.SetFillListener(l.add)

	buy := sig(model.ActionBuy)
	buy.Lots = 3
	oe.ExecuteSignal(buy)
	if e, ok := entryFor(oe, buy); !ok || e.Quantity != 195 {
		t.Fatalf("entry %+v %v, want 195", e, ok)
	}

	oe.UpdateLTP(model.Tick{Token: "12345", Price: 6000})
	part := sig(model.ActionExit)
	part.ExitLots = 2
	oe.ExecuteSignal(part)
	if e, ok := entryFor(oe, buy); !ok || e.Quantity != 65 || !inCall(oe) {
		t.Fatalf("after partial %+v %v", e, ok)
	}

	oe.ExecuteSignal(sig(model.ActionExit))
	if _, ok := entryFor(oe, buy); ok || inCall(oe) {
		t.Fatal("position open after the final exit")
	}
	got := l.get()
	if len(got) != 3 || got[0].Qty != 195 || got[1].Qty != 130 || !got[1].Partial || got[1].FillPricePaise != 6000 ||
		got[2].Qty != 65 || got[2].Partial {
		t.Fatalf("fills %+v", got)
	}
}

// ExitLots covering everything held is a full exit.
func TestPaperPartialExitOfAllIsFull(t *testing.T) {
	oe := exitTestExecutor(10)
	oe.lotSizeOf = func(string) (int64, error) { return 65, nil }
	oe.UpdateLTP(model.Tick{Token: "12345", Price: 5000})
	buy := sig(model.ActionBuy)
	buy.Lots = 2
	oe.ExecuteSignal(buy)
	part := sig(model.ActionExit)
	part.ExitLots = 2
	oe.ExecuteSignal(part)
	if _, ok := entryFor(oe, buy); ok || inCall(oe) {
		t.Fatal("position open after selling all lots")
	}
}

// A partial exit never reaches the broker for a real position: no SELL,
// the position and its protection stay whole.
func TestRealPartialExitNotSent(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	buy := realSig(model.ActionBuy)
	buy.Lots = 3 // ignored: real size is STRAT_LIVE_LOTS
	oe.ExecuteSignal(buy)
	waitFor(t, "entry GTTs", func() bool {
		e, _ := entryFor(oe, buy)
		return e.GttRuleID != "" && e.GttSLRuleID != ""
	})
	e0, _ := entryFor(oe, buy)
	placed := f.placedCount()

	part := realSig(model.ActionExit)
	part.ExitLots = 2
	oe.ExecuteSignal(part)

	if n := f.placedCount(); n != placed {
		t.Fatalf("partial exit placed %d order(s)", n-placed)
	}
	if len(f.cancelled()) != 0 || len(f.modifies()) != 0 {
		t.Fatalf("protection touched: cancelled %v modifies %v", f.cancelled(), f.modifies())
	}
	if e, ok := entryFor(oe, buy); !ok || e.Quantity != e0.Quantity || e.ExitOrderID != "" || e.ExitRequested {
		t.Fatalf("real entry changed: %+v", e)
	}
}
