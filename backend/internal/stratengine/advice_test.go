package stratengine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/portfolio"
	"trading-systemv1/internal/strategy"
)

// Entry advice (WATCH_BUY) is journaled but never reaches the order path:
// no order queue is created for it.
func TestSignalLoop_WatchBuyNeverDispatchesOrder(t *testing.T) {
	j, err := strategy.NewSignalJournal(filepath.Join(t.TempDir(), "signals.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		tfEngine:      strategy.NewTFEngine(10),
		journal:       j,
		pnlTracker:    portfolio.NewPnLTracker(),
		orderExecutor: orderexec.NewOrderExecutor(orderexec.Config{LogPrefix: "[test]"}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.signalLoop(ctx)

	svc.tfEngine.SendSignal(strategy.Signal{
		StrategyName: "NIFTY50_SR", Action: strategy.ActionWatchBuy, Side: strategy.SideCall,
		Token: "99926000", Exchange: "NSE", Qty: 65, Price: 2250000, Reason: "SR CONTINUE CALL",
	})

	var recs []strategy.SignalRecord
	deadline := time.Now().Add(2 * time.Second)
	for len(recs) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		recs, _ = j.GetSignals(10)
	}
	if len(recs) != 1 || recs[0].Action != string(strategy.ActionWatchBuy) {
		t.Fatalf("journal = %+v, want one WATCH_BUY", recs)
	}
	svc.orderQueuesMu.Lock()
	queues := len(svc.orderQueues)
	svc.orderQueuesMu.Unlock()
	if queues != 0 {
		t.Fatalf("advice reached dispatchOrder (%d order queues)", queues)
	}
	if n := len(svc.orderExecutor.GetEntryOrders()); n != 0 {
		t.Fatalf("advice created %d entry orders", n)
	}
}
