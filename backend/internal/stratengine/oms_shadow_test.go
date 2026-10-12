package stratengine

import (
	"encoding/json"
	"testing"
	"time"

	"trading-systemv1/internal/oms"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

func TestShadowIntent_CarriesResolvedContract(t *testing.T) {
	oe := orderexec.NewOrderExecutor(orderexec.Config{
		Qty: 1, CallFNOToken: "111", CallFNOSymbol: "NIFTY_CE", FNOExchange: "NFO", LogPrefix: "[test]",
	})
	svc := &Service{orderExecutor: oe, intentCh: make(chan oms.Intent, 1)}
	ts := time.Date(2026, 10, 12, 4, 31, 0, 0, time.UTC)
	svc.shadowIntent(strategy.Signal{StrategyName: "NIFTY50_SR", Action: strategy.ActionBuy, Side: strategy.SideCall, Token: "99926000", SignalTS: ts})
	in := <-svc.intentCh
	var sig strategy.Signal
	if err := json.Unmarshal(in.Payload, &sig); err != nil {
		t.Fatal(err)
	}
	if sig.FNOToken != "111" || sig.FNOSymbol != "NIFTY_CE" {
		t.Fatalf("intent contract %q/%q, want 111/NIFTY_CE", sig.FNOToken, sig.FNOSymbol)
	}
	if in.ID != oms.IntentID("NIFTY50_SR", "CALL", "BUY", "99926000", ts) {
		t.Fatalf("intent id %s not derived from the signal", in.ID)
	}
}

func TestShadowIntent_NeverBlocks(t *testing.T) {
	svc := &Service{intentCh: make(chan oms.Intent)} // no reader, no room
	done := make(chan struct{})
	go func() {
		svc.shadowIntent(strategy.Signal{StrategyName: "S", Action: strategy.ActionExit, Side: strategy.SideCall})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shadowIntent blocked on a full queue")
	}
	if svc.intentDropped.Load() != 1 {
		t.Fatalf("dropped %d, want 1", svc.intentDropped.Load())
	}
}

func TestShadowIntent_OffIsNoop(t *testing.T) {
	(&Service{}).shadowIntent(strategy.Signal{StrategyName: "S", Action: strategy.ActionBuy})
}
