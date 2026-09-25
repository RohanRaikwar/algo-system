package stratengine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func istAt(h, m int) time.Time {
	return time.Date(2026, 9, 23, h, m, 0, 0, ist)
}

func TestEODCutoff_DefaultAndConfigured(t *testing.T) {
	svc := &Service{}
	if got := svc.eodCutoff(istAt(10, 0)); !got.Equal(istAt(15, 20)) {
		t.Fatalf("default cutoff %v, want 15:20 IST", got)
	}
	svc.cfg.EODExitTime = "15:10"
	if got := svc.eodCutoff(istAt(10, 0)); !got.Equal(istAt(15, 10)) {
		t.Fatalf("configured cutoff %v, want 15:10 IST", got)
	}
}

func TestPastEODCutoff(t *testing.T) {
	svc := &Service{cfg: Config{EODExitTime: "15:20"}}
	for _, c := range []struct {
		h, m int
		want bool
	}{{15, 19, false}, {15, 20, true}, {15, 25, true}, {9, 15, false}} {
		if got := svc.pastEODCutoff(istAt(c.h, c.m)); got != c.want {
			t.Errorf("pastEODCutoff(%02d:%02d) = %v, want %v", c.h, c.m, got, c.want)
		}
	}
}

func TestNextEODRun(t *testing.T) {
	svc := &Service{cfg: Config{EODExitTime: "15:20"}}
	if got := svc.nextEODRun(istAt(11, 0)); !got.Equal(istAt(15, 20)) {
		t.Errorf("before cutoff: %v, want today 15:20", got)
	}
	// Restart between cutoff and close: run now, don't skip today's exit.
	if got := svc.nextEODRun(istAt(15, 24)); !got.Equal(istAt(15, 24)) {
		t.Errorf("inside window: %v, want immediately", got)
	}
	if got := svc.nextEODRun(istAt(16, 0)); !got.Equal(istAt(15, 20).AddDate(0, 0, 1)) {
		t.Errorf("after close: %v, want tomorrow 15:20", got)
	}
}

func TestValidate_EODExitTime(t *testing.T) {
	base := Config{SubscribeTokenKeys: []string{"NSE:99926000"}, CallFNOToken: "111", PutFNOToken: "222"}
	for _, v := range []string{"15:30", "16:00", "3pm"} {
		c := base
		c.EODExitTime = v
		if err := c.Validate(); err == nil {
			t.Errorf("EODExitTime=%q accepted, want error", v)
		}
	}
	c := base
	c.EODExitTime = "15:20"
	if err := c.Validate(); err != nil {
		t.Errorf("EODExitTime=15:20 rejected: %v", err)
	}
}

// A position the executor holds but no strategy knows about (restored
// after a crash, or left by a failed exit) is still exited at EOD, and a
// position still open at the check raises a critical alert.
func TestRunEODExit_SweepsExecutorAndAlertsOnLeftovers(t *testing.T) {
	oldSweep, oldCheck := eodSweepDelay, eodCheckDelay
	eodSweepDelay, eodCheckDelay = time.Millisecond, 2*time.Millisecond
	defer func() { eodSweepDelay, eodCheckDelay = oldSweep, oldCheck }()

	oe := orderexec.NewOrderExecutor(orderexec.Config{
		Qty: 1, CallFNOToken: "111", CallFNOSymbol: "NIFTY_CE", FNOExchange: "NFO", LogPrefix: "[test]",
	})
	oe.ExecuteSignal(strategy.Signal{StrategyName: "NIFTY50_FNO", Action: strategy.ActionBuy, Side: strategy.SideCall, Token: "99926000", Exchange: "NSE"})

	n := &captureNotifier{}
	var mu sync.Mutex
	var dispatched []strategy.Signal
	svc := &Service{orderExecutor: oe, notifier: n, tfEngine: strategy.NewTFEngine(10)}
	// Capture instead of executing, so the position is still open at the check.
	svc.orderRunner = func(_ context.Context, s strategy.Signal) {
		mu.Lock()
		dispatched = append(dispatched, s)
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.runEODExit(ctx)

	waitUntil(t, "sweep exit dispatched", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(dispatched) == 1
	})
	mu.Lock()
	got := dispatched[0]
	mu.Unlock()
	if got.Action != strategy.ActionExit || got.StrategyName != "NIFTY50_FNO" || got.Side != strategy.SideCall || got.Token != "99926000" {
		t.Fatalf("sweep dispatched %+v, want EXIT NIFTY50_FNO CALL on index token", got)
	}
	waitUntil(t, "still-open alert", func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, a := range n.alerts {
			if strings.Contains(a.Message, "STILL OPEN") {
				return true
			}
		}
		return false
	})
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
