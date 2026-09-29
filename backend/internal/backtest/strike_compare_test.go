package backtest

import (
	"testing"
	"time"

	"trading-systemv1/internal/strategy"
)

func TestCompareStrikesPricesSameTradesThreeWays(t *testing.T) {
	e := New(Config{StrategyType: "nifty50_sr",
		Option: OptionModel{Enabled: true, IVPct: 14, RatePct: 6.5, SlippageBps: 50, SlippageMinPsa: 50, StrikeStep: 50}})
	in := time.Date(2026, 9, 30, 10, 0, 0, 0, istLoc) // Wed, expiry Tue 6 Oct
	win := Trade{Side: strategy.SideCall, EntryTime: in, ExitTime: in.Add(45 * time.Minute),
		EntryPrice: 2270000, ExitPrice: 2278000, Strike: 22700, TargetMove: 8000}
	loss := win
	loss.ExitPrice = 2265000
	vs, err := e.CompareStrikes([]Trade{win, loss})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0].Trades != 2 {
		t.Fatalf("variants = %+v", vs)
	}
	if vs[0].Wins != 1 || vs[0].PnLPaise == 0 {
		t.Fatalf("signal variant = %+v", vs[0])
	}
	for _, v := range vs[1:] {
		if v.Trades+v.Refused != 2 {
			t.Fatalf("%s accounted %d+%d trades, want 2", v.Name, v.Trades, v.Refused)
		}
	}
	// The delta pick stays near ATM for an ATM band.
	if s, _, ok := e.pickStrike(&win, "delta"); ok && (s < 22600 || s > 22800) {
		t.Fatalf("delta pick %d far from ATM", s)
	}
}

func TestCompareStrikesNeedsOptionModel(t *testing.T) {
	if _, err := New(Config{}).CompareStrikes(nil); err == nil {
		t.Fatal("ran without the option model")
	}
}
