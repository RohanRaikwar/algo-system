package stratengine

import (
	"fmt"
	"math"
	"testing"
	"time"

	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/underlying"
)

// fakeChainMaster lists strikes lo..hi (step) on exps for one name/segment.
type fakeChainMaster struct {
	name, seg string
	exps      []time.Time
	lo, hi    int64
	step      int64
}

func (f fakeChainMaster) OptionExpiriesOn(name, seg string, _ time.Time) []time.Time {
	if name != f.name || seg != f.seg {
		return nil
	}
	return f.exps
}

func (f fakeChainMaster) FindOption(name, seg string, exp time.Time, strike int64, opt string) *instruments.Instrument {
	if name != f.name || seg != f.seg || strike < f.lo || strike > f.hi || strike%f.step != 0 {
		return nil
	}
	tok := fmt.Sprintf("%d%s%s", strike, opt, exp.Format("0102"))
	return &instruments.Instrument{Token: tok, Symbol: name + tok}
}

func chainAt(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, markethours.IST)
}

func TestBuildOptChain_BankNiftyMonthly(t *testing.T) {
	p := underlying.Get("BANKNIFTY")
	nov, dec := chainAt(2026, 11, 24, 0, 0), chainAt(2026, 12, 29, 0, 0)
	im := fakeChainMaster{name: "BANKNIFTY", seg: "NFO", exps: []time.Time{nov, dec}, lo: 50000, hi: 60000, step: 100}
	greeks := []instruments.OptionContract{{Token: "57000CE1229", IV: 0.14, Delta: 0.51}}

	// Expiry day before 15:30: November's contracts.
	v, ok := buildOptChain(p, im, 5703000, 2, greeks, chainAt(2026, 11, 24, 10, 0))
	if !ok || v.Expiry != "2026-11-24" || v.ATM != 57000 || len(v.Rows) != 5 {
		t.Fatalf("view %+v ok=%v", v, ok)
	}
	// After close: December's, with greeks from the cached chain.
	v, ok = buildOptChain(p, im, 5703000, 2, greeks, chainAt(2026, 11, 24, 15, 31))
	if !ok || v.Expiry != "2026-12-29" || v.Exchange != "NFO" || v.Index != "NSE:99926009" {
		t.Fatalf("view %+v", v)
	}
	mid := v.Rows[2]
	if mid.Strike != 57000 || mid.CE == nil || mid.CE.Token != "57000CE1229" || math.Abs(mid.CE.IV-14) > 1e-9 || mid.CE.Delta != 0.51 || mid.PE == nil {
		t.Fatalf("ATM row %+v / %+v", mid, mid.CE)
	}
	if v.Rows[0].Strike != 56800 || v.Rows[4].Strike != 57200 {
		t.Fatalf("strikes %d..%d", v.Rows[0].Strike, v.Rows[4].Strike)
	}
}

func TestBuildOptChain_SensexAndEdges(t *testing.T) {
	p := underlying.Get("SENSEX")
	exp := chainAt(2026, 11, 26, 0, 0)
	im := fakeChainMaster{name: "SENSEX", seg: "BFO", exps: []time.Time{exp}, lo: 81000, hi: 81300, step: 100}
	now := chainAt(2026, 11, 25, 10, 0)
	v, ok := buildOptChain(p, im, 8124900, 3, nil, now) // ATM 81200, rows beyond 81300 unlisted
	if !ok || v.ATM != 81200 || len(v.Rows) != 4 || v.Exchange != "BFO" {
		t.Fatalf("view %+v", v)
	}
	if _, ok := buildOptChain(p, im, 0, 3, nil, now); ok {
		t.Fatal("built without spot")
	}
	if _, ok := buildOptChain(underlying.Primary(), im, 2600000, 3, nil, now); ok {
		t.Fatal("NIFTY built from SENSEX master")
	}
}
