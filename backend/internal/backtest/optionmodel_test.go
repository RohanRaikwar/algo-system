package backtest

import (
	"math"
	"testing"
	"time"

	"trading-systemv1/internal/optionmath"
	"trading-systemv1/internal/strategy"
)

func TestBSPutCallParity(t *testing.T) {
	s, k, y, iv, r := 24000.0, 24000.0, 4.0/365, 0.13, 0.065
	c, p := optionmath.Price(s, k, y, iv, r, true), optionmath.Price(s, k, y, iv, r, false)
	if diff := c - p - (s - k*math.Exp(-r*y)); math.Abs(diff) > 1e-6 {
		t.Fatalf("parity off by %f", diff)
	}
	// ATM ≈ 0.4 × S × σ × √T ≈ 131, plus rate carry ≈ 8, for 4 DTE at 13% IV.
	if c < 125 || c > 150 {
		t.Fatalf("ATM call %f not in a sane range", c)
	}
}

func TestWeeklyExpiryNeverSameDay(t *testing.T) {
	fri := time.Date(2026, 9, 25, 10, 0, 0, 0, istLoc)
	if got := weeklyExpiry(fri).Format("2006-01-02 15:04"); got != "2026-09-29 15:30" {
		t.Fatalf("Friday → %s", got)
	}
	tue := time.Date(2026, 9, 29, 10, 0, 0, 0, istLoc)
	if got := weeklyExpiry(tue).Format("2006-01-02"); got != "2026-10-06" {
		t.Fatalf("Tuesday → %s (must skip same-day expiry)", got)
	}
}

func TestPremiumThetaAndDelta(t *testing.T) {
	m := OptionModel{Enabled: true, IVPct: 13, RatePct: 6.5}
	morning := time.Date(2026, 9, 24, 10, 0, 0, 0, istLoc) // Thu, 5 DTE
	later := morning.Add(4 * time.Hour)
	c0 := m.premium(strategy.SideCall, 24000, 2400000, morning, 0)
	c1 := m.premium(strategy.SideCall, 24000, 2400000, later, 0)
	if c1 >= c0 {
		t.Fatalf("no time decay: %d → %d", c0, c1)
	}
	up := m.premium(strategy.SideCall, 24000, 2405000, morning, 0) // +50 pts
	if d := float64(up-c0) / 5000; d < 0.4 || d > 0.65 {
		t.Fatalf("ATM delta ≈ %f, want ~0.5", d)
	}
	pe := m.premium(strategy.SidePut, 24000, 2405000, morning, 0)
	if pe >= m.premium(strategy.SidePut, 24000, 2400000, morning, 0) {
		t.Fatal("put must lose value as spot rises")
	}
}
