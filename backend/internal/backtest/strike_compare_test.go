package backtest

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
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

func TestEntryGateRefusesCheapOrNearExpiry(t *testing.T) {
	e := New(Config{Option: OptionModel{Enabled: true, IVPct: 14, RatePct: 6.5, StrikeStep: 50, MinEntryPremium: 5000, MinEntryDTE: 2}})
	monday := time.Date(2026, 10, 5, 10, 0, 0, 0, istLoc) // expiry Tue 6 Oct: 1 day
	wed := time.Date(2026, 9, 30, 10, 0, 0, 0, istLoc)    // expiry Tue 6 Oct: 6 days
	atm := &strategy.Signal{Side: strategy.SideCall, Strike: 22700}
	far := &strategy.Signal{Side: strategy.SideCall, Strike: 23300} // deep OTM: cheap
	if ok, why := e.entryAllowed(atm, model.TFCandle{TS: monday, Close: 2270000}); ok || why == "" {
		t.Fatal("1-day expiry allowed")
	}
	if ok, why := e.entryAllowed(atm, model.TFCandle{TS: wed, Close: 2270000}); !ok {
		t.Fatalf("ATM 6 days out refused: %s", why)
	}
	if ok, _ := e.entryAllowed(far, model.TFCandle{TS: wed, Close: 2270000}); ok {
		t.Fatal("cheap deep-OTM premium allowed")
	}
	off := New(Config{Option: OptionModel{Enabled: true, IVPct: 14, StrikeStep: 50}})
	if ok, _ := off.entryAllowed(atm, model.TFCandle{TS: monday, Close: 2270000}); !ok {
		t.Fatal("gate applied while off")
	}
}

func TestExpiryMinDTERollsMondayToNextWeek(t *testing.T) {
	e := New(Config{Option: OptionModel{Enabled: true, ExpiryMinDTE: 2}})
	monday := time.Date(2026, 10, 5, 10, 0, 0, 0, istLoc)
	if got := e.expiryFor(&Trade{EntryTime: monday}, monday); daysTo(monday, got) != 8 {
		t.Fatalf("Monday expiry %v, want Tue 13 Oct", got)
	}
	wed := time.Date(2026, 9, 30, 10, 0, 0, 0, istLoc)
	if got := e.expiryFor(&Trade{EntryTime: wed}, wed); daysTo(wed, got) != 6 {
		t.Fatalf("Wednesday expiry %v, want Tue 6 Oct", got)
	}
}
