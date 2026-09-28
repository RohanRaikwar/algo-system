package stratengine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/portfolio"
	"trading-systemv1/internal/strategy"
)

type fakeResolver struct {
	fail map[int64]bool
}

func (f fakeResolver) ResolveStrike(_ time.Time, strike int64, opt string) (orderexec.StrikeInfo, error) {
	if f.fail[strike] {
		return orderexec.StrikeInfo{}, errors.New("not listed")
	}
	tok := opt + "-" + time.Unix(strike, 0).UTC().Format("150405")
	return orderexec.StrikeInfo{Token: tok, Symbol: "NIFTY" + tok, Strike: strike}, nil
}

type fakeBasket struct {
	mu        sync.Mutex
	tokens    map[string]string
	cancelled string
	minCredit int64
}

func (b *fakeBasket) MinCredit() int64 { return b.minCredit }

func (b *fakeBasket) SetLegTokens(t map[string]string) { b.mu.Lock(); b.tokens = t; b.mu.Unlock() }
func (b *fakeBasket) CancelBasket(r string)            { b.mu.Lock(); b.cancelled = r; b.mu.Unlock() }

func condorBasket(action strategy.Action) strategy.Signal {
	return strategy.Signal{
		StrategyName: "NIFTY50_RANGE_IC", Action: action, Side: strategy.SideCall,
		Token: "99926000", Exchange: "NSE", Qty: 1,
		Legs: []strategy.LegSpec{
			{Leg: strategy.LegLongCE, Strike: 24300, OptionType: "CE"},
			{Leg: strategy.LegShortCE, Strike: 24200, OptionType: "CE", Short: true},
			{Leg: strategy.LegLongPE, Strike: 23700, OptionType: "PE"},
			{Leg: strategy.LegShortPE, Strike: 23800, OptionType: "PE", Short: true},
		},
	}
}

func TestExpandLegSignalsEntryOrderAndTokens(t *testing.T) {
	svc := &Service{legResolver: fakeResolver{}}
	legs, err := svc.expandLegSignals(condorBasket(strategy.ActionBuy), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 4 {
		t.Fatalf("legs = %d", len(legs))
	}
	for i, l := range legs {
		if l.Leg == "" || l.FNOToken == "" || len(l.Legs) != 0 {
			t.Errorf("leg %d not expanded: %+v", i, l)
		}
		if wantShort := i >= 2; l.Short != wantShort {
			t.Errorf("entry order: leg %d (%s) short=%v — wings must go first", i, l.Leg, l.Short)
		}
	}
	if legs[0].Side != strategy.SideCall || legs[1].Side != strategy.SidePut {
		t.Errorf("sides: %s %s", legs[0].Side, legs[1].Side)
	}
}

func TestExpandLegSignalsExitShortsFirst(t *testing.T) {
	svc := &Service{legResolver: fakeResolver{fail: map[int64]bool{24300: true}}}
	// Exits never resolve strikes, so a resolver failure can't block them.
	legs, err := svc.expandLegSignals(condorBasket(strategy.ActionExit), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !legs[0].Short || !legs[1].Short || legs[2].Short || legs[3].Short {
		t.Fatalf("exit order wrong: %+v", legs)
	}
}

func TestExpandLegSignalsAllOrNothing(t *testing.T) {
	svc := &Service{legResolver: fakeResolver{fail: map[int64]bool{23700: true}}}
	if legs, err := svc.expandLegSignals(condorBasket(strategy.ActionBuy), time.Now()); err == nil || legs != nil {
		t.Fatalf("want error and no legs, got %v %+v", err, legs)
	}
}

// marketOpenTime is a regular trading session minute (Tuesday 10:00 IST).
func marketOpenTime(t *testing.T) time.Time {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, markethours.IST)
	if !markethours.IsMarketOpen(now) {
		t.Skip("2026-09-29 is a market holiday in this calendar")
	}
	return now
}

func TestHandleBasketEntryDispatchesPricedLegs(t *testing.T) {
	now := marketOpenTime(t)
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
	basket := &fakeBasket{}
	var mu sync.Mutex
	var dispatched []strategy.Signal
	svc := &Service{cfg: Config{FNOExchange: "NFO", EODExitTime: "15:20"}, orderExecutor: oe, legResolver: fakeResolver{}}
	svc.orderRunner = func(_ context.Context, s strategy.Signal) { mu.Lock(); dispatched = append(dispatched, s); mu.Unlock() }
	svc.testBasket = basket

	sig := condorBasket(strategy.ActionBuy)
	legs, _ := svc.expandLegSignals(sig, now)
	for _, l := range legs {
		oe.UpdateLTP(model.Tick{Token: l.FNOToken, Exchange: "NFO", Price: 5000})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.handleBasketSignal(ctx, sig, now)

	waitUntil(t, "4 legs dispatched", func() bool { mu.Lock(); defer mu.Unlock(); return len(dispatched) == 4 })
	basket.mu.Lock()
	if len(basket.tokens) != 4 || basket.tokens[strategy.LegShortCE] == "" || basket.tokens[strategy.LegShortCE][:4] != "NFO:" {
		t.Errorf("leg tokens = %+v", basket.tokens)
	}
	basket.mu.Unlock()
	svc.liveOrdersMu.Lock()
	if len(svc.liveOrders) != 4 {
		t.Errorf("live leg rows = %d", len(svc.liveOrders))
	}
	svc.liveOrdersMu.Unlock()
}

func TestHandleBasketEntryCancelsWhenUnpriced(t *testing.T) {
	now := marketOpenTime(t)
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
	basket := &fakeBasket{}
	var mu sync.Mutex
	var dispatched []strategy.Signal
	svc := &Service{cfg: Config{EODExitTime: "15:20"}, orderExecutor: oe, legResolver: fakeResolver{}}
	svc.orderRunner = func(_ context.Context, s strategy.Signal) { mu.Lock(); dispatched = append(dispatched, s); mu.Unlock() }
	svc.testBasket = basket
	svc.legPriceWait = 20 * time.Millisecond
	svc.handleBasketSignal(context.Background(), condorBasket(strategy.ActionBuy), now)

	waitUntil(t, "basket cancelled", func() bool { basket.mu.Lock(); defer basket.mu.Unlock(); return basket.cancelled != "" })
	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != 0 {
		t.Fatalf("unpriced basket dispatched %d legs", len(dispatched))
	}
}

func TestHandleBasketEntryBlockedByKillSwitch(t *testing.T) {
	now := marketOpenTime(t)
	basket := &fakeBasket{}
	svc := &Service{cfg: Config{KillSwitch: true, EODExitTime: "15:20"}, legResolver: fakeResolver{}}
	svc.testBasket = basket
	svc.handleBasketSignal(context.Background(), condorBasket(strategy.ActionBuy), now)
	if basket.cancelled == "" {
		t.Fatal("kill switch must cancel the basket")
	}
}

func TestHandleBasketExitDispatchesImmediately(t *testing.T) {
	var mu sync.Mutex
	var dispatched []strategy.Signal
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
	// Kill switch and a closed market never block exits.
	svc := &Service{cfg: Config{KillSwitch: true}, orderExecutor: oe}
	svc.orderRunner = func(_ context.Context, s strategy.Signal) { mu.Lock(); dispatched = append(dispatched, s); mu.Unlock() }
	svc.handleBasketSignal(context.Background(), condorBasket(strategy.ActionExit), time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC))
	waitUntil(t, "4 exits dispatched", func() bool { mu.Lock(); defer mu.Unlock(); return len(dispatched) == 4 })
	mu.Lock()
	defer mu.Unlock()
	if !dispatched[0].Short || dispatched[0].Action != strategy.ActionExit || dispatched[0].Leg == "" {
		t.Fatalf("first exit = %+v, want a short leg", dispatched[0])
	}
}

func TestOnFillBooksLegOnOptionToken(t *testing.T) {
	svc := &Service{cfg: Config{FNOExchange: "NFO"}, pnlTracker: portfolio.NewPnLTracker(), portfolio: portfolio.New()}
	leg := strategy.Signal{StrategyName: "NIFTY50_RANGE_IC", Side: strategy.SideCall, Token: "99926000", Exchange: "NSE",
		Leg: strategy.LegShortCE, FNOToken: "302", Short: true}
	svc.onFill(orderexec.FillReport{Signal: leg, Direction: "SELL", FillPricePaise: 10000, Qty: 75})
	pos := svc.portfolio.GetPositions()
	if len(pos) != 1 || pos[0].Token != "302" || pos[0].Exchange != "NFO" || pos[0].Qty != -75 {
		t.Fatalf("portfolio = %+v", pos)
	}
	svc.onFill(orderexec.FillReport{Signal: leg, Direction: "BUY", FillPricePaise: 6000, Qty: 75})
	if got := svc.pnlTracker.GetStrategyDailyPnL("NIFTY50_RANGE_IC"); got != 4000*75 {
		t.Fatalf("realized = %d, want %d", got, 4000*75)
	}
	if svc.portfolio.PositionCount() != 0 {
		t.Error("short leg still in portfolio after buy-to-close")
	}
}

func TestExitExecutorEntriesCarriesLeg(t *testing.T) {
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 1, FNOExchange: "NFO", LogPrefix: "[test]"})
	oe.UpdateLTP(model.Tick{Token: "302", Exchange: "NFO", Price: 1000})
	oe.ExecuteSignal(strategy.Signal{StrategyName: "NIFTY50_RANGE_IC", Action: strategy.ActionBuy, Side: strategy.SideCall,
		Token: "99926000", Exchange: "NSE", Leg: strategy.LegShortCE, FNOToken: "302", Short: true})
	var mu sync.Mutex
	var dispatched []strategy.Signal
	svc := &Service{orderExecutor: oe}
	svc.orderRunner = func(_ context.Context, s strategy.Signal) { mu.Lock(); dispatched = append(dispatched, s); mu.Unlock() }
	if n := svc.exitExecutorEntries(context.Background(), "EOD"); n != 1 {
		t.Fatalf("dispatched %d", n)
	}
	waitUntil(t, "sweep exit", func() bool { mu.Lock(); defer mu.Unlock(); return len(dispatched) == 1 })
	mu.Lock()
	got := dispatched[0]
	mu.Unlock()
	if got.Leg != strategy.LegShortCE || !got.Short {
		t.Fatalf("sweep exit lost the leg: %+v", got)
	}
	// Running it through the executor closes the paper leg.
	oe.ExecuteSignal(got)
	if len(oe.GetEntryOrders()) != 0 {
		t.Fatal("sweep exit did not close the leg")
	}
}

func TestLegRecordUsesOptionTokenAndTag(t *testing.T) {
	svc := &Service{cfg: Config{FNOExchange: "NFO"}}
	rec := svc.legRecord(strategy.Signal{StrategyName: "IC", Action: strategy.ActionBuy, Side: strategy.SideCall,
		Token: "99926000", Exchange: "NSE", Leg: strategy.LegShortCE, Strike: 24200, FNOToken: "302", Reason: "IC ENTRY"})
	if rec.Token != "302" || rec.Exchange != "NFO" || rec.Reason != "[SHORT_CE 24200] IC ENTRY" {
		t.Fatalf("record = %+v", rec)
	}
}
