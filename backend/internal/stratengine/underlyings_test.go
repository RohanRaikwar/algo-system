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
	if _, err := svc.extraChain(ex, now); err != nil { // off-path warm-up
		t.Fatal(err)
	}
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

// SENSEX's view goes out on pub:srx keyed by its index; NIFTY's pub:sr
// is not touched by SENSEX candles.
func TestPublishSRXState_KeyedByIndex(t *testing.T) {
	svc, ex := sensexService(t)
	var srx, sr []string
	svc.srxPublishHook = func(p string) { srx = append(srx, p) }
	svc.srPublishHook = func(p string) { sr = append(sr, p) }
	ts := time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC)
	ex.strat.OnTFCandle(model.TFCandle{Exchange: "BSE", Token: "99919000", TF: 60, TS: ts,
		Open: 8420000, High: 8425000, Low: 8415000, Close: 8421000})

	last := svc.publishSRXState(context.Background(), "")
	if len(srx) != 1 || !strings.Contains(srx[0], `"BSE:99919000"`) || !strings.Contains(srx[0], `"strategy":"SENSEX_SR"`) {
		t.Fatalf("srx publish = %v", srx)
	}
	if svc.publishSRXState(context.Background(), last) != last || len(srx) != 1 {
		t.Fatalf("unchanged view republished")
	}
	svc.publishSRState(context.Background(), "")
	if len(sr) != 0 {
		t.Fatalf("NIFTY pub:sr published from a SENSEX candle: %v", sr)
	}
}

// In shadow mode the SENSEX picker decides, the decision is recorded, and
// the old greek path still buys.
func TestPickExtraEntry_ShadowRecordsOnly(t *testing.T) {
	svc, ex := sensexService(t)
	svc.cfg.PickerMode, svc.cfg.SREnabled = "shadow", true
	svc.buildExtraPickers()
	if ex.optPicker == nil {
		t.Fatal("no SENSEX picker in shadow mode")
	}
	ex.optPicker.OnTick(model.Tick{Token: "99919000", Exchange: "BSE", Price: 8420000})
	if st := ex.optPicker.Status(time.Now()); st.Spot != 8420000 {
		t.Fatalf("SENSEX picker spot = %d", st.Spot)
	}
	sig := strategy.Signal{StrategyName: "SENSEX_SR", Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 84200}
	decided, err := svc.pickExtraEntry(ex, &sig, time.Now())
	if decided || err != nil {
		t.Fatalf("shadow decided=%v err=%v", decided, err)
	}
	if d := svc.lastPickerDecision("SENSEX_SR"); d.Strategy != "SENSEX_SR" || d.Mode != "shadow" {
		t.Fatalf("decision = %+v", d)
	}
}

// The SENSEX strike selection view is published keyed by its index with
// SENSEX's own contracts and scaled view target.
func TestExtraStrikeSel_Published(t *testing.T) {
	svc, ex := sensexService(t)
	svc.cfg.SREnabled = true
	ex.res = &fakeExtraRes{}
	ex.greeks = &fakeGreeks{chain: []orderexec.OptionContract{
		srContract(81200, "CE", 0.52, 0.001, -20, 400, testExpiry),
		srContract(81200, "PE", -0.50, 0.001, -20, 380, testExpiry),
	}}
	var got []string
	svc.strikeSelXPublishHook = func(p string) { got = append(got, p) }
	svc.refreshExtraStrikeSels(context.Background(), time.Date(2026, 9, 30, 10, 0, 0, 0, istSR))
	if len(got) != 1 || !strings.Contains(got[0], `"BSE:99919000"`) || !strings.Contains(got[0], `"strategy":"SENSEX_SR"`) ||
		!strings.Contains(got[0], `"symbol":"SENSEX81200CE"`) {
		t.Fatalf("strikeselx = %v", got)
	}
}
