package portfolio

import "testing"

func shortTrade(action string, qty, price int64) Trade {
	return Trade{Token: "CE1", Exchange: "NFO", Action: action, Qty: qty, Price: price, StrategyName: "IC", Short: true}
}

func TestShortLeg_SellOpensBuyCloses(t *testing.T) {
	p := NewPnLTracker()
	if r := p.RecordTrade(shortTrade("SELL", 75, 10000)); r != 0 {
		t.Fatalf("opening a short realizes %d, want 0", r)
	}
	if ce := p.costBasis["NFO:CE1"]; ce.Qty != -75 || ce.AvgPrice != 10000 {
		t.Fatalf("cost basis after short open = %+v", ce)
	}
	// Premium decays 100.00 → 60.00: profit 40.00 × 75.
	r := p.RecordTrade(shortTrade("BUY", 75, 6000))
	if want := int64(4000 * 75); r != want {
		t.Fatalf("realized %d, want %d", r, want)
	}
	if p.costBasis["NFO:CE1"].Qty != 0 {
		t.Error("short not flat after buy-to-close")
	}
	if p.GetStrategyDailyPnL("IC") != 4000*75 {
		t.Errorf("strategy pnl = %d", p.GetStrategyDailyPnL("IC"))
	}
	if s := p.GetDailyStats(); s.Wins != 1 {
		t.Errorf("wins = %d", s.Wins)
	}
}

func TestShortLeg_LossAndUnrealized(t *testing.T) {
	p := NewPnLTracker()
	p.RecordTrade(shortTrade("SELL", 50, 10000))
	prices := map[string]int64{"NFO:CE1": 13000}
	if u := p.GetUnrealizedPnL(prices); u != -3000*50 {
		t.Fatalf("unrealized = %d, want %d", u, -3000*50)
	}
	if s := p.GetSummary(prices); s.OpenPositions != 1 || s.UnrealizedPnL != -3000*50 {
		t.Fatalf("summary = %+v", s)
	}
	if r := p.RecordTrade(shortTrade("BUY", 50, 13000)); r != -3000*50 {
		t.Fatalf("realized = %d", r)
	}
}

func TestShortLeg_BuyToCloseWithoutOpenRealizesNothing(t *testing.T) {
	p := NewPnLTracker()
	if r := p.RecordTrade(shortTrade("BUY", 50, 13000)); r != 0 {
		t.Fatalf("close with no short realized %d", r)
	}
	if p.costBasis["NFO:CE1"].Qty != 0 {
		t.Error("buy-to-close must not open a long")
	}
}

func TestLongPathUnchangedBySellWithoutShortFlag(t *testing.T) {
	p := NewPnLTracker()
	// Existing behaviour: a SELL with nothing held realizes 0 and opens nothing.
	if r := p.RecordTrade(Trade{Token: "X", Exchange: "NFO", Action: "SELL", Qty: 10, Price: 100}); r != 0 {
		t.Fatalf("realized %d", r)
	}
	if p.costBasis["NFO:X"].Qty != 0 {
		t.Error("unflagged SELL must not open a short")
	}
}
