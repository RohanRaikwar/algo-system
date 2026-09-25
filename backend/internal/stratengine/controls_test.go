package stratengine

import (
	"context"
	"sync"
	"testing"
	"time"

	"trading-systemv1/internal/notification"

	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/portfolio"
	"trading-systemv1/internal/strategy"
)

func TestApplyTradingConfig_TogglesKillSwitch(t *testing.T) {
	svc := &Service{}
	svc.applyTradingConfig([]byte(`{"killSwitch":true,"liveOrders":true}`))
	if !svc.killSwitchActive() {
		t.Fatal("kill switch should be on")
	}
	svc.applyTradingConfig([]byte(`{"killSwitch":false}`))
	if svc.killSwitchActive() {
		t.Fatal("kill switch should be off")
	}
}

func TestApplyTradingConfig_EnvKillSwitchCannotBeCleared(t *testing.T) {
	svc := &Service{cfg: Config{KillSwitch: true}}
	svc.applyTradingConfig([]byte(`{"killSwitch":false}`))
	if !svc.killSwitchActive() {
		t.Fatal("STRAT_KILL_SWITCH=true must not be overridden from the UI")
	}
}

func TestApplyTradingConfig_IgnoresGarbageAndMissingField(t *testing.T) {
	svc := &Service{}
	svc.applyTradingConfig([]byte(`{"killSwitch":true}`))
	svc.applyTradingConfig([]byte(`not json`))
	svc.applyTradingConfig([]byte(`{"quantity":75}`))
	if !svc.killSwitchActive() {
		t.Fatal("garbage or payload without killSwitch must not change state")
	}
}

func TestApplyTradingConfig_NeverEnablesLiveOrders(t *testing.T) {
	svc := &Service{cfg: Config{LiveOrders: false}}
	svc.applyTradingConfig([]byte(`{"liveOrders":true}`))
	if svc.cfg.LiveOrders {
		t.Fatal("UI config must never turn on live orders")
	}
}

func TestOnFill_BooksTradesFromFills(t *testing.T) {
	svc := &Service{pnlTracker: portfolio.NewPnLTracker(), portfolio: portfolio.New()}
	sig := strategy.Signal{StrategyName: "NIFTY50_FNO", Exchange: "NSE", Token: "99926000", Qty: 1}

	svc.onFill(orderexec.FillReport{Signal: sig, Direction: "BUY", FillPricePaise: 10000, Qty: 75, Real: true})
	if svc.portfolio.PositionCount() != 1 {
		t.Fatal("BUY fill did not open a portfolio position")
	}
	svc.onFill(orderexec.FillReport{Signal: sig, Direction: "SELL", FillPricePaise: 10400, Qty: 75, Real: true})

	if got, want := svc.pnlTracker.GetRealizedPnL(), int64(400*75); got != want {
		t.Fatalf("realized %d, want %d", got, want)
	}
	if got := svc.pnlTracker.GetStrategyDailyPnL("NIFTY50_FNO"); got != 400*75 {
		t.Fatalf("strategy daily P&L %d, want %d", got, 400*75)
	}
	if svc.portfolio.PositionCount() != 0 {
		t.Fatal("SELL fill did not close the portfolio position")
	}
}

type blockingNotifier struct{ release chan struct{} }

func (b *blockingNotifier) Send(ctx context.Context, _ notification.Alert) error {
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil
}

func TestNotifyAsync_SlowWebhookDoesNotBlock(t *testing.T) {
	n := &blockingNotifier{release: make(chan struct{})}
	defer close(n.release)
	svc := &Service{notifier: n}
	done := make(chan struct{})
	go func() {
		svc.notifyAsync(notification.Alert{Message: "signal"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notifyAsync blocked on a slow notifier")
	}
}

type captureNotifier struct {
	mu     sync.Mutex
	alerts []notification.Alert
}

func (c *captureNotifier) Send(_ context.Context, a notification.Alert) error {
	c.mu.Lock()
	c.alerts = append(c.alerts, a)
	c.mu.Unlock()
	return nil
}

func (c *captureNotifier) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.alerts)
}

func TestOperatorAlert_SentCritical(t *testing.T) {
	n := &captureNotifier{}
	svc := &Service{notifier: n}
	svc.operatorAlert("EXIT FAILED — close manually")
	deadline := time.Now().Add(time.Second)
	for n.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n.count() != 1 || n.alerts[0].Level != notification.AlertCritical {
		t.Fatalf("want one critical alert, got %+v", n.alerts)
	}
}

func TestDispatchOrder_NonBlockingAndFIFOPerStrategy(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	var ran []strategy.Action
	svc := &Service{}
	svc.orderRunner = func(_ context.Context, s strategy.Signal) {
		<-gate
		mu.Lock()
		ran = append(ran, s.Action)
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		svc.dispatchOrder(ctx, strategy.Signal{StrategyName: "A", Action: strategy.ActionExit})
		svc.dispatchOrder(ctx, strategy.Signal{StrategyName: "A", Action: strategy.ActionBuy})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatchOrder blocked the signal loop")
	}
	close(gate)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(ran)
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || ran[0] != strategy.ActionExit || ran[1] != strategy.ActionBuy {
		t.Fatalf("want EXIT then BUY, got %v", ran)
	}
}
