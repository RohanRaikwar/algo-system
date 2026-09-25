package orderexec

import (
	"errors"
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

func exitTestExecutor(rlMax int) *OrderExecutor {
	return NewOrderExecutor(Config{
		LiveOrders:    false,
		Qty:           1,
		CallFNOToken:  "12345",
		CallFNOSymbol: "NIFTY_CE",
		PutFNOToken:   "12346",
		PutFNOSymbol:  "NIFTY_PE",
		FNOExchange:   "NFO",
		LogPrefix:     "[test]",
		RLMaxOrders:   rlMax,
		RLWindow:      time.Minute,
	})
}

func sig(action strategy.Action) strategy.Signal {
	return strategy.Signal{StrategyName: "test_strategy", Action: action, Side: strategy.SideCall, Token: "99926000"}
}

func inCall(oe *OrderExecutor) bool {
	return oe.sideOpen(strategy.SideCall)
}

func TestExit_NotBlockedByRateLimiter(t *testing.T) {
	oe := exitTestExecutor(1)
	oe.ExecuteSignal(sig(strategy.ActionBuy)) // consumes the only slot
	if !inCall(oe) {
		t.Fatal("setup: BUY should open CALL position")
	}
	oe.ExecuteSignal(sig(strategy.ActionExit))
	if inCall(oe) {
		t.Fatal("EXIT was rate limited — exits must never be blocked")
	}
}

func TestEntry_StillRateLimited(t *testing.T) {
	f := &fakeAPI{autoStatus: "complete", autoAvg: 100.0}
	oe := liveExecutor(t, f)
	oe.rl = NewRateLimiter(1, time.Minute)
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	oe.ExecuteSignal(realSig(strategy.ActionExit))
	oe.ExecuteSignal(realSig(strategy.ActionBuy))
	if n := f.placedCount(); n != 2 {
		t.Fatalf("placed %d, want BUY+SELL only — second real BUY must be rate limited", n)
	}
}

func TestExit_NotBlockedByOpenCircuit(t *testing.T) {
	oe := exitTestExecutor(10)
	oe.ExecuteSignal(sig(strategy.ActionBuy))
	for i := 0; i < 5; i++ {
		_ = oe.cb.Execute(func() error { return errors.New("broker down") })
	}
	oe.ExecuteSignal(sig(strategy.ActionExit))
	if inCall(oe) {
		t.Fatal("EXIT blocked by open circuit breaker — exits must never be blocked")
	}
}
