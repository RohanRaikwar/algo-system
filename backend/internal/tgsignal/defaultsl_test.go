package tgsignal

import (
	"testing"

	"trading-systemv1/internal/model"
)

var prodDefaults = Defaults{
	SLPoints:     map[string]int64{"NIFTY": 15, "SENSEX": 30, "BANKNIFTY": 30},
	SLPct:        10,
	TargetPoints: []int64{20, 30},
}

func TestDefaultSLPointsPerIndex(t *testing.T) {
	for _, tc := range []struct {
		under string
		want  int64
	}{{"SENSEX", 45000 - 3000}, {"NIFTY", 45000 - 1500}, {"BANKNIFTY", 45000 - 3000}, {"FINNIFTY", 45000 * 90 / 100}} {
		rx := model.TGSignal{Underlying: tc.under, Strike: 84100, OptionType: "PE", TriggerPaise: 45000}
		got, err := withDefaultSL(rx, ErrNoSL, prodDefaults)
		if err != nil || got.SLPaise != tc.want || !got.SLDefault {
			t.Errorf("%s: sl %d (%v), want %d", tc.under, got.SLPaise, err, tc.want)
		}
	}
	// A trigger below the points keeps the refusal.
	rx := model.TGSignal{Underlying: "SENSEX", Strike: 84100, OptionType: "PE", TriggerPaise: 2500}
	if _, err := withDefaultSL(rx, ErrNoSL, prodDefaults); err == nil {
		t.Error("stop below 0 accepted")
	}
	// LLM reading, same rule.
	r := llmWithDefaultSL(LLMResult{Kind: "new_call", Underlying: "Sensex", Strike: 84100, OptionType: "PE", Trigger: "450"}, prodDefaults)
	if c := r.Call(); c.SLPaise != 42000 || !c.SLDefault {
		t.Errorf("llm sl %+v", c)
	}
}

func TestDefaultTargets(t *testing.T) {
	c := withDefaultTargets(model.TGSignal{TriggerPaise: 45000}, prodDefaults)
	if len(c.Targets) != 2 || c.Targets[0] != 47000 || c.Targets[1] != 48000 || !c.TargetsDefault {
		t.Fatalf("default targets %+v", c)
	}
	own := withDefaultTargets(model.TGSignal{TriggerPaise: 45000, Targets: []int64{48000}}, prodDefaults)
	if len(own.Targets) != 1 || own.TargetsDefault {
		t.Fatalf("caller targets replaced %+v", own)
	}
	if off := withDefaultTargets(model.TGSignal{TriggerPaise: 45000}, Defaults{}); len(off.Targets) != 0 {
		t.Fatalf("targets with defaults off %+v", off)
	}
}

func TestParseDefaultPoints(t *testing.T) {
	m, err := parseSLPoints("nifty=15, SENSEX=30")
	if err != nil || m["NIFTY"] != 15 || m["SENSEX"] != 30 {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"NIFTY", "NIFTY=0", "=5", "NIFTY=x"} {
		if _, err := parseSLPoints(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if m, err := parseSLPoints("off"); err != nil || len(m) != 0 {
		t.Errorf("off: %v %v", m, err)
	}
	p, err := parseTargetPoints("20,30")
	if err != nil || len(p) != 2 || p[1] != 30 {
		t.Fatalf("%v %v", p, err)
	}
	for _, bad := range []string{"30,20", "0", "x", "20,20"} {
		if _, err := parseTargetPoints(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The "SL paid" SENSEX call from the dashboard: SL 420, targets 470/480.
func TestSensexSLPaidCallGetsPointDefaults(t *testing.T) {
	h, s, _ := newTestHandler(t, nil)
	h.defs = prodDefaults
	h.chats[42] = true
	h.onMessage(t.Context(), msg(1, "💠SENSEX 26 NOV 84100 PE💠\n\n↗️ BUY ABOVE :- 450 🔺\n\nStoploss : PAID"), tNow)
	if len(s.calls) != 1 {
		t.Fatalf("calls %+v", s.calls)
	}
	c := s.calls[0]
	if c.SLPaise != 42000 || !c.SLDefault || len(c.Targets) != 2 || c.Targets[0] != 47000 || c.Targets[1] != 48000 || !c.TargetsDefault {
		t.Fatalf("call %+v", c)
	}
}
