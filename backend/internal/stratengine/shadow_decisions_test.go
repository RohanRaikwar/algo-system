package stratengine

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/strategy"
)

// recordOldChoice is the mechanism behind F6: it attaches what the old
// (non-picker) path chose to the picker's own decision, purely for the
// shadow-mode go/no-go review — it never changes what either path does.

func TestRecordOldChoiceSingleEntryAgreeAndDisagree(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.recordPickerDecision(pickerDecision{Strategy: "S", Mode: "shadow", Result: "picked", Symbol: "PICKED_SYM"})

	svc.recordOldChoice("S", "PICKED_SYM", nil)
	if d := svc.lastPickerDecision("S"); d.OldSymbol != "PICKED_SYM" || !d.Agree {
		t.Fatalf("agree case: %+v", d)
	}

	svc.recordOldChoice("S", "OTHER_SYM", nil)
	if d := svc.lastPickerDecision("S"); d.OldSymbol != "OTHER_SYM" || d.Agree {
		t.Fatalf("disagree case: %+v", d)
	}
}

func TestRecordOldChoiceNoOpWithoutAPickerDecision(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.recordOldChoice("NEVER_DECIDED", "X", nil) // PickerMode "off": pickEntry never ran
	if d := svc.lastPickerDecision("NEVER_DECIDED"); d.Strategy != "" {
		t.Fatalf("no-op created a decision: %+v", d)
	}
}

func TestRecordOldChoiceBasketAgreeIsOrderIndependent(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.recordPickerDecision(pickerDecision{Strategy: "IC", Mode: "shadow", Result: "picked", Legs: []pickerLeg{
		{Leg: strategy.LegLongCE, Symbol: "L1"}, {Leg: strategy.LegShortCE, Symbol: "S1"},
		{Leg: strategy.LegLongPE, Symbol: "L2"}, {Leg: strategy.LegShortPE, Symbol: "S2"},
	}})

	svc.recordOldChoice("IC", "", []string{"S1", "L1", "S2", "L2"}) // different order, same set
	if d := svc.lastPickerDecision("IC"); !d.Agree || len(d.OldLegs) != 4 {
		t.Fatalf("basket agree: %+v", d)
	}

	svc.recordOldChoice("IC", "", []string{"S1", "L1", "S2", "DIFFERENT"})
	if d := svc.lastPickerDecision("IC"); d.Agree {
		t.Fatalf("basket disagree not detected: %+v", d)
	}
}

// decisionForCondor carries all four legs and the net credit, not just the
// short CE leg, so a go/no-go review can see the whole basket.
func TestDecisionForCondorCarriesLegsAndCredit(t *testing.T) {
	cp := optionpicker.CondorPick{
		LongCE:  optionpicker.Pick{Contract: optionpicker.Contract{Strike: 23000, Symbol: "LCE"}, Delta: 0.1, Quote: optionpicker.Quote{Bid: 10, Ask: 12}},
		ShortCE: optionpicker.Pick{Contract: optionpicker.Contract{Strike: 22900, Symbol: "SCE"}, Delta: 0.2, Quote: optionpicker.Quote{Bid: 30, Ask: 32}},
		LongPE:  optionpicker.Pick{Contract: optionpicker.Contract{Strike: 22400, Symbol: "LPE"}, Delta: -0.1, Quote: optionpicker.Quote{Bid: 8, Ask: 10}},
		ShortPE: optionpicker.Pick{Contract: optionpicker.Contract{Strike: 22500, Symbol: "SPE"}, Delta: -0.2, Quote: optionpicker.Quote{Bid: 20, Ask: 22}},
		Credit:  4600,
	}
	d := decisionForCondor("NIFTY50_RANGE_IC", "shadow", cp, nil, pickerNow)
	if d.Result != "picked" || d.Credit != 4600 || len(d.Legs) != 4 {
		t.Fatalf("decision = %+v", d)
	}
	byLeg := map[string]pickerLeg{}
	for _, l := range d.Legs {
		byLeg[l.Leg] = l
	}
	if byLeg[strategy.LegShortCE].Symbol != "SCE" || byLeg[strategy.LegShortCE].Strike != 22900 {
		t.Fatalf("short CE leg = %+v", byLeg[strategy.LegShortCE])
	}
	if byLeg[strategy.LegLongPE].Symbol != "LPE" || byLeg[strategy.LegLongPE].Strike != 22400 {
		t.Fatalf("long PE leg = %+v", byLeg[strategy.LegLongPE])
	}
	// The flat legacy fields mirror the short CE leg.
	if d.Symbol != "SCE" || d.Strike != 22900 {
		t.Fatalf("flat mirror = %+v", d)
	}

	refused := decisionForCondor("NIFTY50_RANGE_IC", "shadow", optionpicker.CondorPick{}, &optionpicker.Refusal{Reason: "no credit"}, pickerNow)
	if refused.Result != "refused" || len(refused.Legs) != 0 {
		t.Fatalf("refused decision = %+v", refused)
	}
}

// Wiring: in shadow mode, resolveEntryStrike's old path (RANGE here, delta
// guard off to isolate this from unrelated greeks setup) records its choice
// against the picker's earlier decision.
func TestResolveEntryStrikeShadowRecordsOldChoice(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000, "CE22700")
	svc.cfg.RangeDeltaGuard = false
	svc.nifty50RangeStrategy = strategy.NewNifty50Range(1)
	svc.picker = testPicker(t, svc)
	svc.cfg.PickerMode = "shadow"

	sig := strategy.Signal{StrategyName: svc.nifty50RangeStrategy.Name(), Action: strategy.ActionBuy, Side: strategy.SideCall, Strike: 22700}
	if err := svc.resolveEntryStrike(context.Background(), &sig, pickerNow); err != nil {
		t.Fatalf("resolveEntryStrike: %v", err)
	}
	if sig.FNOSymbol == "" {
		t.Fatal("old path did not resolve a symbol")
	}
	d := svc.lastPickerDecision(svc.nifty50RangeStrategy.Name())
	if d.Mode != "shadow" || d.OldSymbol != sig.FNOSymbol {
		t.Fatalf("decision = %+v, sig.FNOSymbol = %q", d, sig.FNOSymbol)
	}
}

// Wiring: in shadow mode, handleBasketSignal's old path (expandLegSignals)
// records its four leg symbols against the picker's condor decision.
func TestHandleBasketShadowRecordsOldLegsChoice(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.cfg.EODExitTime = "15:20"
	svc.cfg.RangeDeltaGuard = false
	svc.picker = testPicker(t, svc)
	svc.cfg.PickerMode = "shadow"
	basket := &fakeBasket{}
	svc.testBasket = basket
	svc.legPriceWait = 10 * time.Millisecond
	svc.orderRunner = func(context.Context, strategy.Signal) {}

	svc.handleBasketSignal(context.Background(), condorSignal(), pickerNow)

	d := svc.lastPickerDecision("NIFTY50_RANGE_IC")
	if len(d.OldLegs) != 4 {
		t.Fatalf("OldLegs = %v, decision = %+v", d.OldLegs, d)
	}
	for _, s := range d.OldLegs {
		if s == "" {
			t.Fatalf("empty leg symbol in OldLegs: %v", d.OldLegs)
		}
	}
}
