package stratengine

import (
	"context"
	"encoding/json"
	"testing"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// restoredExecutor returns an executor holding one paper SR CALL entry in
// contract "SRCE" bought at 184.05, as after a mid-day restart.
func restoredExecutor(t *testing.T) *orderexec.OrderExecutor {
	t.Helper()
	oe := orderexec.NewOrderExecutor(orderexec.Config{Qty: 65, FNOExchange: "NFO", LogPrefix: "[test]"})
	snap, _ := json.Marshal(map[string]any{
		"position_inst": map[string]any{"NIFTY50_SR|CALL": map[string]string{"token": "SRCE", "symbol": "NIFTY13OCT2622650CE"}},
		"entry_orders": map[string]any{"NIFTY50_SR|CALL": map[string]any{
			"Token": "SRCE", "Price": 18405, "StrategyName": "NIFTY50_SR", "PositionSide": "CALL",
		}},
	})
	if err := oe.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if _, ok := oe.GetEntryOrder("NIFTY50_SR", strategy.SideCall); !ok {
		t.Fatalf("snapshot JSON did not restore the entry: %s", snap)
	}
	return oe
}

func TestExitAfterRestartUsesHeldContract(t *testing.T) {
	svc := &Service{orderExecutor: restoredExecutor(t)}
	token, price, ok := svc.restoredEntryFNO("NIFTY50_SR|CALL")
	if !ok || token != "SRCE" || price != 18405 {
		t.Fatalf("restoredEntryFNO = %q %d %v, want SRCE 18405 true", token, price, ok)
	}
	if _, _, ok := svc.restoredEntryFNO("PAPER_OTHER|CALL"); ok {
		t.Fatal("no entry for PAPER_OTHER|CALL, want ok=false")
	}
}

func TestRestartResubscribesHeldContracts(t *testing.T) {
	var sent []string
	svc := &Service{orderExecutor: restoredExecutor(t)}
	svc.subscribeHook = func(tokens []string) { sent = append(sent, tokens...) }
	svc.subscribeHeldContracts(context.Background())
	if len(sent) != 1 || sent[0] != "SRCE" {
		t.Fatalf("subscribed %v, want [SRCE]", sent)
	}
}
