package stratengine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/portfolio"
	redisstore "trading-systemv1/internal/store/redis"
	"trading-systemv1/internal/strategy"
)

// Alerts raised while restoring (startup settlement of a previous day's
// pending BUY) must reach the notifier, not only the log.
func TestRestoreAndWire_StartupSettlementAlertReachesNotifier(t *testing.T) {
	w, err := redisstore.New(redisstore.WriterConfig{Addr: "127.0.0.1:6379", DB: 15})
	if err != nil {
		t.Skipf("no local redis: %v", err)
	}
	defer w.Close()
	ctx := context.Background()
	key := fmt.Sprintf("test:stratengine:snapshot:%d", time.Now().UnixNano())
	defer w.Client().Del(ctx, key)

	yesterday := time.Now().Add(-26 * time.Hour).Format(time.RFC3339Nano)
	snap := `{"order_executor":{"entry_orders":{"NIFTY50_FNO|CALL":{"ClientOrderID":"k1","Symbol":"NIFTY_CE","Token":"111","Quantity":75,` +
		`"Real":true,"Pending":true,"Timestamp":"` + yesterday + `","StrategyName":"NIFTY50_FNO","PositionSide":"CALL"}},"position_inst":{}}}`
	if err := w.Client().Set(ctx, key, snap, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}

	n := &captureNotifier{}
	svc := &Service{
		cfg:           Config{SnapshotKey: key},
		redisWriter:   w,
		notifier:      n,
		tfEngine:      strategy.NewTFEngine(10),
		pnlTracker:    portfolio.NewPnLTracker(),
		orderExecutor: orderexec.NewOrderExecutor(orderexec.Config{LogPrefix: "[test]"}),
	}
	svc.restoreAndWire(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for n.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var msgs []string
	for _, a := range n.alerts {
		msgs = append(msgs, a.Message)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "previous day") {
		t.Fatalf("startup settlement alert did not reach the notifier; got %q", msgs)
	}
}
