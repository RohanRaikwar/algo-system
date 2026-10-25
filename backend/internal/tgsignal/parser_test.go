package tgsignal

import (
	"errors"
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		und, opt string
		strike   int64
		trig, sl int64
		targets  []int64
	}{
		{"user example", "BUY NIFTY 24500 CE\nABOVE 120\nTARGET 140 / 160\nSL 100",
			"NIFTY", "CE", 24500, 12000, 10000, []int64{14000, 16000}},
		{"one line lower case", "buy nifty 24300 pe above 95.5 tgt 110-130 sl 80",
			"NIFTY", "PE", 24300, 9550, 8000, []int64{11000, 13000}},
		{"emoji and expiry", "🔥 BUY SENSEX 28 OCT 81000 CE 🔥\nNear : 210\nSL: 180\nTarget: 240, 270, 300+",
			"SENSEX", "CE", 81000, 21000, 18000, []int64{24000, 27000, 30000}},
		{"at sign", "NIFTY 24500 CALL BUY @ 120 SL 100 TGT 150",
			"NIFTY", "CE", 24500, 12000, 10000, []int64{15000}},
		{"indian operator live", "F&O | ⚡ INTRADAY\n\n🟢 BUY NIFTY PE 22750\n\n💰 Entry: Near Above 191.00\n🎯 Target:1st: ₹202.00 , 2ND:221 \n🛑 Stop Loss: ₹182.00\n\n\nWait for entry \n\nCandle closing ka wait karna \n\nDisclaimer: Investments in securities markets are subject to market risks.",
			"NIFTY", "PE", 22750, 19100, 18200, []int64{20200, 22100}},
		{"stop loss words", "BUY NIFTY 24500 PUT ABOVE 120 STOP LOSS 100",
			"NIFTY", "PE", 24500, 12000, 10000, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := Parse(c.text)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if s.Underlying != c.und || s.OptionType != c.opt || s.Strike != c.strike ||
				s.TriggerPaise != c.trig || s.SLPaise != c.sl || !slices.Equal(s.Targets, c.targets) {
				t.Fatalf("got %+v", s)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	notCalls := []string{
		"Good morning traders",
		"TARGET 1 DONE 140 🎯",
		"EXIT NIFTY 24500 CE at 135",
	}
	for _, text := range notCalls {
		if _, err := Parse(text); !errors.Is(err, ErrNotACall) {
			t.Errorf("%q: err %v, want ErrNotACall", text, err)
		}
	}
	bad := []string{
		"BUY NIFTY 24500 CE ABOVE 120 TARGET 140", // no SL
		"BUY NIFTY 24500 CE ABOVE 120 SL 130",     // SL above entry
		"BUY BANKNIFTY 52000 CE ABOVE 300 SL 250", // unsupported
		"BUY NIFTY 24500 CE SL 100",               // no entry
	}
	for _, text := range bad {
		if _, err := Parse(text); err == nil || errors.Is(err, ErrNotACall) {
			t.Errorf("%q: err %v, want a refusal", text, err)
		}
	}
}

func TestToPaise(t *testing.T) {
	for in, want := range map[string]int64{"120": 12000, "120.5": 12050, "0.05": 5, "99.999": 9999} {
		if got := toPaise(in); got != want {
			t.Errorf("toPaise(%q) = %d, want %d", in, got, want)
		}
	}
}
