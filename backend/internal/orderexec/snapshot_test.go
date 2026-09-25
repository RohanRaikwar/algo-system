package orderexec

import (
	"testing"

	"trading-systemv1/internal/strategy"
)

func TestSnapshotRestore_ExitAfterRestartUsesEntryContract(t *testing.T) {
	oe := exitTestExecutor(10)
	s := sig(strategy.ActionBuy)
	oe.ExecuteSignal(s) // dry-run BUY locks NIFTY_CE / 12345
	oe.mu.Lock()
	rec := oe.entryOrders[oe.positionKey(s)]
	rec.GttRuleID, rec.GttSLRuleID = "GTT-T", "GTT-SL"
	oe.entryOrders[oe.positionKey(s)] = rec
	oe.mu.Unlock()

	data, err := oe.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Restart: fresh executor whose strike mapping has moved on.
	restarted := exitTestExecutor(10)
	restarted.cfg.CallFNOToken, restarted.cfg.CallFNOSymbol = "99999", "NEW_STRIKE_CE"
	if err := restarted.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if !inCall(restarted) {
		t.Fatal("CALL position flag lost across restart")
	}
	token, symbol, err := restarted.resolveInstrument(sig(strategy.ActionExit), "SELL")
	if err != nil || token != "12345" || symbol != "NIFTY_CE" {
		t.Fatalf("exit must target entry contract 12345/NIFTY_CE, got %s/%s err=%v", token, symbol, err)
	}
	got, ok := restarted.GetEntryOrder("test_strategy", strategy.SideCall)
	if !ok || got.GttRuleID != "GTT-T" || got.GttSLRuleID != "GTT-SL" {
		t.Fatalf("GTT rule IDs lost across restart: %+v ok=%v", got, ok)
	}
}

func TestRestore_EmptyOrGarbage(t *testing.T) {
	oe := exitTestExecutor(10)
	if err := oe.Restore([]byte("not json")); err == nil {
		t.Fatal("expected error on garbage snapshot")
	}
	if inCall(oe) {
		t.Fatal("failed restore must not change state")
	}
}
