package stratengine

import (
	"context"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// ladderResolver maps strike+type to a deterministic token.
type ladderResolver struct {
	mu    sync.Mutex
	calls int
}

func (r *ladderResolver) ResolveStrike(_ time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	tok := opt + itoa(strike)
	return orderexec.StrikeInfo{Token: tok, Symbol: "NIFTY" + tok, Strike: strike}, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestNearestStrike(t *testing.T) {
	if got := nearestStrike(2398000, 50); got != 24000 {
		t.Fatalf("nearestStrike = %d", got)
	}
	if got := nearestStrike(2397000, 50); got != 23950 {
		t.Fatalf("nearestStrike = %d", got)
	}
}

func TestLadderTokensCoverATMPlusMinus(t *testing.T) {
	r := &ladderResolver{}
	svc := &Service{legResolver: r}
	toks := svc.ladderTokens(time.Now(), 2400000) // spot 24,000
	if len(toks) != (2*ladderStrikes+1)*2 {
		t.Fatalf("tokens = %d", len(toks))
	}
	want := map[string]bool{"CE24000": true, "PE24000": true, "CE24400": true, "PE23600": true}
	got := map[string]bool{}
	for _, tk := range toks {
		got[tk] = true
	}
	for tk := range want {
		if !got[tk] {
			t.Errorf("ladder missing %s", tk)
		}
	}
	// Already-subscribed strikes are not sent again.
	if again := svc.ladderTokens(time.Now(), 2400000); len(again) != 0 {
		t.Errorf("second ladder re-sent %d tokens", len(again))
	}
}

func TestRefreshLadderRecentersOnlyAfterMove(t *testing.T) {
	var mu sync.Mutex
	var sent [][]string
	svc := &Service{legResolver: &ladderResolver{}}
	svc.subscribeHook = func(tokens []string) { mu.Lock(); sent = append(sent, tokens); mu.Unlock() }
	ctx := context.Background()
	svc.refreshStrikeLadder(ctx, 2400000)
	waitUntil(t, "first ladder", func() bool { mu.Lock(); defer mu.Unlock(); return len(sent) == 1 })
	waitUntil(t, "ladder idle", func() bool { return !svc.ladderBusy.Load() })
	svc.refreshStrikeLadder(ctx, 2405000) // +50 pts: no recenter
	time.Sleep(20 * time.Millisecond)
	svc.refreshStrikeLadder(ctx, 2412000) // +120 pts: recenter, new strikes only
	waitUntil(t, "second ladder", func() bool { mu.Lock(); defer mu.Unlock(); return len(sent) == 2 })
	mu.Lock()
	defer mu.Unlock()
	if len(sent[1]) == 0 || len(sent[1]) >= len(sent[0]) {
		t.Fatalf("recenter sent %d tokens (first %d)", len(sent[1]), len(sent[0]))
	}
}

type fakeCanceller struct {
	side   strategy.PositionSide
	reason string
}

func (f *fakeCanceller) CancelEntry(side strategy.PositionSide, reason string) { f.side, f.reason = side, reason }

func TestResolveEntryStrikeUsesSignalStrike(t *testing.T) {
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
	oe.UpdateLTP(model.Tick{Token: "CE24050", Exchange: "NFO", Price: 9000})
	svc := &Service{orderExecutor: oe, legResolver: &ladderResolver{}}
	sig := strategy.Signal{StrategyName: "NIFTY50_RANGE", Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 24050}
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sig.FNOToken != "CE24050" || sig.FNOSymbol != "NIFTYCE24050" {
		t.Fatalf("sig = %+v", sig)
	}
	put := strategy.Signal{StrategyName: "NIFTY50_RANGE", Action: strategy.ActionBuy, Side: strategy.SidePut, Strike: 23950}
	if err := svc.resolveEntryStrike(context.Background(), &put, time.Now()); err == nil {
		t.Fatal("a strike with no premium yet must be refused, not bought blind")
	}
}

func TestBasketBelowMinCreditIsCancelled(t *testing.T) {
	now := marketOpenTime(t)
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
	basket := &fakeBasket{minCredit: 2000}
	var mu sync.Mutex
	var dispatched []strategy.Signal
	svc := &Service{cfg: Config{EODExitTime: "15:20"}, orderExecutor: oe, legResolver: fakeResolver{}}
	svc.orderRunner = func(_ context.Context, s strategy.Signal) { mu.Lock(); dispatched = append(dispatched, s); mu.Unlock() }
	svc.testBasket = basket
	sig := condorBasket(strategy.ActionBuy)
	legs, _ := svc.expandLegSignals(sig, now)
	// Shorts 27.50, longs 20.00 → credit 2×2750 − 2×2000 = 1500 paise < 2000 minimum.
	for _, l := range legs {
		p := int64(2000)
		if l.Short {
			p = 2750
		}
		oe.UpdateLTP(model.Tick{Token: l.FNOToken, Exchange: "NFO", Price: p})
	}
	svc.handleBasketSignal(context.Background(), sig, now)
	waitUntil(t, "basket cancelled", func() bool { basket.mu.Lock(); defer basket.mu.Unlock(); return basket.cancelled != "" })
	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != 0 {
		t.Fatalf("low-credit condor dispatched %d legs", len(dispatched))
	}
}

type failingResolver struct{ calls int }

func (f *failingResolver) ResolveStrike(time.Time, int64, string) (orderexec.StrikeInfo, error) {
	f.calls++
	return orderexec.StrikeInfo{}, errTest
}

var errTest = &testErr{}

type testErr struct{}

func (*testErr) Error() string { return "not found" }

func TestLadderStopsAfterRepeatedFailures(t *testing.T) {
	r := &failingResolver{}
	svc := &Service{legResolver: r}
	if toks := svc.ladderTokens(time.Now(), 2400000); len(toks) != 0 || r.calls != 3 {
		t.Fatalf("tokens=%d calls=%d, want 0 and 3", len(toks), r.calls)
	}
}
