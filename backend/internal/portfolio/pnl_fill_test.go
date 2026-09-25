package portfolio

import "testing"

func trade(action string, price int64) Trade {
	return Trade{StrategyName: "S", Exchange: "NFO", Token: "T1", Action: action, Qty: 75, Price: price}
}

func TestCorrectLastFill_BuyFillShiftsCostBasis(t *testing.T) {
	p := NewPnLTracker()
	p.RecordTrade(trade("BUY", 10000))
	if _, ok := p.CorrectLastFill("S", "NFO", "T1", "BUY", 10100); !ok {
		t.Fatal("correction not applied")
	}
	got := p.RecordTrade(trade("SELL", 10500))
	if want := int64(400 * 75); got != want {
		t.Fatalf("realized %d, want %d (sell 10500 - fill 10100)", got, want)
	}
}

func TestCorrectLastFill_SellFillAdjustsRealized(t *testing.T) {
	p := NewPnLTracker()
	p.RecordTrade(trade("BUY", 10000))
	p.RecordTrade(trade("SELL", 10500)) // recorded +500/unit at signal LTP
	delta, ok := p.CorrectLastFill("S", "NFO", "T1", "SELL", 10450)
	if !ok || delta != -50*75 {
		t.Fatalf("delta %d ok=%v, want %d", delta, ok, -50*75)
	}
	if got, want := p.GetRealizedPnL(), int64(450*75); got != want {
		t.Fatalf("realized %d, want %d", got, want)
	}
	if got, want := p.GetStrategyDailyPnL("S"), int64(450*75); got != want {
		t.Fatalf("strategy daily %d, want %d", got, want)
	}
}

func TestCorrectLastFill_BuyCorrectionAfterPositionClosed(t *testing.T) {
	p := NewPnLTracker()
	p.RecordTrade(trade("BUY", 10000))
	p.RecordTrade(trade("SELL", 10500))
	p.CorrectLastFill("S", "NFO", "T1", "BUY", 10100) // fill report arrives after the exit
	if got, want := p.GetRealizedPnL(), int64(400*75); got != want {
		t.Fatalf("realized %d, want %d", got, want)
	}
}

func TestCorrectLastFill_NoMatchingTrade(t *testing.T) {
	p := NewPnLTracker()
	if _, ok := p.CorrectLastFill("S", "NFO", "T1", "BUY", 10100); ok {
		t.Fatal("must report no match")
	}
}

func TestCorrectLastFill_UpdatesTradeRecord(t *testing.T) {
	p := NewPnLTracker()
	p.RecordTrade(trade("BUY", 10000))
	p.CorrectLastFill("S", "NFO", "T1", "BUY", 10100)
	tr := p.GetTrades()
	if tr[len(tr)-1].Price != 10100 {
		t.Fatalf("trade record price %d, want 10100", tr[len(tr)-1].Price)
	}
}

func TestSnapshot_KeepsStrategyDailyPnL(t *testing.T) {
	p := NewPnLTracker()
	p.RecordTrade(trade("BUY", 10000))
	p.RecordTrade(trade("SELL", 10500))
	data, err := p.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	q := NewPnLTracker()
	if err := q.RestorePnL(data); err != nil {
		t.Fatal(err)
	}
	if got := q.GetStrategyDailyPnL("S"); got != 500*75 {
		t.Fatalf("profit-cap input lost on restart: got %d", got)
	}
}
