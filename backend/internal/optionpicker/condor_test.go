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

// richQuotes: every leg bid 3990 / ask 4010 (credit = −40 paise, a debit).
func richQuotes(tok string) (Quote, bool) {
	return Quote{Bid: 3990, Ask: 4010, OI: 50000, At: now}, true
}

// distanceQuotes prices every contract in chain by distance from spot
// (22700): closer-to-money strikes (the shorts a condor sells) quote higher
// than farther-out strikes (the wings it buys), so a short leg's bid always
// beats a wing's ask 100 points further out — a real short condor's credit
// shape — with a tight 20-paise spread throughout.
func distanceQuotes(chain []Contract) func(string) (Quote, bool) {
	byToken := make(map[string]int64, len(chain))
	for _, c := range chain {
		if c.Token != "" {
			byToken[c.Token] = c.Strike
		}
	}
	spotPts := spot / 100
	return func(tok string) (Quote, bool) {
		strike, ok := byToken[tok]
		if !ok {
			return Quote{}, false
		}
		d := strike - spotPts
		if d < 0 {
			d = -d
		}
		price := int64(6000) - 6*d
		return Quote{Bid: price - 10, Ask: price + 10, OI: 50000, At: now}, true
	}
}

func TestSelectCondorAnchorsAtEdgesWithDeltaCap(t *testing.T) {
	in := condorIn
	in.MinCreditPct = 0 // credit floor has its own test; this test only checks leg placement
	chain := condorChain()
	cp, _, err := SelectCondor(chain, in, rules, env(distanceQuotes(chain)))
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
	// ShortCE 23050 (d=350 → bid 3890), LongCE 23150 (d=450 → ask 3310),
	// ShortPE 22450 (d=250 → bid 4490), LongPE 22350 (d=350 → ask 3910):
	// credit = (3890+4490) − (3310+3910) = 1160 paise.
	if cp.Credit != 1160 {
		t.Fatalf("credit = %d", cp.Credit)
	}
}

func TestSelectCondorRefusesThinCredit(t *testing.T) {
	in := condorIn
	in.MinCreditPct = 50 // needs 5000 paise; richQuotes gives a debit
	_, rej, err := SelectCondor(condorChain(), in, rules, env(richQuotes))
	var r *Refusal
	if !errors.As(err, &r) || rej["credit"] != 1 {
		t.Fatalf("err %v rej %v", err, rej)
	}
}

func TestSelectCondorRefusesDebitEvenWithoutFloor(t *testing.T) {
	in := condorIn
	in.MinCreditPct = 0 // no floor at all — a short condor must still never be a debit
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
	cp, _, err := SelectCondor(chain, in, rules, env(distanceQuotes(chain)))
	if err != nil || cp.ShortCE.Strike != 22950 {
		t.Fatalf("short CE %d err %v, want 22950", cp.ShortCE.Strike, err)
	}
}
