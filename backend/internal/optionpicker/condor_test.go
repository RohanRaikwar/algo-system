package optionpicker

import (
	"errors"
	"testing"
)

var condorIn = CondorIntent{Strategy: "IC", ShortCEAtLeast: 22900, ShortPEAtMost: 22500, MaxShortDelta: 0.25, WingWidth: 100, MinDTE: 1, MinCreditPct: 10}

func condorChain() []Contract {
	c := append(ladder("CE", exp1), ladder("PE", exp1)...)
	// 23150 CE is the wing for the 23050 short: at spot 22700, IV 14%, 6 DTE,
	// real Black-Scholes delta only drops under the default 0.25 cap at
	// strike 23050 (22900/22950/23000 are all >0.25), so its wing must be
	// streamed for the default-cap anchor test to find a leg.
	return append(c, ctr(23050, "CE", exp1), ctr(23100, "CE", exp1), ctr(23150, "CE", exp1), ctr(22350, "PE", exp1), ctr(22300, "PE", exp1))
}

// richQuotes: every leg bid 3990 / ask 4010 (credit = −40 paise).
func richQuotes(tok string) (Quote, bool) {
	return Quote{Bid: 3990, Ask: 4010, OI: 50000, At: now}, true
}

func TestSelectCondorAnchorsAtEdgesWithDeltaCap(t *testing.T) {
	in := condorIn
	in.MinCreditPct = 0 // flat test quotes give a negative credit; the credit rule has its own test
	cp, _, err := SelectCondor(condorChain(), in, rules, env(richQuotes))
	if err != nil {
		t.Fatal(err)
	}
	if cp.ShortCE.Strike < 22900 || cp.LongCE.Strike != cp.ShortCE.Strike+100 ||
		cp.ShortPE.Strike > 22500 || cp.LongPE.Strike != cp.ShortPE.Strike-100 {
		t.Fatalf("legs = %d/%d %d/%d", cp.ShortCE.Strike, cp.LongCE.Strike, cp.ShortPE.Strike, cp.LongPE.Strike)
	}
	if cp.ShortCE.Delta > 0.25 || cp.ShortPE.Delta < -0.25 {
		t.Fatalf("short deltas %.3f %.3f", cp.ShortCE.Delta, cp.ShortPE.Delta)
	}
	if cp.Credit != (3990+3990)-(4010+4010) {
		t.Fatalf("credit = %d", cp.Credit)
	}
}

func TestSelectCondorRefusesThinCredit(t *testing.T) {
	in := condorIn
	in.MinCreditPct = 50 // needs 5000 paise; richQuotes gives negative credit
	_, rej, err := SelectCondor(condorChain(), in, rules, env(richQuotes))
	var r *Refusal
	if !errors.As(err, &r) || rej["credit"] != 1 {
		t.Fatalf("err %v rej %v", err, rej)
	}
}

func TestSelectCondorMovesOutWhenWingNotStreamed(t *testing.T) {
	chain := condorChain()
	for i := range chain {
		if chain[i].Option == "CE" && chain[i].Strike == 23000 {
			chain[i].Token = "" // wing of the 22900 short
		}
	}
	in := condorIn
	in.MinCreditPct = 0
	in.MaxShortDelta = 0.5 // default 0.25 cap already rejects the 22900/22950 CE shorts on delta at this spot/IV/DTE; relaxed to isolate the wing-not-streamed rule
	cp, _, err := SelectCondor(chain, in, rules, env(richQuotes))
	if err != nil || cp.ShortCE.Strike != 22950 {
		t.Fatalf("short CE %d err %v, want 22950", cp.ShortCE.Strike, err)
	}
}
