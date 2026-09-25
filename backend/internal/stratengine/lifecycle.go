package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"trading-systemv1/internal/notification"
	"trading-systemv1/internal/strategy"
)

// snapshotLoop periodically persists strategy state to Redis.
func (svc *Service) snapshotLoop(ctx context.Context) {
	interval := time.Duration(svc.cfg.SnapshotIntervalS) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			svc.saveSnapshot(ctx)
		}
	}
}

// executeAndPersist runs a signal through the order executor and then saves
// a snapshot at once, so a crash right after an order cannot lose the
// executor's record of it to the periodic snapshot interval.
func (svc *Service) executeAndPersist(ctx context.Context, sig strategy.Signal) {
	svc.orderExecutor.ExecuteSignal(sig)
	svc.saveSnapshot(ctx)
}

// saveSnapshot persists all strategy states to Redis and publishes the
// indicator snapshot.
func (svc *Service) saveSnapshot(ctx context.Context) {
	if err := svc.persistSnapshot(ctx); err != nil {
		log.Printf("[stratengine] %v", err)
	}
	svc.publishIndicators(ctx)
}

// persistSnapshot writes all strategy, P&L and executor state to Redis and
// reports whether the write landed. The executor calls it synchronously
// before every real order (see SetIntentPersister).
func (svc *Service) persistSnapshot(ctx context.Context) error {
	svc.snapshotMu.Lock()
	defer svc.snapshotMu.Unlock()
	snapshots := make(map[string]json.RawMessage, 4)

	nifty50Snap, err := svc.nifty50Strategy.Snapshot()
	if err != nil {
		log.Printf("[stratengine] NIFTY50_FNO snapshot error: %v", err)
	} else {
		snapshots["nifty50_fno"] = nifty50Snap
	}

	slSnap, err := svc.nifty50SLStrategy.Snapshot()
	if err != nil {
		log.Printf("[stratengine] NIFTY50_FNO_SL snapshot error: %v", err)
	} else {
		snapshots["nifty50_fno_sl"] = slSnap
	}

	pts10Snap, err := svc.nifty5010PtsStrategy.Snapshot()
	if err != nil {
		log.Printf("[stratengine] NIFTY50_10PTS snapshot error: %v", err)
	} else {
		snapshots["nifty50_10pts"] = pts10Snap
	}

	sl2Snap, err := svc.nifty50SL2Strategy.Snapshot()
	if err != nil {
		log.Printf("[stratengine] NIFTY50_FNO_SL2 snapshot error: %v", err)
	} else {
		snapshots["nifty50_fno_sl2"] = sl2Snap
	}

	rangeSnap, err := svc.nifty50RangeStrategy.Snapshot()
	if err != nil {
		log.Printf("[stratengine] NIFTY50_RANGE snapshot error: %v", err)
	} else {
		snapshots["nifty50_range"] = rangeSnap
	}

	fno5m3mSnap, err := svc.nifty50FnO5M3MStrategy.Snapshot()
	if err != nil {
		log.Printf("[stratengine] NIFTY50_FNO_5M3M snapshot error: %v", err)
	} else {
		snapshots["nifty50_fno_5m3m"] = fno5m3mSnap
	}

	// Snapshot P&L tracker state
	pnlSnap, err := svc.pnlTracker.Snapshot()
	if err != nil {
		log.Printf("[stratengine] PnL tracker snapshot error: %v", err)
	} else {
		snapshots["pnl_tracker"] = pnlSnap
	}

	// Snapshot order executor position state (open side, entry contract, GTT IDs)
	execSnap, err := svc.orderExecutor.Snapshot()
	if err != nil {
		log.Printf("[stratengine] order executor snapshot error: %v", err)
	} else {
		snapshots["order_executor"] = execSnap
	}

	// Snapshot portfolio state
	portSnap, err := svc.portfolio.Snapshot()
	if err != nil {
		log.Printf("[stratengine] portfolio snapshot error: %v", err)
	} else {
		snapshots["portfolio"] = portSnap
	}

	// Without the executor's state the snapshot can't record an order
	// intent; report that rather than write a snapshot missing it.
	if _, ok := snapshots["order_executor"]; !ok {
		return fmt.Errorf("snapshot has no order executor state")
	}

	combined, err := json.Marshal(snapshots)
	if err != nil {
		return fmt.Errorf("snapshot marshal error: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := svc.redisWriter.Client().Set(cctx, svc.cfg.SnapshotKey, combined, 0).Err(); err != nil {
		return fmt.Errorf("snapshot write error: %w", err)
	}
	return nil
}

// publishIndicators publishes live indicator values to Redis for the frontend.
func (svc *Service) publishIndicators(ctx context.Context) {
	if svc.redisWriter == nil {
		return
	}

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// ── NIFTY50_FNO indicators ──
	if svc.nifty50Strategy != nil {
		if indSnap := svc.nifty50Strategy.IndicatorSnapshot(); indSnap != nil {
			if payload, err := json.Marshal(indSnap); err == nil {
				svc.redisWriter.Client().Set(cctx, "indicator:nifty50_fno", payload, 24*time.Hour)
				svc.redisWriter.Client().Publish(cctx, "pub:indicator", string(payload))
			}
		}
	}

	// ── NIFTY50_FNO_SL indicators ──
	if svc.nifty50SLStrategy != nil {
		if indSnap := svc.nifty50SLStrategy.IndicatorSnapshot(); indSnap != nil {
			if payload, err := json.Marshal(indSnap); err == nil {
				svc.redisWriter.Client().Set(cctx, "indicator:nifty50_fno_sl", payload, 24*time.Hour)
				svc.redisWriter.Client().Publish(cctx, "pub:indicator", string(payload))
			}
		}
	}

	// ── NIFTY50_10PTS indicators ──
	if svc.nifty5010PtsStrategy != nil {
		if indSnap := svc.nifty5010PtsStrategy.IndicatorSnapshot(); indSnap != nil {
			if payload, err := json.Marshal(indSnap); err == nil {
				svc.redisWriter.Client().Set(cctx, "indicator:nifty50_10pts", payload, 24*time.Hour)
				svc.redisWriter.Client().Publish(cctx, "pub:indicator", string(payload))
			}
		}
	}
}

// restoreAndWire restores state from the Redis snapshot and wires the
// executor's listeners around it. Order matters:
//   - alerts and fill corrections are wired BEFORE restore, so anything
//     startup settlement raises (e.g. a previous day's pending BUY kept open
//     without GTTs) reaches the notifier, not just the log;
//   - the save-on-change listener and the pre-order intent persister are
//     set AFTER restore: a save during restore would write a half-restored
//     snapshot (P&L not yet loaded) over the real one.
func (svc *Service) restoreAndWire(ctx context.Context) {
	svc.orderExecutor.SetFillListener(svc.onFill)
	svc.wireAlerts()
	svc.restoreState(ctx)
	svc.orderExecutor.SetStateListener(func() { svc.saveSnapshot(ctx) })
	svc.orderExecutor.SetIntentPersister(func() error { return svc.persistSnapshot(ctx) })
}

// restoreState loads all strategy snapshots from Redis.
func (svc *Service) restoreState(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	data, err := svc.redisWriter.Client().Get(cctx, svc.cfg.SnapshotKey).Bytes()
	if err != nil {
		log.Printf("[stratengine] no snapshot found (cold start): %v", err)
		return
	}

	var snapshots map[string]json.RawMessage
	if err := json.Unmarshal(data, &snapshots); err != nil {
		log.Printf("[stratengine] snapshot unmarshal error: %v", err)
		return
	}

	if nifty50Data, ok := snapshots["nifty50_fno"]; ok {
		if err := svc.nifty50Strategy.Restore(nifty50Data); err != nil {
			log.Printf("[stratengine] NIFTY50_FNO restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ NIFTY50_FNO strategy state restored")
		}
	}

	if slData, ok := snapshots["nifty50_fno_sl"]; ok {
		if err := svc.nifty50SLStrategy.Restore(slData); err != nil {
			log.Printf("[stratengine] NIFTY50_FNO_SL restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ NIFTY50_FNO_SL strategy state restored")
		}
	}

	if sl2Data, ok := snapshots["nifty50_fno_sl2"]; ok {
		if err := svc.nifty50SL2Strategy.Restore(sl2Data); err != nil {
			log.Printf("[stratengine] NIFTY50_FNO_SL2 restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ NIFTY50_FNO_SL2 strategy state restored")
		}
	}

	if pts10Data, ok := snapshots["nifty50_10pts"]; ok {
		if err := svc.nifty5010PtsStrategy.Restore(pts10Data); err != nil {
			log.Printf("[stratengine] NIFTY50_10PTS restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ NIFTY50_10PTS strategy state restored")
		}
	} else if pts4Data, ok := snapshots["nifty50_4pts"]; ok {
		// attempt fallback restore from 4pts snapshot
		if err := svc.nifty5010PtsStrategy.Restore(pts4Data); err == nil {
			log.Println("[stratengine] ✅ NIFTY50_10PTS strategy state restored from 4pts fallback")
		}
	}

	if fno5m3mData, ok := snapshots["nifty50_fno_5m3m"]; ok {
		if err := svc.nifty50FnO5M3MStrategy.Restore(fno5m3mData); err != nil {
			log.Printf("[stratengine] NIFTY50_FNO_5M3M restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ NIFTY50_FNO_5M3M strategy state restored")
		}
	}

	if rangeData, ok := snapshots["nifty50_range"]; ok {
		if err := svc.nifty50RangeStrategy.Restore(rangeData); err != nil {
			log.Printf("[stratengine] NIFTY50_RANGE restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ NIFTY50_RANGE strategy state restored")
		}
	}

	// Restore P&L tracker state
	if pnlData, ok := snapshots["pnl_tracker"]; ok {
		if err := svc.pnlTracker.RestorePnL(pnlData); err != nil {
			log.Printf("[stratengine] PnL tracker restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ PnL tracker state restored")
		}
	}

	// Restore order executor state before any exit can fire, so exits target
	// the contract that was actually bought.
	if execData, ok := snapshots["order_executor"]; ok {
		if err := svc.orderExecutor.Restore(execData); err != nil {
			log.Printf("[stratengine] order executor restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ order executor state restored")
			svc.orderExecutor.ResumeSettlement()
		}
	}

	// Restore portfolio state
	if portData, ok := snapshots["portfolio"]; ok {
		if err := svc.portfolio.RestorePositions(portData); err != nil {
			log.Printf("[stratengine] portfolio restore error: %v", err)
		} else {
			log.Println("[stratengine] ✅ portfolio state restored")
		}
	}

	svc.seedLiveOrdersFromStrategies()
}

// publishPnLSummary publishes the current P&L summary to Redis for the
// gateway REST API (key: pnl:summary) and WS broadcast (channel: pub:pnl).
func (svc *Service) publishPnLSummary(ctx context.Context) {
	dailyStats := svc.pnlTracker.GetDailyStats()
	summary := map[string]interface{}{
		"realized_pnl":   svc.pnlTracker.GetRealizedPnL(),
		"total_trades":   dailyStats.TradeCount,
		"wins":           dailyStats.Wins,
		"losses":         dailyStats.Losses,
		"win_rate":       dailyStats.WinRate,
		"largest_win":    dailyStats.LargestWin,
		"largest_loss":   dailyStats.LargestLoss,
		"open_positions": svc.portfolio.PositionCount(),
		"total_exposure": svc.portfolio.TotalExposure(),
		"ts":             time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(summary)
	if err != nil {
		log.Printf("[stratengine] P&L summary marshal error: %v", err)
		return
	}

	// Store for REST API
	svc.redisWriter.Client().Set(ctx, "pnl:summary", payload, 24*time.Hour)
	// Publish for WS broadcast
	svc.redisWriter.Client().Publish(ctx, "pub:pnl", string(payload))
}

// buildStreams constructs Redis stream names for TF=60 (1m), TF=120 (2m), and TF=180 (3m) candles.
func (svc *Service) buildStreams(ctx context.Context) []string {
	tfs := []int{60, 120, 180}
	var streams []string

	for _, tf := range tfs {
		if len(svc.cfg.SubscribeTokenKeys) > 0 {
			for _, tk := range svc.cfg.SubscribeTokenKeys {
				streams = append(streams, "candle:"+strconv.Itoa(tf)+"s:"+tk)
			}
		} else {
			discovered := svc.redisReader.DiscoverTFStreams(ctx, []int{tf}, svc.cfg.SubscribeTokenKeys)
			streams = append(streams, discovered...)
		}
	}
	return streams
}

// startConsumer starts the Redis stream XREADGROUP consumer.
func (svc *Service) startConsumer(ctx context.Context) {
	if len(svc.streams) == 0 {
		return
	}
	go func() {
		if err := svc.redisReader.ConsumeTFCandles(ctx, svc.streams, svc.tfCandleCh); err != nil {
			log.Printf("[stratengine] consumer error: %v", err)
		}
	}()
}

// startPELReclaimer starts periodic reclamation of stale PEL messages.
func (svc *Service) startPELReclaimer(ctx context.Context) {
	if len(svc.streams) == 0 {
		return
	}
	go svc.redisReader.StartPELReclaimer(ctx, svc.streams,
		svc.cfg.ConsumerGroup, svc.cfg.ConsumerName,
		time.Duration(svc.cfg.PELIntervalS)*time.Second,
		svc.cfg.PELMinIdleMs, svc.tfCandleCh,
		func(count int) {
			log.Printf("[stratengine] reclaimed %d stale PEL messages", count)
		})
}

// shutdown saves final snapshot and closes connections.
func (svc *Service) shutdown() {
	log.Println("[stratengine] shutdown — saving final snapshot...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	svc.saveSnapshot(shutCtx)

	svc.journal.Close()
	svc.redisWriter.Close()
	svc.redisReader.Close()
	log.Println("[stratengine] shutdown complete.")
}

// ── EOD Auto-Exit & Day-Only Position Policy ──

var ist = time.FixedZone("IST", 5*3600+30*60)

// clearStalePositions fires exit signals for any open position that was
// entered on a previous calendar day (IST). Called once at startup after
// snapshot restore so the engine starts each day with a clean slate.
func (svc *Service) clearStalePositions() {
	today := time.Now().In(ist).Truncate(24 * time.Hour)
	allSigs := svc.forceExitAllStrategies("STALE position from previous day — auto-closed on startup")
	for _, sig := range allSigs {
		log.Printf("[stratengine] clearing stale position: %s %s:%s", sig.Side, sig.Exchange, sig.Token)
		svc.tfEngine.SendSignal(sig)
	}
	if len(allSigs) > 0 {
		log.Printf("[stratengine] cleared %d stale positions (today=%s)", len(allSigs), today.Format("2006-01-02"))
	}
}

// dailyResetLoop waits until 09:00:00 IST each day (just before market
// open at 09:15), then resets per-day counters on the PnL tracker
// (realized daily PnL, win/loss tallies, and the per-strategy daily PnL
// map used by the profit cap check).
//
// 09:00 IST chosen so yesterday's PnL stays visible from EOD (15:30) all
// the way through the overnight window (15:30 → 09:00) for review.
//
// Without this reset, a daemon running across days carries yesterday's
// daily PnL into today, which silently downgrades the first BUY of the
// new day to PAPER once yesterday hit the profit cap.
func (svc *Service) dailyResetLoop(ctx context.Context) {
	for {
		now := time.Now().In(ist)
		// Target: today 09:00:00 IST (or tomorrow's if already past).
		reset := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, ist)
		if !now.Before(reset) {
			reset = reset.Add(24 * time.Hour)
		}
		wait := reset.Sub(now)
		log.Printf("[stratengine] daily PnL reset scheduled at %s (in %s)",
			reset.Format("2006-01-02 15:04:05"), wait.Round(time.Second))

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			svc.pnlTracker.ResetDaily()
			log.Println("[stratengine] 🌅 09:00 IST — pnlTracker daily counters reset (pre-market)")
		}
	}
}

// EOD exit timing, relative to the cutoff: strategies' own exits go first;
// eodSweepDelay later, anything the executor still holds is exited
// directly; eodCheckDelay after the cutoff, a position still open raises a
// critical alert while there is still time to close it by hand.
var (
	eodSweepDelay = 30 * time.Second
	eodCheckDelay = 5 * time.Minute
)

// eodMarketClose is when NSE stops accepting orders for the day.
const eodMarketCloseHour, eodMarketCloseMin = 15, 30

// eodCutoff returns the EOD auto-exit time (cfg.EODExitTime, IST) on the
// IST calendar day of t.
func (svc *Service) eodCutoff(t time.Time) time.Time {
	h, m, err := parseHHMM(svc.cfg.EODExitTime)
	if err != nil {
		h, m = defaultEODExitHour, defaultEODExitMin
	}
	d := t.In(ist)
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, ist)
}

// pastEODCutoff reports whether t is at or after today's EOD cutoff, when
// no new entry may open.
func (svc *Service) pastEODCutoff(t time.Time) bool {
	return !t.Before(svc.eodCutoff(t))
}

// nextEODRun returns when the EOD exit should next run. A start between
// the cutoff and the market close runs it at once, so a restart in that
// window cannot skip the day's exit.
func (svc *Service) nextEODRun(now time.Time) time.Time {
	now = now.In(ist)
	eod := svc.eodCutoff(now)
	closeAt := time.Date(now.Year(), now.Month(), now.Day(), eodMarketCloseHour, eodMarketCloseMin, 0, 0, ist)
	switch {
	case now.Before(eod):
		return eod
	case now.Before(closeAt):
		return now
	default:
		return svc.eodCutoff(now.AddDate(0, 0, 1))
	}
}

// eodAutoExitLoop runs the EOD auto-exit at the cutoff each day. F&O
// positions should not be held overnight unless explicitly intended, and
// exits sent at the 15:30 close itself are refused by the exchange.
func (svc *Service) eodAutoExitLoop(ctx context.Context) {
	for {
		now := time.Now()
		next := svc.nextEODRun(now)
		wait := next.Sub(now)
		log.Printf("[stratengine] EOD auto-exit scheduled at %s (in %s)", next.In(ist).Format("2006-01-02 15:04:05"), wait.Round(time.Second))

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		svc.runEODExit(ctx)
		if ctx.Err() != nil {
			return
		}

		// Reset dynamic strike picker for next trading day.
		if svc.strikePicker != nil {
			svc.strikePicker.Reset()
			atomic.StoreInt32(&svc.strikeResolved, 0)
			atomic.StoreInt32(&svc.strikeResolving, 0)
			atomic.StoreInt64(&svc.strikeRetryAfterNS, 0)
			log.Println("[stratengine] 🎯 strike picker reset for next trading day")
		}

		// Past today's close before scheduling again.
		now = time.Now().In(ist)
		closeAt := time.Date(now.Year(), now.Month(), now.Day(), eodMarketCloseHour, eodMarketCloseMin, 0, 0, ist)
		if !sleepCtx(ctx, closeAt.Sub(now)) {
			return
		}
	}
}

// runEODExit flattens the book for the day: every strategy's open
// positions first, then — after eodSweepDelay — any position the executor
// still holds that no strategy exited (restored after a crash, or left
// open by a failed exit), and finally a critical alert if anything is
// still open eodCheckDelay after the cutoff.
func (svc *Service) runEODExit(ctx context.Context) {
	cutoff := svc.eodCutoff(time.Now()).Format("15:04")
	reason := "EOD auto-exit at " + cutoff + " IST"
	log.Printf("[stratengine] ⏰ %s triggered", reason)

	sigs := svc.forceExitAllStrategies(reason)
	for _, sig := range sigs {
		svc.tfEngine.SendSignal(sig)
	}
	if len(sigs) > 0 {
		log.Printf("[stratengine] EOD: closing %d strategy positions", len(sigs))
		svc.notifyAsync(notification.Alert{
			Level:   notification.AlertWarning,
			Message: fmt.Sprintf("EOD AUTO-EXIT: closing %d positions at %s IST", len(sigs), cutoff),
		})
	} else {
		log.Println("[stratengine] EOD: no open strategy positions")
	}

	if !sleepCtx(ctx, eodSweepDelay) {
		return
	}
	if n := svc.exitExecutorEntries(ctx, reason+" (executor sweep)"); n > 0 {
		log.Printf("[stratengine] EOD: %d positions still held by the executor — exits dispatched", n)
		svc.operatorAlert(fmt.Sprintf("EOD: %d positions were still open after strategy exits — exits sent directly", n))
	}

	if !sleepCtx(ctx, eodCheckDelay-eodSweepDelay) {
		return
	}
	if open := svc.orderExecutor.GetEntryOrders(); len(open) > 0 {
		var names []string
		for k, r := range open {
			names = append(names, fmt.Sprintf("%s %s qty=%d real=%v", k, r.Symbol, r.Quantity, r.Real))
		}
		sort.Strings(names)
		svc.operatorAlert(fmt.Sprintf("EOD: %d positions STILL OPEN %s after the %s IST auto-exit — close manually before 15:30: %s",
			len(open), eodCheckDelay, cutoff, strings.Join(names, "; ")))
	}
}

// forceExitAllStrategies collects every strategy's exit signals.
func (svc *Service) forceExitAllStrategies(reason string) []strategy.Signal {
	var sigs []strategy.Signal
	if svc.nifty50Strategy != nil {
		sigs = append(sigs, svc.nifty50Strategy.ForceExitAll(reason)...)
	}
	if svc.nifty50SLStrategy != nil {
		sigs = append(sigs, svc.nifty50SLStrategy.ForceExitAll(reason)...)
	}
	if svc.nifty50SL2Strategy != nil {
		sigs = append(sigs, svc.nifty50SL2Strategy.ForceExitAll(reason)...)
	}
	if svc.nifty5010PtsStrategy != nil {
		sigs = append(sigs, svc.nifty5010PtsStrategy.ForceExitAll(reason)...)
	}
	if svc.nifty50RangeStrategy != nil {
		sigs = append(sigs, svc.nifty50RangeStrategy.ForceExitAll(reason)...)
	}
	if svc.nifty50FnO5M3MStrategy != nil {
		sigs = append(sigs, svc.nifty50FnO5M3MStrategy.ForceExitAll(reason)...)
	}
	return sigs
}

// exitExecutorEntries dispatches an exit for every position the executor
// holds that no exit is already working on, including ones no strategy
// knows about. Exits a strategy already queued are harmless duplicates: the
// executor finds the position closed, or its exit pending. Returns how many
// were dispatched.
func (svc *Service) exitExecutorEntries(ctx context.Context, reason string) int {
	n := 0
	for _, rec := range svc.orderExecutor.GetEntryOrders() {
		if rec.ExitOrderID != "" {
			continue
		}
		svc.dispatchOrder(ctx, strategy.Signal{
			StrategyName: rec.StrategyName,
			Side:         strategy.PositionSide(rec.PositionSide),
			Action:       strategy.ActionExit,
			Token:        rec.IndexToken,
			Exchange:     rec.IndexExchange,
			Reason:       reason,
		})
		n++
	}
	return n
}

// sleepCtx waits for d, returning false if ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
