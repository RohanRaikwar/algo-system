package stratengine

import (
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

// A restart mid-session must keep today's position; only a position from a
// previous day is exited at startup.
func TestClearStalePositions_KeepsSameDayPosition(t *testing.T) {
	newSvc := func(t *testing.T, tradeDay string) *Service {
		t.Helper()
		sr := strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())
		snap := `{"version":1,"strategy":"NIFTY50_SR","instruments":[` +
			`{"key":"NSE:99926000","side":"CALL","index_entry":2275618,"stop_level":2271533,` +
			`"target_level":2284859,"trade_day":"` + tradeDay + `"}]}`
		if err := sr.Restore([]byte(snap)); err != nil {
			t.Fatal(err)
		}
		return &Service{srStrategy: sr, tfEngine: strategy.NewTFEngine(10)}
	}
	// 30 Sep 12:50 IST: the redeploy that closed the 12:27 entry.
	now := time.Date(2026, 9, 30, 12, 50, 21, 0, ist)

	svc := newSvc(t, "2026-09-30")
	svc.clearStalePositionsAt(now)
	select {
	case sig := <-svc.tfEngine.Signals():
		t.Fatalf("same-day position exited on restart: %+v", sig)
	default:
	}

	svc = newSvc(t, "2026-09-29")
	svc.clearStalePositionsAt(now)
	select {
	case sig := <-svc.tfEngine.Signals():
		if sig.Action != strategy.ActionExit || sig.Side != strategy.SideCall {
			t.Fatalf("want CALL exit, got %+v", sig)
		}
	default:
		t.Fatal("previous-day position was not exited")
	}
}
