package optionmath

import (
	"math"
	"testing"
	"time"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// Reference: S=100 K=100 T=1 σ=0.2 r=0.05 (Hull).
func TestBlackScholesReference(t *testing.T) {
	if p := Price(100, 100, 1, 0.2, 0.05, true); !near(p, 10.4506, 1e-3) {
		t.Fatalf("call = %v", p)
	}
	c := GreeksAt(100, 100, 1, 0.2, 0.05, true)
	if !near(c.Delta, 0.6368, 1e-3) || !near(c.Gamma, 0.018762, 1e-5) || !near(c.Vega, 0.37524, 1e-4) || !near(c.Theta, -6.4140/365, 1e-4) {
		t.Fatalf("call greeks = %+v", c)
	}
	p := GreeksAt(100, 100, 1, 0.2, 0.05, false)
	if !near(p.Delta, -0.3632, 1e-3) || !near(p.Gamma, c.Gamma, 1e-9) || !near(p.Theta, -1.6579/365, 1e-4) {
		t.Fatalf("put greeks = %+v", p)
	}
}

func TestGreeksAtExpiryOrZeroIV(t *testing.T) {
	g := GreeksAt(100, 90, 0, 0.2, 0.05, true)
	if g.Delta != 1 || g.Gamma != 0 {
		t.Fatalf("expired ITM call = %+v", g)
	}
	if g := GreeksAt(100, 110, 1, 0, 0.05, true); g.Delta != 0 {
		t.Fatalf("zero-IV OTM call = %+v", g)
	}
}

func TestYearsTo(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+30*60)
	exp := time.Date(2026, 10, 6, 0, 0, 0, 0, ist)
	now := time.Date(2026, 10, 6, 9, 30, 0, 0, ist) // 6h before 15:30
	if y := YearsTo(exp, now); !near(y, 6.0/(365*24), 1e-9) {
		t.Fatalf("years = %v", y)
	}
	if y := YearsTo(exp, exp.Add(20*time.Hour)); y != 0 {
		t.Fatalf("past expiry = %v", y)
	}
}

func TestImpliedVolRoundTrip(t *testing.T) {
	for _, call := range []bool{true, false} {
		for _, iv := range []float64{0.08, 0.15, 0.4} {
			for _, k := range []float64{80000, 81000, 82000} {
				p := Price(81234, k, 6.0/365, iv, 0.065, call)
				got, ok := ImpliedVol(p, 81234, k, 6.0/365, 0.065, call)
				if !ok || !near(got, iv, 1e-4) {
					t.Fatalf("call=%v k=%v iv=%v: got %v ok=%v", call, k, iv, got, ok)
				}
			}
		}
	}
}

func TestImpliedVolRejectsUnpriceable(t *testing.T) {
	if _, ok := ImpliedVol(5, 100, 80, 0.1, 0.05, true); ok { // below intrinsic 20
		t.Fatal("price below intrinsic must fail")
	}
	if _, ok := ImpliedVol(0, 100, 100, 0.1, 0.05, true); ok {
		t.Fatal("zero price must fail")
	}
	if _, ok := ImpliedVol(10, 100, 100, 0, 0.05, true); ok {
		t.Fatal("expired must fail")
	}
}
