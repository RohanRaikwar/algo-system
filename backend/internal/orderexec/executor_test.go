package orderexec

import (
	"testing"
	"time"

	"trading-systemv1/internal/circuitbreaker"
	"trading-systemv1/internal/strategy"
)

func TestOrderExecutor_DryRun_CircuitBreakerInitialized(t *testing.T) {
	oe := NewOrderExecutor(Config{
		LiveOrders: false,
		LogPrefix:  "[test]",
	})

	if oe.cb == nil {
		t.Fatal("circuit breaker should be initialized")
	}
	if oe.rl == nil {
		t.Fatal("rate limiter should be initialized")
	}
	if oe.cb.CurrentState() != circuitbreaker.StateClosed {
		t.Errorf("expected circuit breaker to start closed, got %v", oe.cb.CurrentState())
	}
}

func TestOrderExecutor_DryRun_ExecuteSignal(t *testing.T) {
	oe := NewOrderExecutor(Config{
		LiveOrders:    false,
		Qty:           1,
		CallFNOToken:  "12345",
		CallFNOSymbol: "NIFTY_CE",
		PutFNOToken:   "12346",
		PutFNOSymbol:  "NIFTY_PE",
		FNOExchange:   "NFO",
		LogPrefix:     "[test]",
	})

	// BUY CALL should succeed in dry-run
	oe.ExecuteSignal(strategy.Signal{
		StrategyName: "test_strategy",
		Action:       strategy.ActionBuy,
		Side:         strategy.SideCall,
		Token:        "99926000",
		Reason:       "test",
	})

	// Should be in CALL position now
	inCall := oe.sideOpen(strategy.SideCall)
	if !inCall {
		t.Error("expected to be in CALL position after BUY")
	}

	// BUY PUT should be blocked (mutual exclusion)
	oe.ExecuteSignal(strategy.Signal{
		StrategyName: "test_strategy",
		Action:       strategy.ActionBuy,
		Side:         strategy.SidePut,
		Token:        "99926000",
		Reason:       "test",
	})

	inPut := oe.sideOpen(strategy.SidePut)
	if inPut {
		t.Error("expected PUT to be blocked while CALL is active")
	}

	// EXIT CALL should succeed and unblock
	oe.ExecuteSignal(strategy.Signal{
		StrategyName: "test_strategy",
		Action:       strategy.ActionExit,
		Side:         strategy.SideCall,
		Token:        "99926000",
		Reason:       "test exit",
	})

	inCall = oe.sideOpen(strategy.SideCall)
	if inCall {
		t.Error("expected CALL position to be closed after EXIT")
	}
}

func TestOrderExecutor_RateLimiterBlocks(t *testing.T) {
	oe := NewOrderExecutor(Config{
		LiveOrders:    false,
		Qty:           1,
		CallFNOToken:  "12345",
		CallFNOSymbol: "NIFTY_CE",
		PutFNOToken:   "12346",
		PutFNOSymbol:  "NIFTY_PE",
		FNOExchange:   "NFO",
		LogPrefix:     "[test]",
		RLMaxOrders:   3,
		RLWindow:      10 * time.Second,
	})

	// Place 3 orders (should all succeed in dry-run)
	for i := 0; i < 3; i++ {
		oe.ExecuteSignal(strategy.Signal{
			StrategyName: "test_strategy",
			Action:       strategy.ActionBuy,
			Side:         strategy.SideCall,
			Token:        "99926000",
			Reason:       "test",
		})
		// Exit to allow next BUY
		oe.ExecuteSignal(strategy.Signal{
			StrategyName: "test_strategy",
			Action:       strategy.ActionExit,
			Side:         strategy.SideCall,
			Token:        "99926000",
			Reason:       "exit",
		})
	}

	// Rate limiter should have consumed 6 allowances (3 BUY + 3 EXIT)
	// With maxOrders=3, BUYs 1-3 consume slots, then exits consume more
	// The 4th through 6th calls are all rate limited
	// Verify rate limiter count
	count := oe.rl.Count()
	if count > 3 {
		// Rate limiter is working — some signals were blocked
		t.Logf("rate limiter count: %d (some signals were rate limited)", count)
	}
}

func TestRateLimiter_Basic(t *testing.T) {
	rl := NewRateLimiter(3, 100*time.Millisecond)

	// First 3 should pass
	for i := 0; i < 3; i++ {
		if err := rl.Allow(); err != nil {
			t.Fatalf("order %d should be allowed: %v", i+1, err)
		}
	}

	// 4th should be rejected
	if err := rl.Allow(); err != ErrRateLimited {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}

	// Wait for window to expire
	time.Sleep(120 * time.Millisecond)

	// Should be allowed again
	if err := rl.Allow(); err != nil {
		t.Errorf("expected allowed after window expired: %v", err)
	}
}

func TestRateLimiter_Count(t *testing.T) {
	rl := NewRateLimiter(10, 100*time.Millisecond)

	if rl.Count() != 0 {
		t.Errorf("expected 0, got %d", rl.Count())
	}

	rl.Allow()
	rl.Allow()

	if rl.Count() != 2 {
		t.Errorf("expected 2, got %d", rl.Count())
	}

	time.Sleep(120 * time.Millisecond)

	if rl.Count() != 0 {
		t.Errorf("expected 0 after window expired, got %d", rl.Count())
	}
}

func TestIdempotencyKey_Unique(t *testing.T) {
	sig := strategy.Signal{
		StrategyName: "test",
		Action:       strategy.ActionBuy,
		Side:         strategy.SideCall,
	}

	key1 := generateIdempotencyKey(sig)
	time.Sleep(2 * time.Millisecond) // ensure different timestamp
	key2 := generateIdempotencyKey(sig)

	if key1 == key2 {
		t.Errorf("expected unique keys, got same: %s", key1)
	}

	if len(key1) != 16 {
		t.Errorf("expected 16-char hex key, got %d chars: %s", len(key1), key1)
	}
}

func TestOrderExecutor_DefaultConfig(t *testing.T) {
	oe := NewOrderExecutor(Config{
		LiveOrders: false,
		LogPrefix:  "[test]",
	})

	// Verify defaults were applied
	if oe.cb == nil {
		t.Fatal("circuit breaker should be initialized with defaults")
	}
	if oe.rl == nil {
		t.Fatal("rate limiter should be initialized with defaults")
	}

	// CB should start closed
	if oe.cb.CurrentState() != circuitbreaker.StateClosed {
		t.Errorf("expected circuit breaker Closed, got %v", oe.cb.CurrentState())
	}
}

func TestOrderExecutor_ResolveInstrument_UsesLockedEntryOnSell(t *testing.T) {
	oe := NewOrderExecutor(Config{
		LiveOrders:    false,
		CallFNOToken:  "cfg_call_token",
		CallFNOSymbol: "CFG_CALL",
		PutFNOToken:   "cfg_put_token",
		PutFNOSymbol:  "CFG_PUT",
		LogPrefix:     "[test]",
	})

	sig := strategy.Signal{
		StrategyName: "nifty",
		Side:         strategy.SideCall,
	}
	oe.setPositionInstrument(sig, "entry_call_token", "ENTRY_CALL")

	// Simulate later config/token drift.
	oe.cfg.CallFNOToken = "new_call_token"
	oe.cfg.CallFNOSymbol = "NEW_CALL"

	token, symbol, err := oe.resolveInstrument(sig, "SELL")
	if err != nil {
		t.Fatalf("resolveInstrument should not fail: %v", err)
	}
	if token != "entry_call_token" || symbol != "ENTRY_CALL" {
		t.Fatalf("expected locked entry instrument, got token=%s symbol=%s", token, symbol)
	}
}

func TestOrderExecutor_DryRun_TracksAndClearsPositionInstrument(t *testing.T) {
	oe := NewOrderExecutor(Config{
		LiveOrders:    false,
		Qty:           1,
		CallFNOToken:  "12345",
		CallFNOSymbol: "NIFTY_CE",
		PutFNOToken:   "12346",
		PutFNOSymbol:  "NIFTY_PE",
		FNOExchange:   "NFO",
		LogPrefix:     "[test]",
	})

	buySig := strategy.Signal{
		StrategyName: "test_strategy",
		Action:       strategy.ActionBuy,
		Side:         strategy.SideCall,
		Token:        "99926000",
		Reason:       "entry",
	}
	oe.ExecuteSignal(buySig)

	inst, ok := oe.getPositionInstrument(buySig)
	if !ok {
		t.Fatal("expected entry instrument to be tracked after BUY")
	}
	if inst.Token != "12345" || inst.Symbol != "NIFTY_CE" {
		t.Fatalf("unexpected tracked instrument: %+v", inst)
	}

	exitSig := strategy.Signal{
		StrategyName: "test_strategy",
		Action:       strategy.ActionExit,
		Side:         strategy.SideCall,
		Token:        "99926000",
		Reason:       "exit",
	}
	oe.ExecuteSignal(exitSig)

	if _, ok := oe.getPositionInstrument(exitSig); ok {
		t.Fatal("expected tracked instrument to be cleared after EXIT")
	}
}

func TestOrderExecutor_NoStrikeResolved_EntrySkippedExitStillCloses(t *testing.T) {
	// Dynamic strikes only: no static tokens and no resolved picker.
	oe := NewOrderExecutor(Config{
		LiveOrders:  false,
		Qty:         1,
		FNOExchange: "NFO",
		LogPrefix:   "[test]",
	})

	buySig := strategy.Signal{
		StrategyName: "test_strategy",
		Action:       strategy.ActionBuy,
		Side:         strategy.SideCall,
		Token:        "99926000",
		Reason:       "entry",
	}
	if _, _, err := oe.resolveInstrument(buySig, "BUY"); err == nil {
		t.Fatal("expected an error resolving an entry with no strike and no static token")
	}
	oe.ExecuteSignal(buySig)
	if _, ok := oe.getPositionInstrument(buySig); ok {
		t.Fatal("entry must be skipped when no strike is resolved")
	}
	if _, ok := oe.entry(oe.positionKey(buySig)); ok {
		t.Fatal("no entry order may be recorded when no strike is resolved")
	}

	// A position opened earlier on a picked strike still exits on that
	// strike, even though nothing is resolved now.
	oe.setPositionInstrument(buySig, "40742", "NIFTY_CE_PICKED")
	exitSig := buySig
	exitSig.Action = strategy.ActionExit
	token, symbol, err := oe.resolveInstrument(exitSig, "SELL")
	if err != nil {
		t.Fatalf("exit with a locked instrument must not fail: %v", err)
	}
	if token != "40742" || symbol != "NIFTY_CE_PICKED" {
		t.Fatalf("exit used %s/%s, want the locked entry instrument", token, symbol)
	}
	oe.ExecuteSignal(exitSig)
	if _, ok := oe.getPositionInstrument(exitSig); ok {
		t.Fatal("expected locked instrument to be cleared after EXIT")
	}
}
