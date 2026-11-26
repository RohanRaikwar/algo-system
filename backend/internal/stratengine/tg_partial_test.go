package stratengine

import (
	"path/filepath"
	"testing"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/strategy"
)

// A paper partial exit books 2 lots and leaves the last lot open in the
// portfolio and the ledger; the final exit closes it.
func TestOnFillPartialExitKeepsRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pnl.db")
	svc := ledgerSvc(t, path)
	sig := strategy.Signal{StrategyName: "TG_TCI", Side: strategy.SideCall, Exchange: "NSE", Token: "99926000", FNOToken: "45001"}
	svc.onFill(orderexec.FillReport{Signal: sig, Direction: "BUY", FillPricePaise: 10000, Qty: 195})
	part := sig
	part.ExitLots = 2
	svc.onFill(orderexec.FillReport{Signal: part, Direction: "SELL", FillPricePaise: 12000, Qty: 130, Partial: true})

	pos := svc.portfolio.GetPositions()
	if len(pos) != 1 || pos[0].Qty != 65 {
		t.Fatalf("portfolio after partial %+v", pos)
	}
	if got := svc.pnlTracker.GetRealizedPnL(); got != 2000*130 {
		t.Fatalf("realized %d, want %d", got, 2000*130)
	}
	svc.onFill(orderexec.FillReport{Signal: sig, Direction: "SELL", FillPricePaise: 13000, Qty: 65})
	if pos := svc.portfolio.GetPositions(); len(pos) != 0 {
		t.Fatalf("portfolio after final exit %+v", pos)
	}
	svc.closePNLLedger()
	if got := dayTrades(t, path); len(got) != 2 || got[0].Qty != 130 || got[1].Qty != 65 {
		t.Fatalf("ledger trades %+v", got)
	}
}

// An entry that never filled is closed in the book only: an EXIT with no
// order, naming the contract, so the dashboard row closes.
func TestEntryDroppedExitIsBookOnly(t *testing.T) {
	buy := strategy.Signal{StrategyName: "TG_TCI", Action: strategy.ActionBuy, Side: strategy.SidePut, Token: "99919000",
		Exchange: "BSE", FNOToken: "845123", FNOSymbol: "SENSEX26NOV84100PE", Lots: 3}
	x := entryDroppedExit(buy, "paper resting BUY not filled")
	if x.Action != strategy.ActionExit || !x.BookOnly || x.Side != strategy.SidePut || x.FNOToken != "845123" ||
		x.Token != "99919000" || x.Lots != 0 || x.Reason != "ENTRY_REFUSED paper resting BUY not filled" {
		t.Fatalf("exit %+v", x)
	}
}

func TestTGHolds(t *testing.T) {
	svc := tgHolding(t)
	if !svc.tgHolds(strategy.TelegramSignalName, strategy.SideCall) {
		t.Fatal("held CALL not seen")
	}
	if svc.tgHolds(strategy.TelegramSignalName, strategy.SidePut) || svc.tgHolds("TG_NONE", strategy.SideCall) {
		t.Fatal("phantom position")
	}
}

// A Telegram entry buys at most the call's price + ₹5 by default.
func TestTGEntryCapDefault(t *testing.T) {
	t.Setenv("STRAT_TG_ENTRY_CAP_PAISE", "")
	if got := LoadConfig().TGEntryCapPaise; got != 500 {
		t.Fatalf("entry cap %d, want 500", got)
	}
}

// Only the Telegram strategies trade by default: SR is off.
func TestSRDisabledByDefault(t *testing.T) {
	t.Setenv("STRAT_SR_ENABLED", "")
	if LoadConfig().SREnabled {
		t.Fatal("SR enabled by default")
	}
}
