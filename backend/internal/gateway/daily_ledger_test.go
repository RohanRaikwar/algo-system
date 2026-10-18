package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"trading-systemv1/internal/pnlstore"
)

func TestMergeLedgerReplacesSignalDaysFromFirstLedgerDay(t *testing.T) {
	ist := mustLoadIST()
	day := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, ist) }
	pairs := []pairedTrade{
		{Strategy: "NIFTY50_SR", Realized: 100, ExitAt: day(16, 11)}, // before the ledger: kept
		{Strategy: "NIFTY50_SR", Realized: 999, ExitAt: day(19, 10)}, // ledger day: replaced
	}
	ledger := []pnlstore.Trade{{Strategy: "NIFTY50_SR", Realized: 250, ExitAt: day(19, 10), ExitReason: "TARGET", Real: true}}
	got := mergeLedger(pairs, ledger, day(19, 0))
	if len(got) != 2 || got[0].Realized != 100 || got[1].Realized != 250 || !got[1].Real || got[1].ExitReason != "TARGET" {
		t.Fatalf("merged %+v", got)
	}
}

func TestDailyOrdersFromLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pnl.db")
	s, err := pnlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tr := range []pnlstore.Trade{
		{Strategy: "SENSEX_SR", Side: "CALL", Exchange: "BFO", Token: "845123", FNOSymbol: "SENSEX26O2282000CE", Qty: 20,
			EntryPrice: 15000, ExitPrice: 16500, Realized: 30000, EntryAt: now.Add(-time.Hour), ExitAt: now.Add(-time.Minute), ExitReason: "MANUAL EXIT"},
		{Strategy: "NIFTY50_SR", Side: "PUT", Qty: 65, EntryPrice: 10000, ExitPrice: 9000, Realized: -65000,
			EntryAt: now.Add(-2 * time.Hour), ExitAt: now.Add(-2 * time.Minute), Real: true},
	} {
		if err := s.AddTrade(tr); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	svc := NewDailyAnalyticsService([]string{filepath.Join(t.TempDir(), "none.db")}, nil)
	svc.LedgerPath = path
	orders, err := svc.DailyOrders(30, "")
	if err != nil || len(orders.Items) != 1 || orders.Items[0].Trades != 2 || orders.Items[0].PnL != -35000 {
		t.Fatalf("orders %+v %v", orders, err)
	}
	o := orders.Items[0].Orders[0] // newest first
	if o.Strategy != "SENSEX_SR" || o.FNOSymbol != "SENSEX26O2282000CE" || o.ExitReason != "MANUAL EXIT" || o.Real {
		t.Fatalf("order %+v", o)
	}
	if !orders.Items[0].Orders[1].Real {
		t.Fatal("real flag lost")
	}
	pnl, err := svc.DailyPnL(30, "SENSEX_SR")
	if err != nil || len(pnl.Items) != 1 || pnl.Items[0].PnL != 30000 || pnl.Items[0].Wins != 1 {
		t.Fatalf("pnl %+v %v", pnl, err)
	}
}

func TestDailyOrdersWithoutLedger(t *testing.T) {
	svc := NewDailyAnalyticsService([]string{filepath.Join(t.TempDir(), "none.db")}, nil)
	svc.LedgerPath = filepath.Join(t.TempDir(), "missing.db")
	if got, err := svc.DailyOrders(30, ""); err != nil || len(got.Items) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}
