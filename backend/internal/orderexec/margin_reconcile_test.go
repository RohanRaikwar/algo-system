package orderexec

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

func TestMargin_ShortRefusesBuy(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0, rmsNet: "1000.00"}
	oe := liveExecutor(t, f)
	oe.cfg.MarginBufferPct = 10
	oe.UpdateLTP(model.Tick{Token: "111", Price: 10000}) // 75 × ₹100 × 1.1 = ₹8250 > ₹1000
	alerts := alertSink(oe)
	if err := oe.RefreshMargin(); err != nil {
		t.Fatal(err)
	}
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if f.placedCount() != 0 {
		t.Fatal("BUY sent without the margin for it")
	}
	if !strings.Contains(alerts(), "available margin 1000.00") {
		t.Fatalf("no margin alert: %q", alerts())
	}
}

func TestMargin_EnoughAllowsBuy(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0, rmsNet: "50000.00"}
	oe := liveExecutor(t, f)
	oe.cfg.MarginBufferPct = 10
	oe.UpdateLTP(model.Tick{Token: "111", Price: 10000})
	_ = oe.RefreshMargin()
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if f.placedCount() != 1 {
		t.Fatal("BUY refused with enough margin")
	}
}

// Unknown or stale margin never blocks: the broker checks anyway.
func TestMargin_UnknownOrStaleAllowsBuy(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0, rmsNet: "1.00"}
	oe := liveExecutor(t, f)
	oe.cfg.MarginBufferPct = 10
	oe.UpdateLTP(model.Tick{Token: "111", Price: 10000})
	oe.ExecuteSignal(realSig(strategy.ActionBuy)) // never refreshed
	if f.placedCount() != 1 {
		t.Fatal("BUY refused with unknown margin")
	}
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	_ = oe.RefreshMargin()
	oe.mu.Lock()
	oe.margin.at = time.Now().Add(-time.Hour)
	oe.mu.Unlock()
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if f.placedCount() != 3 {
		t.Fatal("BUY refused with stale margin")
	}
}

func TestMargin_StatusFalseKeepsCache(t *testing.T) {
	f := &fakeAPI{rmsNet: "500.00"}
	oe := liveExecutor(t, f)
	_ = oe.RefreshMargin()
	f.mu.Lock()
	f.rmsNet = "abc"
	f.mu.Unlock()
	if err := oe.RefreshMargin(); err == nil {
		t.Fatal("unparseable margin accepted")
	}
	if oe.margin.paise != 50000 {
		t.Fatalf("cache %d, want the last good 50000", oe.margin.paise)
	}
}

func setBrokerQty(f *fakeAPI, token string, qty string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.positions = []map[string]any{{"symboltoken": token, "tradingsymbol": "NIFTY_CE", "netqty": qty}}
}

// Broker flat on two passes in a row: the position is closed and its stop
// cancelled. One pass alone only waits.
func TestReconcileFix_FlatTwiceCloses(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	setBrokerQty(f, "111", "0")

	res, err := oe.ReconcileAndFix()
	if err != nil || len(res.Waiting) != 1 || len(res.Fixed) != 0 {
		t.Fatalf("first pass %+v err=%v, want waiting", res, err)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); !ok {
		t.Fatal("closed on a single reading")
	}
	res, _ = oe.ReconcileAndFix()
	if len(res.Fixed) != 1 {
		t.Fatalf("second pass %+v, want fixed", res)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("position still tracked after broker flat twice")
	}
	if !strings.Contains(strings.Join(f.calls(), ","), "cancel:"+e.SLOrderID) {
		t.Fatalf("stop not cancelled: %v", f.calls())
	}
}

// A bad read between good ones never drops the position.
func TestReconcileFix_TransientReadIgnored(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	setBrokerQty(f, "111", "0")
	_, _ = oe.ReconcileAndFix()
	setBrokerQty(f, "111", "75")
	_, _ = oe.ReconcileAndFix()
	setBrokerQty(f, "111", "0")
	res, _ := oe.ReconcileAndFix()
	if len(res.Fixed) != 0 || len(res.Waiting) != 1 {
		t.Fatalf("pass %+v, want waiting again (readings not consecutive)", res)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); !ok {
		t.Fatal("position dropped on non-consecutive readings")
	}
}

func TestReconcileFix_PartialReducesAndResizesStop(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := stopExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	setBrokerQty(f, "111", "25")
	_, _ = oe.ReconcileAndFix()
	res, _ := oe.ReconcileAndFix()
	if len(res.Fixed) != 1 {
		t.Fatalf("pass %+v, want fixed", res)
	}
	e, _ := entryFor(oe, realSig(strategy.ActionBuy))
	stops := f.placedStops()
	if e.Quantity != 25 || len(stops) != 2 || stops[1]["quantity"] != "25" {
		t.Fatalf("entry qty %d, stops %v — want 25 and a new stop for 25", e.Quantity, stops)
	}
}

func TestReconcileFix_MoreOrUnknownIsUnsafe(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	f.mu.Lock()
	f.positions = []map[string]any{
		{"symboltoken": "111", "tradingsymbol": "NIFTY_CE", "netqty": "150"},
		{"symboltoken": "999", "tradingsymbol": "OTHER", "netqty": "50"},
	}
	f.mu.Unlock()
	for i := 0; i < 2; i++ {
		res, _ := oe.ReconcileAndFix()
		if len(res.Unsafe) != 2 || len(res.Fixed) != 0 {
			t.Fatalf("pass %d %+v, want 2 unsafe", i, res)
		}
	}
	if e, _ := entryFor(oe, realSig(strategy.ActionBuy)); e.Quantity != 75 {
		t.Fatalf("entry changed to %d on unsafe drift", e.Quantity)
	}
}

func TestReconcile_StatusFalseIsError(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.api = statusFalsePositions{f}
	if _, err := oe.ReconcileAndFix(); err == nil {
		t.Fatal("status=false positions read as flat")
	}
}

type statusFalsePositions struct{ *fakeAPI }

func (statusFalsePositions) Position() (map[string]any, error) {
	return map[string]any{"status": false, "message": "Something Went Wrong"}, nil
}
