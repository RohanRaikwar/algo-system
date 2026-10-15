package stratengine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

type fakeGreeks struct {
	chain []instruments.OptionContract
	err   error

	mu    sync.Mutex // entryChain may refresh from a goroutine
	calls int
}

func (f *fakeGreeks) LoadOptionChain(time.Time) ([]instruments.OptionContract, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.chain, f.err
}

func (f *fakeGreeks) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// warmChain loads the chain off the signal path, as strikeSelLoop and the
// picker do in market hours; entries then read the cache.
func warmChain(t *testing.T, svc *Service, now time.Time) {
	t.Helper()
	if _, err := svc.optionChain(now); err != nil {
		t.Fatalf("warm chain: %v", err)
	}
}

var testExpiry = time.Date(2026, 10, 6, 0, 0, 0, 0, time.FixedZone("IST", 5*3600+30*60))

// symbolResolver returns the real symbol shape so the expiry can be read.
type symbolResolver struct{}

func (symbolResolver) ResolveStrike(_ time.Time, strike int64, opt string) (instruments.StrikeInfo, error) {
	return instruments.StrikeInfo{Token: opt + itoa(strike), Symbol: "NIFTY06OCT26" + itoa(strike) + opt, Strike: strike}, nil
}

func contract(strike int64, opt string, delta float64) instruments.OptionContract {
	return instruments.OptionContract{Strike: strike, OptionType: instruments.OptionType(opt), Delta: delta, Expiry: testExpiry}
}

func deltaSvc(g *fakeGreeks, spot int64, tokens ...string) *Service {
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
	oe.UpdateLTP(model.Tick{Token: "99926000", Exchange: "NSE", Price: spot})
	for _, tk := range tokens {
		oe.UpdateLTP(model.Tick{Token: tk, Exchange: "NFO", Price: 5000})
	}
	svc := &Service{cfg: Config{RangeDeltaGuard: true}, orderExecutor: oe, legResolver: symbolResolver{}}
	svc.greeks = g
	return svc
}

func buyCall(strike int64) strategy.Signal {
	return strategy.Signal{StrategyName: "TEST_STRAT", Action: strategy.ActionBuy, Side: strategy.SideCall,
		Token: "99926000", Exchange: "NSE", Strike: strike}
}

func TestDeltaGuardRefusesWithoutGreeks(t *testing.T) {
	g := &fakeGreeks{err: errors.New("option chain fetch requires SmartConnect session")}
	svc := deltaSvc(g, 2400000, "CE24000")
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatal("no greeks must refuse, not guess")
	}
	g2 := &fakeGreeks{chain: []instruments.OptionContract{contract(24100, "CE", 0.4)}}
	svc2 := deltaSvc(g2, 2400000, "CE24000")
	now := time.Now()
	warmChain(t, svc2, now)
	sig2 := buyCall(24000)
	if err := svc2.resolveEntryStrike(context.Background(), &sig2, now); err == nil {
		t.Fatal("strike missing from the chain must refuse")
	}
}

func TestDeltaGuardOffSkipsGreeks(t *testing.T) {
	g := &fakeGreeks{err: errors.New("down")}
	svc := deltaSvc(g, 2400000, "CE24000")
	svc.cfg.RangeDeltaGuard = false
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err != nil || g.n() != 0 {
		t.Fatalf("guard off: err=%v calls=%d", err, g.n())
	}
}

func TestDeltaGuardCachesChain(t *testing.T) {
	g := &fakeGreeks{chain: []instruments.OptionContract{contract(24000, "CE", 0.5)}}
	svc := deltaSvc(g, 2400000, "CE24000")
	now := time.Now()
	warmChain(t, svc, now)
	for i := 0; i < 3; i++ {
		sig := buyCall(24000)
		if err := svc.resolveEntryStrike(context.Background(), &sig, now); err != nil {
			t.Fatal(err)
		}
	}
	if g.n() != 1 {
		t.Fatalf("OptionGreek called %d times, want 1 within the cache window", g.n())
	}
}

func TestSymbolExpiry(t *testing.T) {
	got, ok := symbolExpiry("NIFTY06OCT2624200CE")
	if !ok || got.Format("2006-01-02") != "2026-10-06" {
		t.Fatalf("symbolExpiry = %v %v", got, ok)
	}
	if _, ok := symbolExpiry("BAD"); ok {
		t.Fatal("short symbol must fail")
	}
}

func TestDeltaGuardIgnoresOtherExpiry(t *testing.T) {
	next := testExpiry.AddDate(0, 0, 7)
	g := &fakeGreeks{chain: []instruments.OptionContract{
		{Strike: 24000, OptionType: "CE", Delta: 0.10, Expiry: next}, // next week: wrong contract
		contract(24000, "CE", 0.50),
	}}
	svc := deltaSvc(g, 2400000, "CE24000")
	now := time.Now()
	warmChain(t, svc, now)
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, now); err != nil {
		t.Fatalf("matched the wrong expiry: %v", err)
	}
}

func richContract(strike int64, opt string, delta, iv, liq float64) instruments.OptionContract {
	c := contract(strike, opt, delta)
	c.IV, c.LiquidityScore = iv, liq
	return c
}

func guardSvc(t *testing.T, c instruments.OptionContract, ltp int64) *Service {
	t.Helper()
	g := &fakeGreeks{chain: []instruments.OptionContract{c}}
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]", PaperSlippageBps: 50, PaperSlippageMinPaise: 50})
	oe.UpdateLTP(model.Tick{Token: "99926000", Exchange: "NSE", Price: 2400000})
	oe.UpdateLTP(model.Tick{Token: string(c.OptionType) + itoa(c.Strike), Exchange: "NFO", Price: ltp})
	svc := &Service{cfg: Config{RangeDeltaGuard: true, RangeMinLiquidity: 5000, RangeMaxBuyIV: 25, RangeCostMultiple: 3},
		orderExecutor: oe, legResolver: symbolResolver{}}
	svc.greeks = g
	warmChain(t, svc, time.Now())
	return svc
}

func TestGuardRefusesIlliquidContract(t *testing.T) {
	svc := guardSvc(t, richContract(24000, "CE", 0.5, 14, 1200), 10000)
	sig := buyCall(24000)
	sig.TargetMove = 5000
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatal("illiquid contract accepted")
	}
}

func TestGuardRefusesHighIVBuy(t *testing.T) {
	svc := guardSvc(t, richContract(24000, "CE", 0.5, 32, 90000), 10000)
	sig := buyCall(24000)
	sig.TargetMove = 5000
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatal("IV 32% buy accepted with max 25%")
	}
	// IV reported as a fraction (0.32) is normalised to 32%.
	svc2 := guardSvc(t, richContract(24000, "CE", 0.5, 0.32, 90000), 10000)
	sig2 := buyCall(24000)
	sig2.TargetMove = 5000
	if err := svc2.resolveEntryStrike(context.Background(), &sig2, time.Now()); err == nil {
		t.Fatal("fractional IV not normalised")
	}
}

func TestGuardCostCheck(t *testing.T) {
	// Premium ₹100 → slip ₹0.50 each way → round trip ₹1.00 = 100 paise; ×3 = 300 paise.
	// delta 0.5 × move 400 paise (4 pts) = 200 paise < 300 → refuse.
	svc := guardSvc(t, richContract(24000, "CE", 0.5, 14, 90000), 10000)
	sig := buyCall(24000)
	sig.TargetMove = 400
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatal("target too small for costs accepted")
	}
	// 40 pts move → 2000 paise expected gain → OK.
	sig2 := buyCall(24000)
	sig2.TargetMove = 4000
	if err := svc.resolveEntryStrike(context.Background(), &sig2, time.Now()); err != nil {
		t.Fatalf("healthy trade refused: %v", err)
	}
}

func TestNormIV(t *testing.T) {
	if normIV(0.15) != 15 || normIV(15) != 15 || normIV(0) != 0 {
		t.Fatal("normIV")
	}
}
