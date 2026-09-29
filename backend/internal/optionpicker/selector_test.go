package optionpicker

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var ist = time.FixedZone("IST", 5*3600+30*60)

// Wed 30 Sep 2026 11:30 IST; weekly expiries Tue 06 Oct and Tue 13 Oct.
var (
	now    = time.Date(2026, 9, 30, 11, 30, 0, 0, ist)
	exp1   = time.Date(2026, 10, 6, 0, 0, 0, 0, ist)
	exp2   = time.Date(2026, 10, 13, 0, 0, 0, 0, ist)
	rules  = Rules{MaxSpreadPct: 2, MaxQuoteAge: 3 * time.Second, MaxChainAge: 2 * time.Minute, GammaDTE: 1, MaxBuyIV: 25, MinLiquidity: 5000, CostMultiple: 3, RatePct: 6.5}
	spot   = int64(2270000) // 22700.00
	callIn = SingleIntent{Strategy: "T", Option: "CE", DeltaMin: 0.45, DeltaMax: 0.60, MinDTE: 1, MaxThetaPct: 25, MaxGamma: 0.005}
)

func ctr(strike int64, opt string, exp time.Time) Contract {
	return Contract{Token: opt + itoa(strike) + exp.Format("0102"), Symbol: "S" + itoa(strike) + opt, Strike: strike, Option: opt, Expiry: exp, IV: 14, Liquidity: 100000}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// quotes gives every token a fresh tight quote around the Black-Scholes mid.
func quotes(over map[string]Quote) func(string) (Quote, bool) {
	return func(tok string) (Quote, bool) {
		if q, ok := over[tok]; ok {
			return q, true
		}
		return Quote{LTP: 15000, Bid: 14990, Ask: 15010, OI: 50000, At: now}, true
	}
}

func env(q func(string) (Quote, bool)) Env { return Env{Spot: spot, Now: now, ChainAt: now, Quote: q} }

func ladder(opt string, exp time.Time) []Contract {
	var c []Contract
	for k := int64(22400); k <= 23000; k += 50 {
		c = append(c, ctr(k, opt, exp))
	}
	return c
}

func TestSelectSingleClosestToBandMiddle(t *testing.T) {
	p, _, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(quotes(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Strike != 22700 && p.Strike != 22650 { // |delta| nearest 0.525 is at/just below ATM
		t.Fatalf("strike %d delta %.3f", p.Strike, p.Delta)
	}
	if p.Delta < 0.45 || p.Delta > 0.60 || p.DTE != 6 || p.Quote.Bid != 14990 {
		t.Fatalf("pick = %+v", p)
	}
}

func TestSelectSingleRejectsWideSpreadForNextInBand(t *testing.T) {
	best, _, _ := SelectSingle(ladder("CE", exp1), callIn, rules, env(quotes(nil)))
	wide := map[string]Quote{best.Token: {LTP: 15000, Bid: 14500, Ask: 15500, OI: 50000, At: now}}
	p, rej, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(quotes(wide)))
	if err != nil || p.Strike == best.Strike || rej["spread"] != 1 {
		t.Fatalf("pick %d rej %v err %v", p.Strike, rej, err)
	}
}

func TestSelectSingleRefusesWithoutQuotes(t *testing.T) {
	none := func(string) (Quote, bool) { return Quote{}, false }
	_, rej, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(none))
	var r *Refusal
	if !errors.As(err, &r) || rej["quote stale"] == 0 {
		t.Fatalf("err %v rej %v", err, rej)
	}
	stale := func(string) (Quote, bool) { return Quote{Bid: 14990, Ask: 15010, At: now.Add(-4 * time.Second)}, true }
	if _, rej, err := SelectSingle(ladder("CE", exp1), callIn, rules, env(stale)); err == nil || rej["quote stale"] == 0 {
		t.Fatalf("stale quotes accepted: %v", rej)
	}
}

func TestSelectSingleNotStreamed(t *testing.T) {
	c := ladder("CE", exp1)
	for i := range c {
		c[i].Token = ""
	}
	if _, rej, err := SelectSingle(c, callIn, rules, env(quotes(nil))); err == nil || rej["not streamed"] != len(c) {
		t.Fatalf("rej %v err %v", rej, err)
	}
}

func TestSelectSingleNeverSameDay(t *testing.T) {
	expiryDay := time.Date(2026, 10, 6, 10, 0, 0, 0, ist)
	chain := append(ladder("CE", exp1), ladder("CE", exp2)...)
	in := callIn
	in.MinDTE = 0
	// quotes(nil) stamps quotes at the package-level `now`; this test moves
	// the clock to expiryDay, so give it quotes fresh at that instant.
	fresh := func(string) (Quote, bool) {
		return Quote{LTP: 15000, Bid: 14990, Ask: 15010, OI: 50000, At: expiryDay}, true
	}
	e := env(fresh)
	e.Now, e.ChainAt = expiryDay, expiryDay
	p, _, err := SelectSingle(chain, in, rules, e)
	if err != nil || !sameDate(p.Expiry, exp2) {
		t.Fatalf("expiry %v err %v, want 13 Oct", p.Expiry, err)
	}
}

func TestSelectSingleMinDTE(t *testing.T) {
	monday := time.Date(2026, 10, 5, 10, 0, 0, 0, ist)
	in := callIn
	in.MinDTE = 2
	// quotes(nil) stamps quotes at the package-level `now`; this test moves
	// the clock to monday, so give it quotes fresh at that instant.
	fresh := func(string) (Quote, bool) {
		return Quote{LTP: 15000, Bid: 14990, Ask: 15010, OI: 50000, At: monday}, true
	}
	e := env(fresh)
	e.Now, e.ChainAt = monday, monday
	p, _, err := SelectSingle(append(ladder("CE", exp1), ladder("CE", exp2)...), in, rules, e)
	if err != nil || !sameDate(p.Expiry, exp2) || p.DTE != 8 {
		t.Fatalf("pick %+v err %v", p, err)
	}
}

func TestSelectSingleStaleChainAndNoSpot(t *testing.T) {
	e := env(quotes(nil))
	e.ChainAt = now.Add(-3 * time.Minute)
	if _, _, err := SelectSingle(ladder("CE", exp1), callIn, rules, e); err == nil || !strings.Contains(err.Error(), "greeks stale") {
		t.Fatalf("err %v", err)
	}
	e = env(quotes(nil))
	e.Spot = 0
	if _, _, err := SelectSingle(ladder("CE", exp1), callIn, rules, e); err == nil || err.Error() != "no spot" {
		t.Fatalf("err %v", err)
	}
}

func TestSelectSingleIVAndLiquidityAndCost(t *testing.T) {
	c := ladder("CE", exp1)
	for i := range c {
		c[i].IV = 30
	}
	if _, rej, err := SelectSingle(c, callIn, rules, env(quotes(nil))); err == nil || rej["iv"] == 0 {
		t.Fatalf("high IV accepted: %v", rej)
	}
	thin := func(string) (Quote, bool) { return Quote{Bid: 14990, Ask: 15010, OI: 10, At: now}, true }
	c = ladder("CE", exp1)
	for i := range c {
		c[i].Liquidity = 10
	}
	if _, rej, err := SelectSingle(c, callIn, rules, env(thin)); err == nil || rej["liquidity"] == 0 {
		t.Fatalf("thin contract accepted: %v", rej)
	}
	in := callIn
	in.TargetMove = 1000 // 10 points × 0.5 delta = 500 paise < 3 × 20 spread? no: 500 ≥ 60 passes
	if _, _, err := SelectSingle(ladder("CE", exp1), in, rules, env(quotes(nil))); err != nil {
		t.Fatalf("cost rule too strict: %v", err)
	}
	in.TargetMove = 100 // 1 point × 0.5 = 50 paise < 3 × 20 = 60 → every candidate fails cost
	if _, rej, err := SelectSingle(ladder("CE", exp1), in, rules, env(quotes(nil))); err == nil || rej["cost"] == 0 {
		t.Fatalf("cost rule not applied: %v", rej)
	}
}

// A contract whose theta exceeds the intent's cap is rejected with key
// "theta". The cap lives on SingleIntent (not Rules): SR and RANGE want
// different theta tolerances.
func TestSelectSingleRejectsTheta(t *testing.T) {
	in := callIn
	in.MaxThetaPct = 5 // real BS theta here (~15.4/day on a ~150 premium) far exceeds 5%
	if _, rej, err := SelectSingle(ladder("CE", exp1), in, rules, env(quotes(nil))); err == nil || rej["theta"] == 0 {
		t.Fatalf("high-theta contract accepted: rej=%v err=%v", rej, err)
	}
	// MaxThetaPct 0 (off) must not reject on theta.
	in.MaxThetaPct = 0
	if _, rej, err := SelectSingle(ladder("CE", exp1), in, rules, env(quotes(nil))); err != nil || rej["theta"] != 0 {
		t.Fatalf("theta cap off still rejected: rej=%v err=%v", rej, err)
	}
}

func sameDate(a, b time.Time) bool {
	a, b = a.In(ist), b.In(ist)
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
