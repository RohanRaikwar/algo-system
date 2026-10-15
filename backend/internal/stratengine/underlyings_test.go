package stratengine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// fakeExtraRes resolves SENSEX contracts as "SX<strike><opt>" with token
// "<strike><opt>".
type fakeExtraRes struct{ calls []string }

func (f *fakeExtraRes) ResolveStrike(_ time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	return f.ResolveStrikeOn(time.Time{}, strike, opt)
}

func (f *fakeExtraRes) ResolveStrikeOn(exp time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	f.calls = append(f.calls, fmt.Sprintf("%d%s", strike, opt))
	tok := fmt.Sprintf("%d%s", strike, opt)
	return orderexec.StrikeInfo{Token: tok, Symbol: "SENSEX" + tok, Strike: strike, LotSize: 20}, nil
}

func (f *fakeExtraRes) NextExpiry(time.Time) time.Time { return testExpiry }

func sensexService(t *testing.T) (*Service, *srExtra) {
	t.Helper()
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	svc := &Service{
		cfg:           Config{Qty: 65, SRDeltaMin: 0.45, SRDeltaMax: 0.60},
		orderExecutor: orderexec.NewOrderExecutor(orderexec.Config{Qty: 65, FNOExchange: "NFO", LogPrefix: "[test]"}),
	}
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())
	extras := svc.buildSRExtras(strategy.DefaultNifty50SRConfig())
	if len(extras) != 1 {
		t.Fatalf("extras = %d, want 1", len(extras))
	}
	return svc, extras[0]
}

func TestSRExtras_BuiltFromProfile(t *testing.T) {
	svc, ex := sensexService(t)
	c := ex.strat.Config()
	if ex.strat.Name() != "SENSEX_SR" || c.IndexToken != "BSE:99919000" || c.StrikeStep != 100 {
		t.Fatalf("SENSEX SR config: name=%s index=%s step=%d", ex.strat.Name(), c.IndexToken, c.StrikeStep)
	}
	if c.TouchTolPts != 3300 || c.MaxDayLossPts != 19800 {
		t.Fatalf("points not scaled: touch=%d maxloss=%d", c.TouchTolPts, c.MaxDayLossPts)
	}
	if svc.srByName("SENSEX_SR") != ex.strat || svc.srByName("NIFTY50_SR") != svc.srStrategy || svc.srByName("X") != nil {
		t.Fatalf("srByName lookup wrong")
	}
	if len(svc.srAll()) != 2 || ex.snapshotKey() != "sensex_sr" {
		t.Fatalf("srAll=%d key=%s", len(svc.srAll()), ex.snapshotKey())
	}
	if got := svc.qualifyFNOTokenFor("SENSEX_SR", "1163095"); got != "BFO:1163095" {
		t.Fatalf("qualify SENSEX = %s", got)
	}
	if got := svc.qualifyFNOTokenFor("NIFTY50_SR", "53408"); got != "NFO:53408" {
		t.Fatalf("qualify NIFTY = %s", got)
	}
}

func TestSRExtras_EnvOverride(t *testing.T) {
	t.Setenv("STRAT_SR_SENSEX_MAX_DAY_LOSS_PAISE", "25000")
	_, ex := sensexService(t)
	if got := ex.strat.Config().MaxDayLossPts; got != 25000 {
		t.Fatalf("MaxDayLossPts = %d, want override 25000", got)
	}
}

func TestSRExtras_NotBuiltWithoutUnderlyings(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	svc := &Service{}
	if n := len(svc.buildSRExtras(strategy.DefaultNifty50SRConfig())); n != 0 {
		t.Fatalf("built %d extras without UNDERLYINGS", n)
	}
}

// A SENSEX entry picks by greeks from its own chain and buys that exact
// contract; with no premium yet it subscribes on BFO and refuses.
func TestResolveExtraEntry_GreeksPickOwnContract(t *testing.T) {
	svc, ex := sensexService(t)
	res := &fakeExtraRes{}
	ex.res = res
	ex.greeks = &fakeGreeks{chain: []orderexec.OptionContract{
		srContract(81000, "CE", 0.70, 0.001, -20, 500, testExpiry),
		srContract(81200, "CE", 0.52, 0.001, -20, 400, testExpiry),
		srContract(81500, "CE", 0.35, 0.001, -20, 300, testExpiry),
	}}
	var subscribed []string
	svc.subscribeHook = func(tokens []string) { subscribed = append(subscribed, tokens...) }
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, istSR)

	sig := strategy.Signal{StrategyName: "SENSEX_SR", Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 81100}
	err := svc.resolveExtraEntry(context.Background(), ex, &sig, now)
	if err == nil || !strings.Contains(err.Error(), "no premium") || len(subscribed) != 1 || subscribed[0] != "81200CE" {
		t.Fatalf("want refusal + subscribe 81200CE, got err=%v subscribed=%v", err, subscribed)
	}

	svc.orderExecutor.UpdateLTP(model.Tick{Token: "81200CE", Price: 40000})
	sig = strategy.Signal{StrategyName: "SENSEX_SR", Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 81100}
	if err := svc.resolveExtraEntry(context.Background(), ex, &sig, now); err != nil {
		t.Fatal(err)
	}
	if sig.FNOToken != "81200CE" || sig.FNOSymbol != "SENSEX81200CE" || sig.Strike != 81200 {
		t.Fatalf("sig = %+v", sig)
	}
}

// Without a chain the SENSEX entry takes the strategy's ATM strike.
func TestResolveExtraEntry_NoGreeksTakesATM(t *testing.T) {
	svc, ex := sensexService(t)
	ex.res = &fakeExtraRes{}
	svc.orderExecutor.UpdateLTP(model.Tick{Token: "81100PE", Price: 30000})
	sig := strategy.Signal{StrategyName: "SENSEX_SR", Action: strategy.ActionBuy, Side: strategy.SidePut, Strike: 81100}
	if err := svc.resolveExtraEntry(context.Background(), ex, &sig, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sig.FNOToken != "81100PE" {
		t.Fatalf("FNOToken = %s", sig.FNOToken)
	}
}

// The SENSEX ladder steps by 100 around its own index.
func TestExtraLadder_StepsBySENSEXStrike(t *testing.T) {
	_, ex := sensexService(t)
	res := &fakeExtraRes{}
	tokens := ex.ladderTokens(res, time.Now(), 8123400) // 81234.00
	if len(tokens) != 2*(2*ladderStrikes+1) {
		t.Fatalf("ladder tokens = %d", len(tokens))
	}
	if res.calls[0] != fmt.Sprintf("%dCE", 81200-ladderStrikes*100) {
		t.Fatalf("first ladder strike = %s", res.calls[0])
	}
}
