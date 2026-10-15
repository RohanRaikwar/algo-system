package orderexec

import (
	"testing"

	"trading-systemv1/internal/model"
)

func legSig(action model.Action, leg, token string, side model.PositionSide, short bool) model.Signal {
	return model.Signal{
		StrategyName: "PAPER_BASKET", Action: action, Side: side,
		Token: "99926000", Exchange: "NSE", Qty: 75,
		Leg: leg, FNOToken: token, FNOSymbol: "SYM" + token, Short: short,
	}
}

func captureFills(oe *OrderExecutor) *fillLog {
	l := &fillLog{}
	oe.SetFillListener(l.add)
	return l
}

func setLTP(oe *OrderExecutor, token string, p int64) {
	oe.UpdateLTP(model.Tick{Token: token, Exchange: "NFO", Price: p})
}

func basketLegs() []model.Signal {
	return []model.Signal{
		legSig(model.ActionBuy, "LONG_CE", "301", model.SideCall, false),
		legSig(model.ActionBuy, "SHORT_CE", "302", model.SideCall, true),
		legSig(model.ActionBuy, "LONG_PE", "303", model.SidePut, false),
		legSig(model.ActionBuy, "SHORT_PE", "304", model.SidePut, true),
	}
}

func TestLegs_FourLegsOpenUnderDistinctKeys(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]"})
	fills := captureFills(oe)
	for i, s := range basketLegs() {
		setLTP(oe, s.FNOToken, int64(1000*(i+1)))
		oe.ExecuteSignal(s)
	}
	entries := oe.GetEntryOrders()
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4: %+v", len(entries), entries)
	}
	for _, s := range basketLegs() {
		rec, ok := entries[oe.positionKey(s)]
		if !ok || rec.Token != s.FNOToken || rec.Leg != s.Leg || rec.Short != s.Short || rec.Real {
			t.Errorf("leg %s entry = %+v ok=%v", s.Leg, rec, ok)
		}
	}
	if len(fills.get()) != 4 {
		t.Fatalf("fills = %d", len(fills.get()))
	}
	for _, f := range fills.get() {
		want := "BUY"
		if f.Signal.Short {
			want = "SELL"
		}
		if f.Direction != want || f.Real || f.Qty != 75 {
			t.Errorf("fill %s: %+v", f.Signal.Leg, f)
		}
	}
}

func TestLegs_ShortLegClosesWithBuy(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]"})
	fills := captureFills(oe)
	open := legSig(model.ActionBuy, "SHORT_CE", "302", model.SideCall, true)
	setLTP(oe, "302", 9000)
	oe.ExecuteSignal(open)

	exit := open
	exit.Action = model.ActionExit
	exit.FNOToken = "" // exits close the locked contract
	setLTP(oe, "302", 5000)
	oe.ExecuteSignal(exit)

	if len(fills.get()) != 2 {
		t.Fatalf("fills = %+v", fills.get())
	}
	c := fills.get()[1]
	if c.Direction != "BUY" || c.FillPricePaise != 5000 || c.Signal.FNOToken != "302" || !c.Signal.Short {
		t.Fatalf("close fill = %+v", c)
	}
	if len(oe.GetEntryOrders()) != 0 {
		t.Error("leg still tracked after close")
	}
}

func TestLegs_DuplicateOpenBlocked(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]"})
	fills := captureFills(oe)
	s := legSig(model.ActionBuy, "LONG_CE", "301", model.SideCall, false)
	setLTP(oe, "301", 1000)
	oe.ExecuteSignal(s)
	oe.ExecuteSignal(s)
	if len(fills.get()) != 1 {
		t.Fatalf("second open not blocked: %d fills", len(fills.get()))
	}
}

func TestLegs_ExitWithNothingOpenIsNoop(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]"})
	fills := captureFills(oe)
	oe.ExecuteSignal(legSig(model.ActionExit, "LONG_CE", "301", model.SideCall, false))
	if len(fills.get()) != 0 {
		t.Fatal("exit with nothing open reported a fill")
	}
}

func TestLegs_DontBlockSingleLegPositions(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]",
		CallFNOToken: "111", CallFNOSymbol: "CE", PutFNOToken: "222", PutFNOSymbol: "PE"})
	for _, s := range basketLegs() {
		setLTP(oe, s.FNOToken, 1000)
		oe.ExecuteSignal(s)
	}
	// Same strategy name, single-leg PUT: legs must not count as an opposite-side conflict.
	single := model.Signal{StrategyName: "PAPER_BASKET", Action: model.ActionBuy, Side: model.SidePut, Token: "99926000", Exchange: "NSE"}
	setLTP(oe, "222", 1000)
	oe.ExecuteSignal(single)
	if _, ok := oe.GetEntryOrder("PAPER_BASKET", model.SidePut); !ok {
		t.Fatal("single-leg entry blocked by leg positions")
	}
}

// Safety: a leg never reaches the broker, even under the real-order name
// with a live session.
func TestLegs_NeverReachBrokerEvenWhenLive(t *testing.T) {
	fake := &fakeAPI{autoStatus: "complete", autoAvg: 10}
	oe := liveExecutor(t, fake)
	s := legSig(model.ActionBuy, "SHORT_CE", "302", model.SideCall, true)
	s.StrategyName = "NIFTY50_FNO"
	setLTP(oe, "302", 1000)
	oe.ExecuteSignal(s)
	exit := s
	exit.Action = model.ActionExit
	oe.ExecuteSignal(exit)
	if n := fake.placedCount(); n != 0 {
		t.Fatalf("leg sent %d orders to the broker", n)
	}
}

func TestLegs_SnapshotKeepsLegFields(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]"})
	s := legSig(model.ActionBuy, "SHORT_PE", "304", model.SidePut, true)
	setLTP(oe, "304", 1000)
	oe.ExecuteSignal(s)
	data, err := oe.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]"})
	if err := r.Restore(data); err != nil {
		t.Fatal(err)
	}
	rec, ok := r.GetEntryOrders()[r.positionKey(s)]
	if !ok || rec.Leg != "SHORT_PE" || !rec.Short || rec.Token != "304" {
		t.Fatalf("restored leg = %+v ok=%v", rec, ok)
	}
}

func TestSingleLegEntryUsesSignalContract(t *testing.T) {
	oe := NewOrderExecutor(Config{Qty: 75, FNOExchange: "NFO", LogPrefix: "[test]",
		CallFNOToken: "ATM", CallFNOSymbol: "NIFTY_ATM_CE"})
	fills := captureFills(oe)
	s := model.Signal{StrategyName: "PAPER_OTHER", Action: model.ActionBuy, Side: model.SideCall,
		Token: "99926000", Exchange: "NSE", FNOToken: "OTM1", FNOSymbol: "NIFTY_OTM1_CE", Strike: 24050}
	setLTP(oe, "OTM1", 8000)
	oe.ExecuteSignal(s)
	rec, ok := oe.GetEntryOrder("PAPER_OTHER", model.SideCall)
	if !ok || rec.Token != "OTM1" || rec.Symbol != "NIFTY_OTM1_CE" || rec.Price != 8000 {
		t.Fatalf("entry = %+v ok=%v", rec, ok)
	}
	// The exit closes the locked contract even if it names none.
	exit := model.Signal{StrategyName: "PAPER_OTHER", Action: model.ActionExit, Side: model.SideCall, Token: "99926000", Exchange: "NSE"}
	setLTP(oe, "OTM1", 9000)
	oe.ExecuteSignal(exit)
	got := fills.get()
	if len(got) != 2 || got[1].FillPricePaise != 9000 {
		t.Fatalf("fills = %+v", got)
	}
}
