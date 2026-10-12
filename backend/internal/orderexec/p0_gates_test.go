package orderexec

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trading-systemv1/internal/circuitbreaker"
	"trading-systemv1/internal/strategy"
)

func srSig(action strategy.Action) strategy.Signal {
	s := realSig(action)
	s.StrategyName = "NIFTY50_SR"
	return s
}

// A strategy outside the live allowlist stays paper even with a live session.
func TestLiveAllowlist_UnlistedStrategyStaysPaper(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.ExecuteSignal(srSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d real orders for unlisted NIFTY50_SR, want 0", n)
	}
	if e, ok := entryFor(oe, srSig(strategy.ActionBuy)); !ok || e.Real {
		t.Fatalf("want a paper entry, got %+v ok=%v", e, ok)
	}
}

func TestLiveAllowlist_ListedStrategyGoesReal(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.liveStrategies = map[string]bool{"NIFTY50_SR": true}
	if !oe.IsRealStrategy("NIFTY50_SR") || oe.IsRealStrategy("NIFTY50_FNO") {
		t.Fatal("IsRealStrategy does not follow the allowlist")
	}
	oe.ExecuteSignal(srSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("placed %d, want 1 real BUY", n)
	}
}

// Real entry size is lots × the contract's lot size, not STRAT_QTY.
func TestRealEntryQty_LotsTimesLotSize(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.cfg.Qty = 1
	oe.cfg.LiveLots = 2
	oe.lotSizeOf = func(string) (int64, error) { return 65, nil }
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("placed %d, want 1", n)
	}
	if q := f.placedAt(0)["quantity"]; q != "130" {
		t.Fatalf("quantity %v, want 130 (2 lots × 65)", q)
	}
}

func TestRealEntryQty_UnknownLotSizeRefused(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	alerts := alertSink(oe)
	oe.lotSizeOf = func(string) (int64, error) { return 0, errors.New("instrument master not loaded") }
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d with unknown lot size, want 0", n)
	}
	if _, ok := entryFor(oe, realSig(strategy.ActionBuy)); ok {
		t.Fatal("entry recorded for a refused BUY")
	}
	if !strings.Contains(alerts(), "lot size unknown") {
		t.Fatalf("no lot-size alert: %q", alerts())
	}
	// The refusal must not leave the position reserved.
	oe.lotSizeOf = func(string) (int64, error) { return 75, nil }
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("placed %d after lot size became known, want 1", n)
	}
}

func TestRealEntryQty_AboveFreezeRefused(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.cfg.LiveLots = 30 // 30 × 75 = 2250 > 1800
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d above freeze qty, want 0", n)
	}
}

// Margin-style rejections prove the broker is up: they must not trip the
// breaker and lock out every later entry.
func TestBreaker_BusinessRejectionsDoNotTrip(t *testing.T) {
	reject := errors.New("place order failed: map[errorcode:AB4036 message:Insufficient funds]")
	f := &fakeAPI{placeErr: []error{reject, reject, reject, reject}}
	oe := liveExecutor(t, f)
	for i := 0; i < 4; i++ {
		oe.ExecuteSignal(realSig(strategy.ActionBuy))
	}
	if n := f.placedCount(); n != 4 {
		t.Fatalf("placed %d, want 4 (breaker must stay closed on rejections)", n)
	}
	if st := oe.cb.CurrentState(); st != circuitbreaker.StateClosed {
		t.Fatalf("breaker %v after business rejections, want closed", st)
	}
}

// An open breaker lets an entry probe again once its timeout passes.
func TestBreaker_OpenRecoversForEntries(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.cb = circuitbreaker.NewCircuitBreaker(1, 20*time.Millisecond)
	_ = oe.cb.Execute(func() error { return errNoSession })
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 0 {
		t.Fatalf("placed %d while breaker open, want 0", n)
	}
	time.Sleep(30 * time.Millisecond)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("placed %d after reset timeout, want 1 probe", n)
	}
	if st := oe.cb.CurrentState(); st != circuitbreaker.StateClosed {
		t.Fatalf("breaker %v after successful probe, want closed", st)
	}
}

type stubPnL struct{ v atomic.Int64 }

func (s *stubPnL) GetStrategyDailyPnL(string) int64 { return s.v.Load() }

// The day-loss limit refuses new real entries but never an exit.
func TestDayLoss_BlocksEntryNotExit(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.cfg.MaxDayLossPaise = 200000
	pnl := &stubPnL{}
	oe.SetPnLTracker(pnl)

	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 1 {
		t.Fatalf("placed %d, want BUY", n)
	}
	pnl.v.Store(-200000)
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want the exit to go through at the loss limit", n)
	}
	alerts := alertSink(oe)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want new BUY refused at the loss limit", n)
	}
	if !strings.Contains(alerts(), "DAY LOSS LIMIT") {
		t.Fatalf("no day-loss alert: %q", alerts())
	}
}

// With no target configured only the stop-loss GTT is placed, so no
// sibling rule is left live after the stop fires.
func TestGTT_NoTargetByDefault(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.cfg.GTTTargetBps = 0
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	e, ok := entryFor(oe, realSig(strategy.ActionBuy))
	if !ok {
		t.Fatal("no entry")
	}
	if e.GttRuleID != "" || e.GttSLRuleID == "" {
		t.Fatalf("want SL GTT only, got target=%q sl=%q", e.GttRuleID, e.GttSLRuleID)
	}
	f.mu.Lock()
	made := len(f.gttMade)
	f.mu.Unlock()
	if made != 1 {
		t.Fatalf("created %d GTT rules, want 1", made)
	}
}

func TestIsBrokerFault(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{errNoSession, true},
		{ErrOrderStateUnknown, true},
		{errors.New("TokenException: Invalid Token"), true},
		{errors.New("order+refresh failed: x"), true},
		{errors.New("place order failed: Insufficient funds"), false},
		{errors.New("place order failed: Invalid quantity"), false},
	} {
		if got := isBrokerFault(c.err); got != c.want {
			t.Errorf("isBrokerFault(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
