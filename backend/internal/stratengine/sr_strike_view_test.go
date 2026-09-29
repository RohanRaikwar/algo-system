package stratengine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func strikeSelSvc(g *fakeGreeks, sent *[]string) *Service {
	svc := deltaSvc(g, 2269000, "CE22700", "PE22700")
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax = 0.45, 0.60
	svc.strikeSelPublishHook = func(p string) { *sent = append(*sent, p) }
	return svc
}

func lastStrikeSel(t *testing.T, sent []string) strikeSelView {
	t.Helper()
	if len(sent) == 0 {
		t.Fatal("nothing published")
	}
	var v strikeSelView
	if err := json.Unmarshal([]byte(sent[len(sent)-1]), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStrikeSelLivePickWithGreeksAndRejects(t *testing.T) {
	now := time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC) // 11:30 IST, DTE 7
	ce := contract(22700, "CE", 0.52)
	ce.Gamma, ce.Theta, ce.Vega, ce.IV, ce.LiquidityScore = 0.0011, -6.5, 11.2, 0.142, 250000
	g := &fakeGreeks{chain: []orderexec.OptionContract{ce, contract(22800, "CE", 0.30), contract(22700, "PE", -0.20)}}
	var sent []string
	svc := strikeSelSvc(g, &sent)

	svc.refreshStrikeSel(context.Background(), now)
	v := lastStrikeSel(t, sent)
	if v.Params.DeltaMin != 0.45 || v.Params.DeltaMax != 0.60 || v.Error != "" {
		t.Fatalf("params/error = %+v %q", v.Params, v.Error)
	}
	p := v.Call.Pick
	if p == nil || p.Strike != 22700 || p.Symbol != "NIFTY06OCT2622700CE" || p.Token != "CE22700" ||
		p.Delta != 0.52 || p.IV != 14.2 || p.Premium != 50 || p.DTE != 7 || p.Expiry != "2026-10-06" {
		t.Fatalf("call pick = %+v", p)
	}
	if v.Call.Rejects.Delta != 1 {
		t.Fatalf("call rejects = %+v", v.Call.Rejects)
	}
	if v.Put.Pick != nil || v.Put.Rejects.Delta != 1 {
		t.Fatalf("put = %+v", v.Put)
	}
}

func TestStrikeSelReportsMissingChain(t *testing.T) {
	var sent []string
	svc := strikeSelSvc(&fakeGreeks{err: errors.New("no such host")}, &sent)
	svc.refreshStrikeSel(context.Background(), time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC))
	v := lastStrikeSel(t, sent)
	if v.Error == "" || v.Call != nil || v.Put != nil {
		t.Fatalf("view = %+v", v)
	}
}

func TestPickSRStrikeRecordsLastPick(t *testing.T) {
	now := time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC)
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(22650, "PE", -0.50)}}
	var sent []string
	svc := strikeSelSvc(g, &sent)
	svc.orderExecutor.UpdateLTP(model.Tick{Token: "PE22650", Exchange: "NFO", Price: 9000})
	sig := strategy.Signal{StrategyName: "NIFTY50_SR", Action: strategy.ActionBuy, Side: strategy.SidePut, Strike: 22700}
	if _, err := svc.pickSRStrike(&sig, now); err != nil {
		t.Fatal(err)
	}
	l := lastStrikeSel(t, sent).Last
	if l == nil || l.Strike != 22650 || l.AskedStrike != 22700 || l.Side != "PUT" || l.Premium != 90 || l.Delta != -0.50 {
		t.Fatalf("last = %+v", l)
	}
}

// expiryAwareResolver resolves by expiry like the real strike picker.
type expiryAwareResolver struct{ symbolResolver }

func (expiryAwareResolver) ResolveStrikeOn(exp time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	d := strings.ToUpper(exp.Format("02Jan06"))
	return orderexec.StrikeInfo{Token: d + opt + itoa(strike), Symbol: "NIFTY" + d + itoa(strike) + opt, Strike: strike}, nil
}

func TestSREntryOnMondayBuysNextWeekContract(t *testing.T) {
	monday := time.Date(2026, 10, 5, 5, 0, 0, 0, time.UTC) // 10:30 IST, expiry Tue 06 Oct
	next := testExpiry.AddDate(0, 0, 7)
	near := contract(22700, "PE", -0.52)
	far := contract(22700, "PE", -0.52)
	far.Expiry = next
	g := &fakeGreeks{chain: []orderexec.OptionContract{near, far}}
	var sent []string
	svc := strikeSelSvc(g, &sent)
	svc.cfg.SRMinDTE = 2
	svc.cfg.RangeDeltaGuard = false
	svc.legResolver = expiryAwareResolver{}
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.Nifty50SRConfig{})
	svc.orderExecutor.UpdateLTP(model.Tick{Token: "13OCT26PE22700", Exchange: "NFO", Price: 18000})

	sig := strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SidePut, Strike: 22700}
	if err := svc.resolveEntryStrike(context.Background(), &sig, monday); err != nil {
		t.Fatal(err)
	}
	if sig.FNOSymbol != "NIFTY13OCT2622700PE" || sig.FNOToken != "13OCT26PE22700" {
		t.Fatalf("bought %s (%s), want next week's NIFTY13OCT2622700PE", sig.FNOSymbol, sig.FNOToken)
	}
}

func TestStrikeSelIncludesPickerStatusAndDecisions(t *testing.T) {
	var sent []string
	svc := strikeSelSvc(&fakeGreeks{chain: []orderexec.OptionContract{contract(22700, "CE", 0.52)}}, &sent)
	svc.cfg.PickerMode, svc.cfg.PickMaxSpreadPct, svc.cfg.PickMaxQuoteAge, svc.cfg.PickMaxChainAge = "shadow", 2, 3*time.Second, 2*time.Minute
	svc.picker = testPicker(t, svc)
	svc.recordPickerDecision(pickerDecision{Strategy: "NIFTY50_SR", Mode: "shadow", Result: "picked", Symbol: "NIFTY06OCT2622700CE", TS: "x"})
	svc.refreshStrikeSel(context.Background(), pickerNow)
	v := lastStrikeSel(t, sent)
	if v.Picker == nil || v.Picker.Mode != "shadow" || v.Picker.Streamed == 0 || v.Picker.Rules.MaxSpreadPct != 2 ||
		len(v.Picker.Decisions) != 1 || v.Picker.Decisions[0].Symbol != "NIFTY06OCT2622700CE" {
		t.Fatalf("picker view = %+v", v.Picker)
	}
}

func TestSRExpiryLadderSubscribesNextWeekOnMonday(t *testing.T) {
	monday := time.Date(2026, 10, 5, 5, 0, 0, 0, time.UTC)
	far := contract(22700, "CE", 0.52)
	far.Expiry = testExpiry.AddDate(0, 0, 7)
	var sent []string
	svc := strikeSelSvc(&fakeGreeks{chain: []orderexec.OptionContract{contract(22700, "CE", 0.52), far}}, &sent)
	svc.cfg.SRMinDTE = 2
	svc.legResolver = expiryAwareResolver{}
	var subscribed []string
	svc.subscribeHook = func(tokens []string) { subscribed = append(subscribed, tokens...) }

	svc.refreshStrikeSel(context.Background(), monday)
	if len(subscribed) != 2*(2*ladderStrikes+1) || subscribed[0] != "13OCT26CE22300" {
		t.Fatalf("subscribed %d tokens (first %v), want the 13 Oct ladder", len(subscribed), subscribed)
	}
	svc.refreshStrikeSel(context.Background(), monday.Add(30*time.Second))
	if len(subscribed) != 2*(2*ladderStrikes+1) {
		t.Fatalf("re-subscribed: %d", len(subscribed))
	}

	svc.cfg.SRMinDTE = 1 // nearest expiry is the SR expiry: main ladder covers it
	subscribed = nil
	svc2 := strikeSelSvc(&fakeGreeks{chain: []orderexec.OptionContract{contract(22700, "CE", 0.52)}}, &sent)
	svc2.cfg.SRMinDTE = 2
	svc2.subscribeHook = func(tokens []string) { subscribed = append(subscribed, tokens...) }
	svc2.refreshStrikeSel(context.Background(), time.Date(2026, 9, 30, 5, 0, 0, 0, time.UTC)) // Wed: 6 days out
	if len(subscribed) != 0 {
		t.Fatalf("subscribed %v when nearest expiry already qualifies", subscribed)
	}
}
