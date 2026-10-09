package stratengine

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestIntentForSR(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax, svc.cfg.SRMinDTE = 0.45, 0.60, 2
	svc.cfg.SRThetaMaxGainPct, svc.cfg.SRMaxGamma = 25, 0.005
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.Nifty50SRConfig{})
	sr := strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SidePut, Strike: 22700, TargetMove: 2000}
	in, ok := svc.intentFor(sr)
	if !ok || in.Option != "PE" || !near(in.DeltaMin, 0.45) || !near(in.DeltaMax, 0.60) || in.MinDTE != 2 || in.TargetMove != 2000 {
		t.Fatalf("SR intent = %+v ok=%v", in, ok)
	}
	// SR carries the old path's gain-relative theta rule (not a flat
	// per-day cap, which refuses ATM weeklies near expiry) and its gamma cap.
	if in.MaxThetaPct != 0 || !near(in.ThetaMaxGainPct, 25) || !near(in.MaxGamma, 0.005) {
		t.Fatalf("SR intent theta/gamma caps = %+v, want gain 25 / flat 0 / 0.005", in)
	}

	if _, ok := svc.intentFor(strategy.Signal{StrategyName: "OTHER", Action: strategy.ActionBuy}); ok {
		t.Fatal("unknown strategy got an intent")
	}
	if _, ok := svc.intentFor(strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionExit}); ok {
		t.Fatal("exit got an intent")
	}
}

// The picker's chainAdapter and the old delta-guard path must share one
// cache: back-to-back calls (picker load, then the old path within the
// cache window) make exactly one broker call.
func TestChainAdapterSharesCacheWithOldOptionChainPath(t *testing.T) {
	g := &fakeGreeks{chain: []orderexec.OptionContract{contract(24000, "CE", 0.5)}}
	svc := deltaSvc(g, 2400000, "CE24000")
	now := time.Now()

	if _, err := (chainAdapter{svc}).Load(now); err != nil {
		t.Fatalf("chainAdapter.Load: %v", err)
	}
	if _, err := svc.optionChain(now); err != nil {
		t.Fatalf("optionChain: %v", err)
	}
	if g.calls != 1 {
		t.Fatalf("OptionGreek called %d times, want 1 (shared cache)", g.calls)
	}
}

func TestNewPickerNilWhenSRDisabled(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.PickerMode = "on"
	svc.cfg.SREnabled = false
	if p := svc.newPicker(); p != nil {
		t.Fatal("newPicker built a picker with no consumers (SR disabled)")
	}
}

func TestNewPickerBuiltWhenSREnabled(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.PickerMode = "on"
	svc.cfg.SREnabled = true
	if p := svc.newPicker(); p == nil {
		t.Fatal("newPicker returned nil with SR enabled")
	}
}

func TestPickEntryShadowKeepsOldPathOnDecides(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax = 0.45, 0.60
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.Nifty50SRConfig{})
	svc.picker = testPicker(t, svc)
	sig := strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 22700}
	now := pickerNow

	svc.cfg.PickerMode = "shadow"
	s1 := sig
	if decided, err := svc.pickEntry(&s1, now); decided || err != nil || s1.FNOToken != "" || s1.Strike != 22700 {
		t.Fatalf("shadow decided=%v err=%v token=%q strike=%d", decided, err, s1.FNOToken, s1.Strike)
	}
	if d := svc.lastPickerDecision(svc.srStrategy.Name()); d.Mode != "shadow" || d.Result != "picked" || d.Token == "" {
		t.Fatalf("shadow decision = %+v", d)
	}

	svc.cfg.PickerMode = "on"
	s2 := sig
	if decided, err := svc.pickEntry(&s2, now); !decided || err != nil || s2.FNOToken == "" || s2.FNOSymbol == "" {
		t.Fatalf("on decided=%v err=%v sig=%+v", decided, err, s2)
	}
	if d := svc.lastPickerDecision(svc.srStrategy.Name()); d.Mode != "on" || d.Token != s2.FNOToken || d.Strike != s2.Strike {
		t.Fatalf("on decision = %+v, sig = %+v", d, s2)
	}
}

func TestPickEntryOffLeavesSignalAlone(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax = 0.45, 0.60
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.Nifty50SRConfig{})
	svc.picker = testPicker(t, svc)
	svc.cfg.PickerMode = "off"
	sig := strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 22700}
	if decided, err := svc.pickEntry(&sig, pickerNow); decided || err != nil || sig.FNOToken != "" {
		t.Fatalf("off decided=%v err=%v token=%q", decided, err, sig.FNOToken)
	}
	if d := svc.lastPickerDecision(svc.srStrategy.Name()); d.Mode != "" {
		t.Fatalf("off recorded a decision: %+v", d)
	}
	// No picker at all (newPicker returns nil in off mode) behaves the same.
	svc.picker, svc.cfg.PickerMode = nil, "on"
	if decided, err := svc.pickEntry(&sig, pickerNow); decided || err != nil {
		t.Fatalf("nil picker decided=%v err=%v", decided, err)
	}
}

func TestPickEntryOnRefusesWhenPickerRefuses(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.SRDeltaMin, svc.cfg.SRDeltaMax, svc.cfg.SRMinDTE = 0.45, 0.60, 30 // no expiry that far out
	svc.srStrategy = strategy.NewNifty50SR(65, strategy.Nifty50SRConfig{})
	svc.picker = testPicker(t, svc)
	svc.cfg.PickerMode = "on"
	sig := strategy.Signal{StrategyName: svc.srStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 22700}
	err := svc.resolveEntryStrike(context.Background(), &sig, pickerNow)
	if err == nil || !strings.HasPrefix(err.Error(), "picker: ") || sig.FNOToken != "" {
		t.Fatalf("on refusal err=%v token=%q", err, sig.FNOToken)
	}
	if d := svc.lastPickerDecision(svc.srStrategy.Name()); d.Mode != "on" || d.Result != "refused" || d.Reason == "" {
		t.Fatalf("refusal decision = %+v", d)
	}
}

var pickerNow = time.Date(2026, 9, 30, 6, 0, 0, 0, time.UTC) // Wed 11:30 IST

type staticChain []optionpicker.Contract

func (s staticChain) Load(time.Time) ([]optionpicker.Contract, error) { return s, nil }

type allResolver struct{}

func (allResolver) Lookup(e time.Time, k int64, o string) (string, string, bool) {
	return "T" + itoa(k) + o, "NIFTY" + e.Format("02Jan06") + itoa(k) + o, true
}
func (allResolver) Search(time.Time, int64, string) (string, string, error) { return "", "", nil }

type nopSub struct{ got []string }

func (n *nopSub) SubscribeOptions(t []string) { n.got = append(n.got, t...) }

func testPicker(t *testing.T, svc *Service) *optionpicker.Picker {
	t.Helper()
	var chain staticChain
	for k := int64(22400); k <= 23000; k += 50 {
		for _, o := range []string{"CE", "PE"} {
			chain = append(chain, optionpicker.Contract{Strike: k, Option: o, Expiry: testExpiry, IV: 14, Liquidity: 100000})
		}
	}
	svc.cfg.PickMaxSpreadPct = 2
	svc.cfg.PickMaxQuoteAge = 3 * time.Second
	svc.cfg.PickMaxChainAge = 2 * time.Minute
	sub := &nopSub{}
	p := optionpicker.New(svc.pickerConfig(), chain, allResolver{}, sub)
	p.OnTick(model.Tick{Token: optionpicker.SpotToken, Price: 2270000})
	optionpicker.RefreshForTest(p, pickerNow)
	if len(sub.got) == 0 {
		t.Fatal("picker universe subscribed nothing")
	}
	for _, tok := range sub.got {
		p.OnTick(model.Tick{Token: tok, Price: 15000, BestBid: 14990, BestAsk: 15010, OI: 50000, QuoteTS: pickerNow})
	}
	return p
}

func TestPickerConfigCarriesRankAndDecisionCarriesScore(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.PickRank, svc.cfg.PickHoldMinutes = "return", 45
	if r := svc.pickerConfig().Rules; r.Rank != "return" || r.HoldMinutes != 45 {
		t.Fatalf("rules = %+v", r)
	}
	d := decisionFor("NIFTY50_SR", "shadow", optionpicker.Pick{Score: 0.2543}, nil, pickerNow)
	if d.Score != 0.254 {
		t.Fatalf("decision score = %v, want 0.254", d.Score)
	}
}
