package stratengine

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/notification"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/portfolio"
	"trading-systemv1/internal/strategy"

	goredis "github.com/go-redis/redis/v8"
)

const (
	tradingConfigKey     = "trading:config"
	tradingConfigChannel = "cmd:config_update"

	positionReconcileInterval = 60 * time.Second
)

// killSwitchActive reports whether new entries are blocked. STRAT_KILL_SWITCH
// (env) is a floor the dashboard cannot lift; the dashboard toggle adds to it.
func (svc *Service) killSwitchActive() bool {
	return svc.cfg.KillSwitch || svc.uiKillSwitch.Load()
}

// applyTradingConfig applies a trading config published by the gateway
// (POST /api/trading/config). Only the kill switch is honoured: the gateway
// has no auth, so the dashboard may stop entries but never enable live
// orders or change sizing.
func (svc *Service) applyTradingConfig(data []byte) {
	var cfg struct {
		KillSwitch *bool `json:"killSwitch"`
		LiveOrders *bool `json:"liveOrders"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("[stratengine] ⚠️  bad trading config payload: %v", err)
		return
	}
	if cfg.KillSwitch != nil {
		if prev := svc.uiKillSwitch.Swap(*cfg.KillSwitch); prev != *cfg.KillSwitch {
			log.Printf("[stratengine] 🛑 dashboard kill switch → %v (effective=%v)", *cfg.KillSwitch, svc.killSwitchActive())
		}
	}
	if cfg.LiveOrders != nil && *cfg.LiveOrders != svc.cfg.LiveOrders {
		log.Printf("[stratengine] ℹ️  dashboard liveOrders=%v ignored — live orders are set only by env", *cfg.LiveOrders)
	}
}

// configUpdateLoop loads the persisted trading config, then follows live
// updates on cmd:config_update, resubscribing if the subscription drops.
func (svc *Service) configUpdateLoop(ctx context.Context) {
	client := svc.redisWriter.Client()

	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if data, err := client.Get(cctx, tradingConfigKey).Bytes(); err == nil {
		svc.applyTradingConfig(data)
	}
	cancel()

	backoff := time.Second
	for ctx.Err() == nil {
		sub := client.Subscribe(ctx, tradingConfigChannel)
		for msg := range sub.Channel() {
			backoff = time.Second
			svc.applyTradingConfig([]byte(msg.Payload))
		}
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		log.Printf("[stratengine] ⚠️  %s subscription closed — resubscribing in %s", tradingConfigChannel, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// startMetrics tracks the order-path gauges (circuit breaker, rate limiter),
// serves them on cfg.MetricsAddr when set, and publishes a snapshot to Redis
// for the dashboard Health page. Returns a stop func.
func (svc *Service) startMetrics() func() {
	// Own registry: only order-path series, so stratengine doesn't export
	// zero-valued mdengine_* metrics that would pollute the dashboard.
	m, reg := metrics.NewOrderMetrics()
	svc.orderExecutor.AttachMetrics(m)

	var srv *metrics.Server
	if svc.cfg.MetricsAddr != "" {
		srv = metrics.NewServerWithRegistry(svc.cfg.MetricsAddr, metrics.NewHealthStatus(), reg)
		srv.Start()
	}

	var rdb *goredis.Client
	if svc.redisWriter != nil {
		rdb = svc.redisWriter.Client()
	}

	// Refresh breaker/limiter gauges between orders so the dashboard never
	// shows a stale "open" breaker or an old window count, then publish.
	const interval = 5 * time.Second
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
			}
			svc.orderExecutor.ReportMetrics()
			if rdb == nil {
				continue
			}
			snap := m.OrderSnapshot()
			snap.UpdatedAt = metrics.Stamp()
			data, err := json.Marshal(snap)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = rdb.Set(ctx, metrics.SnapshotKeyStratEngine, data, metrics.SnapshotTTL(interval)).Err()
			cancel()
		}
	}()
	return func() {
		close(done)
		if srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			srv.Stop(ctx)
		}
	}
}

// orderQueueDepth bounds each strategy's pending-order queue. Signals are
// a few per minute at most; a full queue means the executor is wedged.
const orderQueueDepth = 256

// dispatchOrder hands a signal to its strategy's order worker and returns
// at once, so a slow order (fill confirmation, background settling) never
// stalls the signal loop or another strategy's exits. Orders of one
// strategy run strictly in arrival order, so a reverse EXIT always
// completes before its BUY.
func (svc *Service) dispatchOrder(ctx context.Context, sig strategy.Signal) {
	svc.orderQueuesMu.Lock()
	if svc.orderQueues == nil {
		svc.orderQueues = make(map[string]chan strategy.Signal)
	}
	q, ok := svc.orderQueues[sig.StrategyName]
	if !ok {
		q = make(chan strategy.Signal, orderQueueDepth)
		svc.orderQueues[sig.StrategyName] = q
		go svc.orderWorker(ctx, q)
	}
	svc.orderQueuesMu.Unlock()

	select {
	case q <- sig:
	default:
		// Never drop an exit: wait for room. Entries on a wedged queue are
		// dropped with an alert.
		if sig.Action == strategy.ActionBuy {
			svc.operatorAlert("order queue full for " + sig.StrategyName + " — entry dropped; executor may be stuck")
			return
		}
		svc.operatorAlert("order queue full for " + sig.StrategyName + " — exit waiting; executor may be stuck")
		select {
		case q <- sig:
		case <-ctx.Done():
		}
	}
}

func (svc *Service) orderWorker(ctx context.Context, q <-chan strategy.Signal) {
	run := svc.orderRunner
	if run == nil {
		run = svc.executeAndPersist
	}
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-q:
			run(ctx, sig)
		}
	}
}

// operatorAlert sends a situation that needs a human to the configured
// notifier (webhook/Telegram) as CRITICAL. Delivery runs off the caller's
// goroutine with a timeout so a slow webhook never stalls the order path.
func (svc *Service) operatorAlert(msg string) {
	svc.notifyAsync(notification.Alert{
		Level:   notification.AlertCritical,
		Title:   "Trading: operator action needed",
		Message: msg,
	})
}

// notifyAsync delivers alert off the caller's goroutine with a timeout, so
// a slow webhook never stalls the signal loop or the order path.
func (svc *Service) notifyAsync(alert notification.Alert) {
	if svc.notifier == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.notifier.Send(ctx, alert); err != nil {
			log.Printf("[stratengine] ⚠️  alert delivery failed: %v (alert: %s)", err, alert.Message)
		}
	}()
}

// wireAlerts routes executor and engine alerts to operatorAlert.
func (svc *Service) wireAlerts() {
	svc.orderExecutor.SetAlerter(svc.operatorAlert)
	svc.tfEngine.OnExitDropped = func(sig strategy.Signal) {
		svc.operatorAlert("DROPPED EXIT " + sig.StrategyName + " " + string(sig.Side) + " — signal channel full; position may still be open")
	}
}

// onFill records a fill in the P&L tracker and portfolio. Trades are
// booked here, from the executor's fills, not when the signal fires: an
// order that was blocked, rejected or never filled never shows as a trade,
// and a failed exit leaves the position open in P&L too. Runs on the order
// goroutine; the dashboard publish is handed off.
func (svc *Service) onFill(f orderexec.FillReport) {
	sig := f.Signal
	qty := f.Qty
	if qty <= 0 {
		qty = sig.Qty
	}
	realized := svc.pnlTracker.RecordTrade(portfolio.Trade{
		StrategyName: sig.StrategyName,
		Token:        sig.Token,
		Exchange:     sig.Exchange,
		Action:       f.Direction,
		Qty:          qty,
		Price:        f.FillPricePaise,
		Timestamp:    time.Now(),
	})
	if svc.portfolio != nil {
		if f.Direction == "BUY" {
			svc.portfolio.OpenPosition(sig.Token, sig.Exchange, qty, f.FillPricePaise)
		} else {
			svc.portfolio.ClosePosition(sig.Token, sig.Exchange)
		}
	}
	log.Printf("[stratengine] P&L: %s %s %s:%s qty=%d fill=%d paise real=%v realized=%d paise",
		sig.StrategyName, f.Direction, sig.Exchange, sig.Token, qty, f.FillPricePaise, f.Real, realized)

	if svc.redisWriter != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			svc.publishPnLSummary(ctx)
		}()
	}
}

// positionReconcileLoop compares the executor's real positions with the
// broker's once at startup (so a position the restored state doesn't know
// about is reported before the first signal) and then every
// positionReconcileInterval during market hours. It alerts on any
// mismatch and does not act on it.
func (svc *Service) positionReconcileLoop(ctx context.Context) {
	svc.reconcilePositionsOnce(true)
	ticker := time.NewTicker(positionReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !markethours.IsMarketOpen(time.Now()) {
			continue
		}
		svc.reconcilePositionsOnce(false)
	}
}

func (svc *Service) reconcilePositionsOnce(startup bool) {
	mismatches, err := svc.orderExecutor.ReconcilePositions()
	if errors.Is(err, orderexec.ErrNotLive) {
		return
	}
	if err != nil {
		log.Printf("[stratengine] ⚠️  position reconcile failed: %v", err)
		return
	}
	when := ""
	if startup {
		when = " at startup"
	}
	for _, m := range mismatches {
		log.Printf("[stratengine] 🚨 POSITION MISMATCH%s %s — check broker and close/adjust manually", when, m)
		svc.operatorAlert("POSITION MISMATCH" + when + " " + m.String() + " — check broker and close/adjust manually")
	}
}
