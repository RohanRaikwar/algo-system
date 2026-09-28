package stratengine

import (
	"context"
	"errors"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

type fakeGreeks struct {
	chain []orderexec.OptionContract
	err   error
	calls int
}

func (f *fakeGreeks) LoadOptionChain(time.Time) ([]orderexec.OptionContract, error) {
	f.calls++
	return f.chain, f.err
}

var testExpiry = time.Date(2026, 10, 6, 0, 0, 0, 0, time.FixedZone("IST", 5*3600+30*60))

// symbolResolver returns the real symbol shape so the expiry can be read.
type symbolResolver struct{}

func (symbolResolver) ResolveStrike(_ time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	return orderexec.StrikeInfo{Token: opt + itoa(strike), Symbol: "NIFTY06OCT26" + itoa(strike) + opt, Strike: strike}, nil
}

func contract(strike int64, opt string, delta float64) orderexec.OptionContract {
	return orderexec.OptionContract{Strike: strike, OptionType: orderexec.OptionType(opt), Delta: delta, Expiry: testExpiry}
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
	return strategy.Signal{StrategyName: "NIFTY50_RANGE", Action: strategy.ActionBuy, Side: strategy.SideCall,
		Token: "99926000", Exchange: "NSE", Strike: strike}
}

func TestDeltaGuardAcceptsInBand(t *testing.T) {
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(24000, "CE", 0.52)}}
	svc := deltaSvc(g, 2400000, "CE24000")
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sig.FNOToken != "CE24000" {
		t.Fatalf("token = %s", sig.FNOToken)
	}
}

func TestDeltaGuardStepsTowardATMWhenTooFarOTM(t *testing.T) {
	// Wanted 1 OTM (24050, band 0.30-0.50) but delta 0.22 near expiry → move to 24000 (ATM band 0.40-0.60).
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(24050, "CE", 0.22), contract(24000, "CE", 0.48)}}
	svc := deltaSvc(g, 2400000, "CE24050", "CE24000")
	sig := buyCall(24050)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sig.Strike != 24000 || sig.FNOToken != "CE24000" {
		t.Fatalf("stepped to %d %s, want 24000", sig.Strike, sig.FNOToken)
	}
}

func TestDeltaGuardPutUsesAbsDeltaAndStepsUp(t *testing.T) {
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(23950, "PE", -0.20), contract(24000, "PE", -0.47)}}
	svc := deltaSvc(g, 2400000, "PE23950", "PE24000")
	sig := buyCall(23950)
	sig.Side = strategy.SidePut
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sig.Strike != 24000 {
		t.Fatalf("PUT stepped to %d, want 24000", sig.Strike)
	}
}

func TestDeltaGuardRefusesWhenStillOutOfBand(t *testing.T) {
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(24050, "CE", 0.22), contract(24000, "CE", 0.30)}}
	svc := deltaSvc(g, 2400000, "CE24050", "CE24000")
	sig := buyCall(24050)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatalf("entry accepted with delta out of band: %+v", sig)
	}
}

func TestDeltaGuardRefusesWithoutGreeks(t *testing.T) {
	g := &fakeGreeks{err: errors.New("option chain fetch requires SmartConnect session")}
	svc := deltaSvc(g, 2400000, "CE24000")
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err == nil {
		t.Fatal("no greeks must refuse, not guess")
	}
	g2 := &fakeGreeks{chain: []orderexec.OptionContract{contract(24100, "CE", 0.4)}}
	svc2 := deltaSvc(g2, 2400000, "CE24000")
	sig2 := buyCall(24000)
	if err := svc2.resolveEntryStrike(context.Background(), &sig2, time.Now()); err == nil {
		t.Fatal("strike missing from the chain must refuse")
	}
}

func TestDeltaGuardOffSkipsGreeks(t *testing.T) {
	g := &fakeGreeks{err: errors.New("down")}
	svc := deltaSvc(g, 2400000, "CE24000")
	svc.cfg.RangeDeltaGuard = false
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err != nil || g.calls != 0 {
		t.Fatalf("guard off: err=%v calls=%d", err, g.calls)
	}
}

func TestDeltaGuardCachesChain(t *testing.T) {
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(24000, "CE", 0.5)}}
	svc := deltaSvc(g, 2400000, "CE24000")
	now := time.Now()
	for i := 0; i < 3; i++ {
		sig := buyCall(24000)
		if err := svc.resolveEntryStrike(context.Background(), &sig, now); err != nil {
			t.Fatal(err)
		}
	}
	if g.calls != 1 {
		t.Fatalf("OptionGreek called %d times, want 1 within the cache window", g.calls)
	}
}

func TestDeltaBand(t *testing.T) {
	if lo, hi := deltaBand(0); lo != 0.40 || hi != 0.60 {
		t.Fatalf("ATM band %.2f-%.2f", lo, hi)
	}
	if lo, hi := deltaBand(1); lo < 0.2999 || lo > 0.3001 || hi < 0.4999 || hi > 0.5001 {
		t.Fatalf("OTM1 band %.2f-%.2f", lo, hi)
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
	g := &fakeGreeks{chain: []orderexec.OptionContract{
		{Strike: 24000, OptionType: "CE", Delta: 0.10, Expiry: next}, // next week: wrong contract
		contract(24000, "CE", 0.50),
	}}
	svc := deltaSvc(g, 2400000, "CE24000")
	sig := buyCall(24000)
	if err := svc.resolveEntryStrike(context.Background(), &sig, time.Now()); err != nil {
		t.Fatalf("matched the wrong expiry: %v", err)
	}
}

func richContract(strike int64, opt string, delta, iv, liq float64) orderexec.OptionContract {
	c := contract(strike, opt, delta)
	c.IV, c.LiquidityScore = iv, liq
	return c
}

func guardSvc(t *testing.T, c orderexec.OptionContract, ltp int64) *Service {
	t.Helper()
	g := &fakeGreeks{chain: []orderexec.OptionContract{c}}
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]", PaperSlippageBps: 50, PaperSlippageMinPaise: 50})
	oe.UpdateLTP(model.Tick{Token: "99926000", Exchange: "NSE", Price: 2400000})
	oe.UpdateLTP(model.Tick{Token: string(c.OptionType) + itoa(c.Strike), Exchange: "NFO", Price: ltp})
	svc := &Service{cfg: Config{RangeDeltaGuard: true, RangeMinLiquidity: 5000, RangeMaxBuyIV: 25, RangeCostMultiple: 3},
		orderExecutor: oe, legResolver: symbolResolver{}}
	svc.greeks = g
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

func TestBasketGuardChecksLegs(t *testing.T) {
	now := marketOpenTime(t)
	exp := testExpiry
	mk := func(strike int64, opt string, iv, liq float64) orderexec.OptionContract {
		return orderexec.OptionContract{Strike: strike, OptionType: orderexec.OptionType(opt), Delta: 0.2, IV: iv, LiquidityScore: liq, Expiry: exp}
	}
	chain := []orderexec.OptionContract{
		mk(24300, "CE", 12, 50000), mk(24200, "CE", 13, 50000), mk(23700, "PE", 12, 50000), mk(23800, "PE", 13, 50000),
	}
	run := func(chain []orderexec.OptionContract) string {
		basket := &fakeBasket{}
		oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
		svc := &Service{cfg: Config{EODExitTime: "15:20", RangeDeltaGuard: true, RangeMinLiquidity: 5000, RangeMinSellIV: 11},
			orderExecutor: oe, legResolver: symbolResolver{}}
		svc.greeks = &fakeGreeks{chain: chain}
		svc.testBasket = basket
		svc.legPriceWait = 10 * time.Millisecond
		svc.orderRunner = func(context.Context, strategy.Signal) {}
		svc.handleBasketSignal(context.Background(), condorBasket(strategy.ActionBuy), now)
		waitUntil(t, "basket outcome", func() bool { basket.mu.Lock(); defer basket.mu.Unlock(); return basket.cancelled != "" })
		return basket.cancelled
	}
	// Healthy chain: passes the guard, then cancels only for lack of premium ticks.
	if r := run(chain); r == "" || !contains(r, "no premium") {
		t.Fatalf("healthy chain cancelled for %q", r)
	}
	low := append([]orderexec.OptionContract(nil), chain...)
	low[1].LiquidityScore = 100
	if r := run(low); !contains(r, "liquidity") {
		t.Fatalf("illiquid leg: %q", r)
	}
	cheap := append([]orderexec.OptionContract(nil), chain...)
	cheap[1].IV, cheap[3].IV = 8, 9
	if r := run(cheap); !contains(r, "IV") {
		t.Fatalf("low IV condor: %q", r)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
