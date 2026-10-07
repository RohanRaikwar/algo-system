package exitpolicy

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 7, 10, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))

func thetaParams() ThetaConfig {
	return ThetaConfig{MaxFlatMin: 60, DecayPctOfGain: 25, FlatProgressPct: 30, FallbackFlatPremPct: 10, ConfirmMin: 1}
}

// callPos: CALL bought at ₹100 with delta 0.50, theta ₹15/day, target
// 100 index points → expected gain ₹50 (5000 paise).
func callPos() Position {
	return Position{
		Key: "NIFTY50_SR|CALL", Strategy: "NIFTY50_SR", Side: "CALL",
		IndexToken: "26000", FNOToken: "12345", EntryTS: t0,
		IndexEntry: 2500000, PremEntry: 10000,
		Greeks: NewEntryGreeks(0.50, -15, 10000),
	}
}

func putPos() Position {
	p := callPos()
	p.Key, p.Side = "NIFTY50_SR|PUT", "PUT"
	p.Greeks = NewEntryGreeks(-0.50, -15, 10000)
	return p
}

func TestNewEntryGreeks(t *testing.T) {
	g := NewEntryGreeks(-0.5234, -15.007, 10000)
	if g.DeltaMilli != -523 || g.DecayPerDayPaise != 1501 || g.ExpGainPaise != 5234 {
		t.Fatalf("got %+v", *g)
	}
	if NewEntryGreeks(0, -15, 10000) != nil || NewEntryGreeks(0.5, -15, 0) != nil {
		t.Fatal("no delta or no target must give nil greeks")
	}
}

func TestThetaRule(t *testing.T) {
	rule := ThetaRule{P: thetaParams()}
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	tests := []struct {
		name string
		pos  Position
		snap Snapshot
		fire bool
		text string
	}{
		{"fresh and flat", callPos(),
			Snapshot{Now: at(5), IndexLTP: 2500000, PremLTP: 9990, PremBest: 10050}, false, ""},
		{"flat past max_flat_min", callPos(),
			// modeled 1500×60/375 = 240 paise, well under 25% of 5000
			Snapshot{Now: at(60), IndexLTP: 2500000, PremLTP: 9800, PremBest: 10100}, true, "flat 60m"},
		{"modeled decay over budget", func() Position {
			p := callPos()
			p.Greeks = NewEntryGreeks(0.50, -500, 10000) // 50000×20/375 = 2666 ≥ 1250
			return p
		}(), Snapshot{Now: at(20), IndexLTP: 2500000, PremLTP: 9900, PremBest: 10000}, true, "modeled"},
		{"realized decay: index flat, premium crushed", callPos(),
			// expected 10000 + 0.5×0 = 10000, now 8600 → realized 1400 ≥ 1250
			Snapshot{Now: at(15), IndexLTP: 2500000, PremLTP: 8600, PremBest: 10000}, true, "realized"},
		{"index fell: premium drop explained by delta", callPos(),
			// expected 10000 + 0.5×(−3000) = 8500, now 8600 → realized 0
			Snapshot{Now: at(15), IndexLTP: 2497000, PremLTP: 8600, PremBest: 10000}, false, ""},
		{"working trade is never cut", callPos(),
			// best gain 2000 ≥ 30% of 5000
			Snapshot{Now: at(90), IndexLTP: 2504000, PremLTP: 11000, PremBest: 12000}, false, ""},
		{"protect armed", callPos(),
			Snapshot{Now: at(90), IndexLTP: 2500000, PremLTP: 9000, PremBest: 10100, ProtectArmed: true}, false, ""},
		{"PUT realized decay with index up", putPos(),
			// expected 10000 + (−0.5)×(+1000) = 9500, now 8000 → realized 1500
			Snapshot{Now: at(15), IndexLTP: 2501000, PremLTP: 8000, PremBest: 10000}, true, "realized"},
		{"no greeks: time branch only", func() Position {
			p := callPos()
			p.Greeks = nil
			return p
		}(), Snapshot{Now: at(60), IndexLTP: 2500000, PremLTP: 5000, PremBest: 10500}, true, "flat 60m"},
		{"no greeks: not flat by premium fallback", func() Position {
			p := callPos()
			p.Greeks = nil
			return p
		}(), Snapshot{Now: at(60), IndexLTP: 2500000, PremLTP: 10500, PremBest: 11500}, false, ""},
		{"no greeks: no decay branch before max_flat_min", func() Position {
			p := callPos()
			p.Greeks = nil
			return p
		}(), Snapshot{Now: at(30), IndexLTP: 2500000, PremLTP: 5000, PremBest: 10000}, false, ""},
		{"no premium yet", func() Position {
			p := callPos()
			p.PremEntry = 0
			return p
		}(), Snapshot{Now: at(90), IndexLTP: 2500000}, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, fire := rule.Evaluate(tc.pos, tc.snap)
			if fire != tc.fire {
				t.Fatalf("fire=%v want %v (%s)", fire, tc.fire, d.Text)
			}
			if fire && (d.Reason != ReasonTheta || !strings.Contains(d.Text, tc.text)) {
				t.Fatalf("decision %+v, want text containing %q", d, tc.text)
			}
		})
	}
}

func TestThetaRuleText(t *testing.T) {
	d, ok := ThetaRule{P: thetaParams()}.Evaluate(callPos(),
		Snapshot{Now: t0.Add(15 * time.Minute), IndexLTP: 2500000, PremLTP: 8600, PremBest: 10000})
	if !ok {
		t.Fatal("want fire")
	}
	want := "THETA CALL flat 15m decay=₹14.00 (28% of exp gain ₹50.00) realized"
	if d.Text != want {
		t.Fatalf("text %q\nwant %q", d.Text, want)
	}
	if d.Detail["realized"] != 1400 || d.Detail["exp_gain"] != 5000 || d.Detail["held_min"] != 15 {
		t.Fatalf("detail %v", d.Detail)
	}
}
