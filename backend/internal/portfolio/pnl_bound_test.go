package portfolio

import (
	"testing"
	"time"
)

// The trade list in the snapshot stays bounded; the total count survives.
func TestPnLTradesBounded(t *testing.T) {
	p := NewPnLTracker()
	for i := 0; i < 1000; i++ {
		p.RecordTrade(Trade{StrategyName: "S", Token: "1", Exchange: "NFO", Action: "BUY", Qty: 1, Price: 100, Timestamp: time.Now()})
		p.RecordTrade(Trade{StrategyName: "S", Token: "1", Exchange: "NFO", Action: "SELL", Qty: 1, Price: 110, Timestamp: time.Now()})
	}
	if n := len(p.GetTrades()); n > 2*tradesKept {
		t.Fatalf("kept %d trades, want at most %d", n, 2*tradesKept)
	}
	if got := p.GetSummary(nil).TotalTrades; got != 2000 {
		t.Fatalf("total trades %d, want 2000", got)
	}
	snap, err := p.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r := NewPnLTracker()
	if err := r.RestorePnL(snap); err != nil {
		t.Fatal(err)
	}
	if got := r.GetSummary(nil).TotalTrades; got != 2000 || r.realizedPnL != p.realizedPnL {
		t.Fatalf("restored total %d realized %d", got, r.realizedPnL)
	}
	// The last fill can still be corrected.
	if _, ok := p.CorrectLastFill("S", "NFO", "1", "SELL", 111); !ok {
		t.Fatal("last fill not found")
	}
}
