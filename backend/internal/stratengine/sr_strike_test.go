package stratengine

import (
	"context"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func srContract(strike int64, opt string, delta, gamma, theta, premium float64, expiry time.Time) orderexec.OptionContract {
	return orderexec.OptionContract{
		Strike: strike, OptionType: orderexec.OptionType(opt), Expiry: expiry,
		Delta: delta, Gamma: gamma, Theta: theta, Premium: premium, LiquidityScore: 100000,
	}
}

func chainPremium(c orderexec.OptionContract) float64 { return c.Premium }

var srLimits = srGreekLimits{DeltaMin: 0.45, DeltaMax: 0.60, MaxThetaPct: 8, MaxGamma: 0.005, GammaDTE: 1, MinLiquidity: 5000}

func TestSRStrikePicksDeltaBandMiddle(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR) // expiry 06 Oct, 6 days out
	chain := []orderexec.OptionContract{
		srContract(24100, "CE", 0.68, 0.001, -8, 210, testExpiry),
		srContract(24200, "CE", 0.53, 0.001, -9, 150, testExpiry),
		srContract(24250, "CE", 0.46, 0.001, -9, 120, testExpiry),
		srContract(24300, "CE", 0.38, 0.001, -8, 95, testExpiry),
		srContract(24200, "PE", -0.47, 0.001, -9, 140, testExpiry),
	}
	c, err := selectSRContract(chain, "CE", now, srLimits, chainPremium)
	if err != nil {
		t.Fatal(err)
	}
	if c.Strike != 24200 {
		t.Errorf("strike %d, want 24200 (delta 0.53)", c.Strike)
	}
	p, err := selectSRContract(chain, "PE", now, srLimits, chainPremium)
	if err != nil || p.Strike != 24200 {
		t.Errorf("PE strike %d err %v", p.Strike, err)
	}
}

func TestSRStrikeRejectsThetaTrap(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)
	chain := []orderexec.OptionContract{
		srContract(24200, "CE", 0.53, 0.001, -15, 150, testExpiry), // 10% a day
		srContract(24250, "CE", 0.47, 0.001, -8, 120, testExpiry),  // 6.7%
	}
	c, err := selectSRContract(chain, "CE", now, srLimits, chainPremium)
	if err != nil || c.Strike != 24250 {
		t.Fatalf("strike %d err %v, want 24250", c.Strike, err)
	}
}

func TestSRStrikeGammaCapOnlyNearExpiry(t *testing.T) {
	chain := []orderexec.OptionContract{srContract(24200, "CE", 0.52, 0.009, -9, 150, testExpiry)}
	far := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)
	if _, err := selectSRContract(chain, "CE", far, srLimits, chainPremium); err != nil {
		t.Fatalf("gamma cap applied 6 days out: %v", err)
	}
	near := time.Date(2026, 10, 5, 10, 0, 0, 0, istSR) // day before expiry
	_, err := selectSRContract(chain, "CE", near, srLimits, chainPremium)
	if err == nil || !strings.Contains(err.Error(), "gamma 1") {
		t.Fatalf("want gamma refusal, got %v", err)
	}
}

func TestSRStrikeSkipsSameDayExpiry(t *testing.T) {
	now := time.Date(2026, 10, 6, 10, 0, 0, 0, istSR) // expiry day
	next := testExpiry.AddDate(0, 0, 7)
	chain := []orderexec.OptionContract{
		srContract(24200, "CE", 0.52, 0.002, -30, 60, testExpiry),
		srContract(24200, "CE", 0.52, 0.001, -9, 180, next),
	}
	c, err := selectSRContract(chain, "CE", now, srLimits, chainPremium)
	if err != nil || !sameDay(c.Expiry, next) {
		t.Fatalf("expiry %v err %v, want next week", c.Expiry, err)
	}
}

func TestSRStrikeRefusesWithoutPremiumOrContracts(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)
	if _, err := selectSRContract(nil, "CE", now, srLimits, chainPremium); err == nil {
		t.Error("empty chain accepted")
	}
	chain := []orderexec.OptionContract{srContract(24200, "CE", 0.52, 0.001, -9, 0, testExpiry)}
	_, err := selectSRContract(chain, "CE", now, srLimits, chainPremium)
	if err == nil || !strings.Contains(err.Error(), "no premium 1") {
		t.Fatalf("want no-premium refusal, got %v", err)
	}
	// Premium supplied from the ladder LTP instead of the chain.
	ltp := func(orderexec.OptionContract) float64 { return 150 }
	if c, err := selectSRContract(chain, "CE", now, srLimits, ltp); err != nil || c.Strike != 24200 {
		t.Fatalf("strike %d err %v", c.Strike, err)
	}
}

func TestSREntryResolvesGreeksPick(t *testing.T) {
	c1 := srContract(24000, "CE", 0.63, 0.001, -3, 0, testExpiry) // too deep
	c2 := srContract(24050, "CE", 0.52, 0.001, -3, 0, testExpiry)
	g := &fakeGreeks{chain: []orderexec.OptionContract{c1, c2}}
	svc := deltaSvc(g, 2400000, "CE24000", "CE24050")
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax, svc.cfg.SRMaxThetaPct = 0.45, 0.60, 8
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())

	sig := buyCall(24000)
	sig.StrategyName = "NIFTY50_SR"
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)
	if err := svc.resolveEntryStrike(context.Background(), &sig, now); err != nil {
		t.Fatal(err)
	}
	if sig.Strike != 24050 || sig.FNOToken != "CE24050" {
		t.Fatalf("bought %d %s, want 24050 (delta 0.52)", sig.Strike, sig.FNOToken)
	}
}

// IV and cost are part of the pick: an expensive contract is skipped for
// the next one in the band instead of being picked and then refused.
func TestSRStrikeSkipsHighIVForNextInBand(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)
	hot := srContract(24200, "CE", 0.52, 0.001, -9, 150, testExpiry)
	hot.IV = 31
	ok := srContract(24250, "CE", 0.47, 0.001, -8, 120, testExpiry)
	ok.IV = 0.18 // fraction form
	lim := srLimits
	lim.MaxBuyIV = 25
	ev, err := evalSRContracts([]orderexec.OptionContract{hot, ok}, "CE", now, lim, chainPremium)
	if err != nil || !ev.Found || ev.Best.Strike != 24250 || ev.Rejects.IV != 1 {
		t.Fatalf("ev=%+v err=%v, want 24250 with 1 IV reject", ev, err)
	}
}

func TestSRStrikeSkipsContractWhoseGainMissesCost(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)
	chain := []orderexec.OptionContract{
		srContract(24250, "CE", 0.46, 0.001, -8, 120, testExpiry),
		srContract(24200, "CE", 0.53, 0.001, -9, 150, testExpiry),
	}
	lim := srLimits
	lim.CostMultiple = 3
	lim.TargetMove = 2000                               // 20 index points
	lim.Slippage = func(ltp int64) int64 { return 160 } // ₹1.60 a side
	// gain = 2000 × delta: 0.53 → 1060, 0.46 → 920; need ≥ 3 × 320 = 960.
	ev, err := evalSRContracts(chain, "CE", now, lim, chainPremium)
	if err != nil || !ev.Found || ev.Best.Strike != 24200 || ev.Rejects.Cost != 1 {
		t.Fatalf("ev=%+v err=%v, want 24200 with 1 cost reject", ev, err)
	}
	lim.TargetMove = 0 // live view: no target, no cost rule
	if ev, _ := evalSRContracts(chain, "CE", now, lim, chainPremium); ev.Rejects.Cost != 0 {
		t.Fatalf("cost rule applied without a target: %+v", ev.Rejects)
	}
}

func TestSRStrikeMinDTESkipsNextDayExpiry(t *testing.T) {
	monday := time.Date(2026, 10, 5, 10, 0, 0, 0, istSR) // expiry Tue 06 Oct, 1 day out
	next := testExpiry.AddDate(0, 0, 7)
	chain := []orderexec.OptionContract{
		srContract(24200, "CE", 0.52, 0.002, -9, 60, testExpiry),
		srContract(24200, "CE", 0.52, 0.001, -9, 180, next),
	}
	lim := srLimits
	lim.MinDTE = 2
	ev, err := evalSRContracts(chain, "CE", monday, lim, chainPremium)
	if err != nil || !ev.Found || !sameDay(ev.Expiry, next) || ev.DTE != 8 {
		t.Fatalf("ev=%+v err=%v, want next week (8d)", ev, err)
	}
}
