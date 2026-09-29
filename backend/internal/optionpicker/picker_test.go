package optionpicker

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

func TestPickerJoinsChainUniverseAndQuotes(t *testing.T) {
	src := &fakeSource{out: ladder("CE", exp1)}
	for i := range src.out {
		src.out[i].Token, src.out[i].Symbol = "", "" // the chain has no tokens
	}
	res, sub := &fakeRes{}, &fakeSub{}
	p := New(Config{Rules: rules, LadderStrikes: 4, StrikeStep: 50, ChainEvery: 15 * time.Second, ChainBackoff: time.Minute, SearchEvery: time.Second}, src, res, sub)
	p.OnTick(model.Tick{Token: SpotToken, Price: spot})
	p.chain.Refresh(now)
	p.refreshUniverseAt(now)
	for _, tok := range sub.got {
		p.OnTick(model.Tick{Token: tok, Price: 15000, BestBid: 14990, BestAsk: 15010, OI: 50000, QuoteTS: now})
	}
	pick, _, err := p.PickSingle(callIn, now)
	if err != nil {
		t.Fatal(err)
	}
	if pick.Token == "" || pick.Symbol == "" || pick.Quote.Ask != 15010 {
		t.Fatalf("pick = %+v", pick)
	}
	if st := p.Status(now); st.Streamed != len(sub.got) || st.Spot != spot || !st.ChainAt.Equal(now) {
		t.Fatalf("status = %+v", st)
	}
}

func TestPickerNewSessionOnDateChange(t *testing.T) {
	src := &fakeSource{out: ladder("CE", exp1)}
	for i := range src.out {
		src.out[i].Token, src.out[i].Symbol = "", ""
	}
	res, sub := &fakeRes{}, &fakeSub{}
	p := New(Config{Rules: rules, LadderStrikes: 4, StrikeStep: 50, ChainEvery: 15 * time.Second, ChainBackoff: time.Minute, SearchEvery: time.Second}, src, res, sub)
	p.OnTick(model.Tick{Token: SpotToken, Price: spot})
	p.chain.Refresh(now)

	p.refreshUniverseAt(now)
	n := len(sub.got)
	if n == 0 {
		t.Fatalf("expected some tokens subscribed, got %d", n)
	}

	nextDay := now.Add(24 * time.Hour)
	p.refreshUniverseAt(nextDay) // date change: must re-subscribe the whole ladder
	if len(sub.got) != 2*n {
		t.Fatalf("expected re-subscribe of %d tokens on date change, sub.got now %d: %v", 2*n, len(sub.got), sub.got)
	}

	p.refreshUniverseAt(nextDay.Add(time.Second)) // same day again: no new subscribes
	if len(sub.got) != 2*n {
		t.Fatalf("same-day refresh added subscriptions: %d", len(sub.got))
	}
}
