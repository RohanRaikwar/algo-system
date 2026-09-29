package optionpicker

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestQuoteBookKeepsLastQuoteAndFreshness(t *testing.T) {
	b := NewQuoteBook(3 * time.Second)
	t0 := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	b.Update(model.Tick{Token: "40712", Price: 20300, BestBid: 20280, BestAsk: 20320, OI: 9, QuoteTS: t0})
	b.Update(model.Tick{Token: "40712", Price: 20310}) // LTP-only packet keeps the last bid/ask
	q, ok := b.Get("40712")
	if !ok || q.LTP != 20310 || q.Bid != 20280 || q.Ask != 20320 || q.OI != 9 || !q.At.Equal(t0) {
		t.Fatalf("quote = %+v", q)
	}
	if bid, ask, ok := b.BidAsk("40712", t0.Add(3*time.Second)); !ok || bid != 20280 || ask != 20320 {
		t.Fatal("fresh quote rejected")
	}
	if _, _, ok := b.BidAsk("40712", t0.Add(3*time.Second+time.Millisecond)); ok {
		t.Fatal("stale quote accepted")
	}
	if _, _, ok := b.BidAsk("unknown", t0); ok {
		t.Fatal("unknown token accepted")
	}
}

func BenchmarkQuoteBookUpdate(b *testing.B) {
	qb := NewQuoteBook(3 * time.Second)
	tk := model.Tick{Token: "40712", Price: 20300, BestBid: 20280, BestAsk: 20320, QuoteTS: time.Now()}
	qb.Update(tk)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qb.Update(tk)
	}
}
