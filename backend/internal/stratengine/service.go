package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"trading-systemv1/internal/heartbeat"
	"trading-systemv1/internal/instruments"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/notification"
	"trading-systemv1/internal/optionpicker"
	"trading-systemv1/internal/orderexec"
	"trading-systemv1/internal/pnlstore"
	"trading-systemv1/internal/portfolio"
	redisstore "trading-systemv1/internal/store/redis"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

// getEnvInt reads an integer from environment variable with a default fallback
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return defaultVal
}

// Service is the top-level orchestrator for the strategy engine.
// Runs NIFTY50_SR (paper only).
type Service struct {
	cfg Config

	tfEngine   *strategy.TFEngine
	srStrategy *strategy.Nifty50SR
	srExtras   []*srExtra // SR on underlyings beyond NIFTY (INDEX_TOKEN list)
	// Telegram call strategies: TELEGRAM_SIGNAL first, then one per
	// STRAT_TG_CHANNELS chat (tgChats routes those chats).
	tgStrategies []*strategy.TelegramSignal
	tgChats      map[int64]*strategy.TelegramSignal
	tgRecv       tgReceipts // call key → tgsignal receipt time (latency timeline)

	redisReader   *redisstore.Reader
	redisWriter   *redisstore.Writer
	journal       *strategy.SignalJournal
	pnlLedger     *pnlLedger      // closed-trade ledger (pnl_ledger.go); nil when off
	pnlStore      *pnlstore.Store // its SQLite store
	sweeps        *sweepWriter    // SR liquidity sweeps into journal (sweeps.go)
	notifier      notification.Notifier
	orderExecutor *orderexec.OrderExecutor
	strikePicker  atomic.Pointer[instruments.StrikePicker] // swapped by the ATM goroutine

	// Global option picker (picker_wiring.go); nil when STRAT_PICKER_MODE=off.
	picker    *optionpicker.Picker
	pickerDec pickerDecisions

	// Contract resolution (option_select.go). legResolver is a test hook;
	// legPicker is built lazily when no ATM picker exists.
	legResolver   legResolver
	legPicker     *instruments.StrikePicker
	legPickerOnce sync.Once

	ladderState                // strike ladder + per-entry option selection (option_select.go)
	deltaState                 // entry quality guard (delta_guard.go)
	futures     futureResolver // test hook; nil = instrument master

	srPublishHook         func(payload string) // test hook for publishSRState
	srxPublishHook        func(payload string) // test hook for publishSRXState
	strikeSelXPublishHook func(payload string) // test hook for publishExtraStrikeSels

	refused            refusedLog           // today's blocked entries (refused.go)
	refusedPublishHook func(payload string) // test hook for recordRefusedEntry

	exitPol exitPolicyState // unified exit layer (exit_policy.go)

	strikeSel            strikeSelState       // NIFTY50_SR strike selection view (sr_strike_view.go)
	strikeSelPublishHook func(payload string) // test hook for publishStrikeSel

	// Portfolio & P&L tracking
	pnlTracker *portfolio.PnLTracker
	portfolio  *portfolio.Portfolio

	// Session manager for health monitoring
	sessionManager *smartconnect.SessionManager
	// orderWS is the order-status socket state for the Health page.
	orderWS orderWSStats

	streams    []string
	tfCandleCh chan model.TFCandle
	tickCh     chan model.Tick // raw ticks for live stoploss checking

	// Atomic flags for strike resolution (shared between tickRouterLoop and eodAutoExitLoop)
	strikeResolved     int32 // 0 = not resolved, 1 = resolved
	strikeResolving    int32 // 0 = idle, 1 = resolution in progress
	strikeRetryAfterNS int64 // unix nanos; cooldown: don't retry until after this time

	// Dashboard kill switch (cmd:config_update); see killSwitchActive.
	uiKillSwitch atomic.Bool
	// reconcileHalt blocks new entries while broker positions disagree
	// with the executor in a way it must not fix itself.
	reconcileHalt atomic.Bool

	// snapshotMu serialises saveSnapshot (periodic loop + after each order)
	// so an older snapshot can never overwrite a newer one in Redis.
	snapshotMu sync.Mutex

	// Per-strategy FIFO order workers; see dispatchOrder.
	orderQueuesMu sync.Mutex
	orderQueues   map[string]chan strategy.Signal
	orderRunner   func(context.Context, strategy.Signal) // nil = executeAndPersist

	liveOrdersView // dashboard pub:orders state (liveorders.go)
}

// New creates a new strategy engine Service.
func New(cfg Config) (*Service, error) {
	// Load holidays from NSE API / cache / hardcoded fallback
	configDir := markethours.GetConfigDir()
	markethours.InitHolidays(configDir)
	markethours.StartBackgroundRefresher(context.Background(), configDir)

	// ── Pre-load Instrument Master for dynamic lot sizes ──
	log.Println("[stratengine] 📥 Pre-loading instrument master for lot sizes...")
	im := instruments.GetInstrumentMaster()
	if err := im.EnsureLoaded(30 * time.Second); err != nil {
		log.Printf("[stratengine] ⚠️  Instrument master load failed (will use fallback NIFTY_LOT_SIZE): %v", err)
	} else {
		log.Println("[stratengine] ✅ Instrument master loaded successfully")
	}

	svc := &Service{
		cfg:            cfg,
		tfCandleCh:     make(chan model.TFCandle, 5000),
		tickCh:         make(chan model.Tick, 10000),
		liveOrdersView: liveOrdersView{liveOrders: make(map[string]*liveOrderRuntime)},
	}

	if err := svc.openIO(cfg); err != nil {
		return nil, err
	}

	// ── Notifier ──
	if cfg.NotifyWebhook != "" {
		svc.notifier = notification.NewWebhookNotifier(cfg.NotifyWebhook)
	} else {
		svc.notifier = notification.NewLogNotifier()
	}

	// ── Portfolio & P&L ──
	svc.portfolio = portfolio.New()
	svc.pnlTracker = portfolio.NewPnLTracker()

	svc.buildStrategies(cfg)
	svc.buildExecution(cfg)

	// ── Dynamic Strike Picker ──
	if cfg.DynamicStrikes {
		log.Println("[stratengine] 🎯 dynamic strike selection ENABLED")
		// StrikePicker will be created after the OrderExecutor has a SmartConnect session.
		// The actual resolution happens on the first NIFTY tick (see tickRouterLoop).
	}
	if cfg.OptionAutomation {
		if cfg.DynamicStrikes {
			log.Println("[stratengine] 🤖 signal-time option automation ENABLED")
		} else {
			log.Println("[stratengine] ⚠️  option automation requested but dynamic strikes are disabled")
		}
	}

	return svc, nil
}

// openIO opens the Redis reader and writer and the signal journal,
// closing what it opened on error.
func (svc *Service) openIO(cfg Config) error {
	// ── Redis reader (stream consumer) ──
	var err error
	svc.redisReader, err = redisstore.NewReader(redisstore.ReaderConfig{
		Addr:          cfg.RedisAddr,
		Password:      cfg.RedisPassword,
		ConsumerGroup: cfg.ConsumerGroup,
		ConsumerName:  cfg.ConsumerName,
	})
	if err != nil {
		return fmt.Errorf("redis reader: %w", err)
	}

	// ── Redis writer (for snapshot persistence) ──
	svc.redisWriter, err = redisstore.New(redisstore.WriterConfig{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	if err != nil {
		svc.redisReader.Close()
		return fmt.Errorf("redis writer: %w", err)
	}

	// ── Signal journal (SQLite) ──
	os.MkdirAll("data", 0o755)
	svc.journal, err = strategy.NewSignalJournal(cfg.JournalPath)
	if err != nil {
		svc.redisReader.Close()
		svc.redisWriter.Close()
		return fmt.Errorf("signal journal: %w", err)
	}

	return nil
}

// buildStrategies builds NIFTY50_SR (and one SR per extra underlying) and
// registers them on the TF engine when SR is enabled.
func (svc *Service) buildStrategies(cfg Config) {
	// ── Strategies ──
	// NIFTY50_SR: regime-aware support/resistance (fade, breakout-retest,
	// trend pullback); strike picked from the live chain by greeks.
	srCfg := strategy.DefaultNifty50SRConfig()
	srCfg.IndexToken = "NSE:99926000"
	srCfg.MaxDayLossPts = cfg.SRMaxDayLossPts
	srCfg.DayExtremePct, srCfg.DayRunPts, srCfg.DayExtremePuts = cfg.SRDayExtremePct, cfg.SRDayRunPts, cfg.SRDayExtremePuts
	srCfg.DayExtremeFromMin = cfg.SRDayExtremeFrom
	srCfg.BoxBars, srCfg.BoxATRPct, srCfg.GateMode = cfg.SRBoxBars, cfg.SRBoxATRPct, cfg.SRGateMode
	srCfg.AdvicePullFastEMA, srCfg.AdviceContinueMin = cfg.SRAdvicePullFastEMA, cfg.SRAdviceContinueMin
	srCfg.AdviceMaxRiskATRPct, srCfg.StrongTrendADX = cfg.SRAdviceMaxRiskATRPct, cfg.SRStrongTrendADX
	srCfg.SupportStopMode, srCfg.SupportStopTouches = cfg.SRSupportStopMode, cfg.SRSupportStopTouches
	srCfg.SupportStopZonePts = cfg.SRSupportStopZonePts
	srCfg.SweepEnabled = cfg.SRSweepMinWickPts > 0
	srCfg.SweepMinWickPts, srCfg.SweepCooldownMin, srCfg.SweepWarnMin = cfg.SRSweepMinWickPts, cfg.SRSweepCooldownMin, cfg.SRSweepWarnMin
	srCfg.FNOTargetPct = cfg.FNOTargetPct
	svc.srStrategy = strategy.NewNifty50SR(cfg.Qty, paperTarget(srCfg, "NIFTY50_SR", cfg.LiveStrategies))
	svc.srStrategy.SetExpiry(instruments.NewStrikePicker(nil).NextExpiry(time.Now()))

	svc.syncStrategyFNOTokens(cfg.CallFNOToken, cfg.PutFNOToken)
	svc.initExitPolicy()

	svc.tfEngine = strategy.NewTFEngine(1000)

	// NIFTY50_SR is paper unless STRAT_LIVE_STRATEGIES names it.
	registered := 0
	if cfg.SREnabled {
		svc.tfEngine.Register(svc.srStrategy)
		registered++
		log.Println("[stratengine] 🟢 registered NIFTY50_SR (S/R fade + breakout-retest + trend pullback, greeks strike, paper)")
		for _, ex := range svc.buildSRExtras(srCfg) {
			svc.tfEngine.Register(ex.strat)
			registered++
			log.Printf("[stratengine] 🟢 registered %s on %s (options %s, %s)", ex.strat.Name(), ex.prof.IndexKey(), ex.prof.FNOExchange, ex.prof)
		}
	}
	if cfg.TGEnabled {
		for _, s := range svc.buildTGStrategies(cfg) {
			s.SetOnEvent(func(callKey, event, detail string) { svc.publishTGOutcomeAsync(s.Name(), callKey, event, detail) })
			s.SetOnDrop(func(a strategy.TGArm, reason string) { svc.onTGDrop(s.Name(), a, reason) })
			svc.tfEngine.Register(s)
			registered++
			log.Printf("[stratengine] 🟢 registered %s (Telegram calls on %s, ladder trail %d%%, fixed target %d%%)",
				s.Name(), model.TGSignalChannel, cfg.TGLadderTrailPct, cfg.TGTargetPct)
		}
	}
	if registered == 0 {
		log.Println("[stratengine] ⚪ No strategies registered — all detached from engine")
	}

}

// buildExecution creates the session manager (live orders), the order
// executor and the option pickers.
func (svc *Service) buildExecution(cfg Config) {
	// ── Session Manager for Order Execution ──
	var sessionManager *smartconnect.SessionManager
	if cfg.LiveOrders && cfg.AngelAPIKey != "" && cfg.AngelClientID != "" && cfg.AngelPassword != "" && cfg.AngelTOTP != "" {
		log.Println("[stratengine] 🔑 initializing session manager for order execution...")
		log.Println("[stratengine] 📦 session manager created (TTL: 30min, proactive refresh at: 20min)")
		sessionManager = smartconnect.NewSessionManager(cfg.AngelAPIKey, cfg.AngelClientID, cfg.AngelPassword, cfg.AngelTOTP, false)
		if err := sessionManager.Login(); err != nil {
			log.Printf("[stratengine] ⚠️  session manager login failed: %v — order executor will create own session", err)
			sessionManager = nil
		} else {
			sessionManager.Start() // Start proactive refresh
			log.Println("[stratengine] ✅ session manager initialized with auto-refresh")
			svc.sessionManager = sessionManager // Store for health monitoring
		}
	}

	// ── Order Executor ──
	svc.orderExecutor = orderexec.NewOrderExecutor(orderexec.Config{
		LotSizeOf: func(symbol string) (int64, error) {
			return instruments.GetInstrumentMaster().GetLotSize(symbol)
		},
		LiveOrders:           cfg.LiveOrders,
		Qty:                  cfg.Qty,
		CallFNOToken:         cfg.CallFNOToken,
		CallFNOSymbol:        cfg.CallFNOSymbol,
		PutFNOToken:          cfg.PutFNOToken,
		PutFNOSymbol:         cfg.PutFNOSymbol,
		FNOExchange:          cfg.FNOExchange,
		FNOOrderType:         cfg.FNOOrderType,
		FNOProductType:       cfg.FNOProductType,
		FNOStopLossPaise:     cfg.GTTStopLossPaise,
		GTTTargetBps:         cfg.GTTTargetBps,
		LiveStrategies:       cfg.LiveStrategies,
		LiveLots:             cfg.LiveLots,
		FreezeQty:            cfg.FreezeQty,
		MaxDayLossPaise:      cfg.LiveMaxDayLossPaise,
		MaxDayLossTotalPaise: cfg.LiveMaxDayLossTotalPaise,
		RequireJournal:       cfg.LiveOrders,
		ProtectionMode:       cfg.ProtectionMode,
		ExecMode:             cfg.ExecMode,
		ChaseTicks:           cfg.ChaseTicks,
		ChaseReprices:        cfg.ChaseReprices,
		ChaseMaxBps:          cfg.ChaseMaxBps,
		MarginBufferPct:      cfg.MarginBufferPct,
		AngelAPIKey:          cfg.AngelAPIKey,
		AngelClientID:        cfg.AngelClientID,
		AngelPassword:        cfg.AngelPassword,
		AngelTOTP:            cfg.AngelTOTP,
		LogPrefix:            "[order_executor]",
		SessionManager:       sessionManager, // Pass session manager for auto-refresh

		PaperSlippageBps:      cfg.PaperSlippageBps,
		PaperSlippageMinPaise: cfg.PaperSlippageMinPaise,
	})

	// P&L source for the executor's daily max-loss gate on real entries.
	svc.orderExecutor.SetPnLTracker(svc.pnlTracker)
	svc.attachOrderJournal()
	svc.attachPNLLedger()

	// ── Global option picker (off | shadow | on) ──
	svc.picker = svc.newPicker()
	svc.buildExtraPickers()

}

// Run starts all subsystems and blocks until ctx is cancelled.
func (svc *Service) Run(ctx context.Context) error {
	log.Println("[stratengine] starting Strategy Engine...")

	svc.restoreAndWire(ctx)
	svc.restoreTGPosition(ctx)
	svc.warmupRangeStrategies()
	svc.refreshVolumeToken(ctx, time.Now())
	stopMetrics := svc.startMetrics()
	defer stopMetrics()

	// ── Dashboard kill switch + broker position reconciliation ──
	go svc.configUpdateLoop(ctx)
	go svc.positionReconcileLoop(ctx)
	svc.startOrderStream(ctx)

	// ── Re-subscribe the picker universe + strike ladder on market open
	// (mdengine (re)connect), not only at midnight ──
	go svc.marketStateLoop(ctx)

	// ── Discover streams for TF=60 (1m candles) ──
	svc.streams = svc.buildStreams(ctx)
	log.Printf("[stratengine] consuming from %d streams: %v", len(svc.streams), svc.streams)

	// ── Ensure consumer groups ──
	if len(svc.streams) > 0 {
		if err := svc.redisReader.EnsureConsumerGroup(ctx, svc.streams); err != nil {
			log.Printf("[stratengine] WARNING: consumer group setup: %v", err)
		}
	}

	// ── Recover pending messages ──
	if len(svc.streams) > 0 {
		if err := svc.redisReader.RecoverPending(ctx, svc.streams, svc.tfCandleCh); err != nil {
			log.Printf("[stratengine] pending recovery error: %v", err)
		}
	}

	// ── Start PEL reclaimer ──
	svc.startPELReclaimer(ctx)

	// ── Start heartbeat publisher ──
	go heartbeat.NewPublisher("stratengine", svc.redisWriter.Client()).Run(ctx)

	// ── Start session health publisher ──
	if svc.sessionManager != nil {
		go svc.publishSessionHealth(ctx)
	}

	// ── Start consumer (Redis → tfCandleCh) ──
	svc.startConsumer(ctx)

	// ── Start TFEngine (tfCandleCh → signals) ──
	go svc.tfEngine.Run(ctx, svc.tfCandleCh)

	// ── Start tick subscriber (Redis PubSub → tickCh) ──
	go func() {
		if err := svc.redisReader.SubscribeTicks(ctx, svc.tickCh); err != nil {
			log.Printf("[stratengine] tick subscriber error: %v", err)
		}
	}()

	// ── Start tick router (tickCh → strategies for live SL + FNO LTP tracking) ──
	tfTickCh := make(chan model.Tick, 10000)
	go svc.tickRouterLoop(ctx, tfTickCh)
	go svc.tfEngine.RunTicks(ctx, tfTickCh)

	// ── Start signal processor (signals → journal + notify + orders) ──
	go svc.signalLoop(ctx)

	// ── Global option picker: chain refresh + streamed universe ──
	go svc.runPicker(ctx)

	// ── NIFTY50_SR strategy dashboard state (pub:sr) ──
	if svc.cfg.SREnabled {
		go svc.srStateLoop(ctx)
	}

	// ── Open-position context for exitwatch (pub:poscontext, internal) ──
	go svc.posContextLoop(ctx)

	// ── exitwatch exit requests (cmd:exitwatch:exit) for ExitWatchAuto ──
	if len(svc.cfg.ExitWatchAuto) > 0 {
		go svc.exitRequestLoop(ctx)
	}

	// ── Dashboard position commands (cmd:position): paper SL/target/exit ──
	if svc.cfg.SREnabled || len(svc.tgStrategies) > 0 {
		go svc.positionCommandLoop(ctx)
	}

	// ── Telegram group calls (cmd:tgsignal) for TELEGRAM_SIGNAL ──
	if len(svc.tgStrategies) > 0 {
		go svc.tgSignalLoop(ctx)
		go svc.tgFollowUpLoop(ctx)
	}

	// ── Unified exit layer: minute evaluation + config reload ──
	go svc.exitPolicyLoop(ctx)
	go svc.exitPolicyReloadLoop(ctx)

	// ── NIFTY50_SR strike selection view (pub:strikesel) ──
	if svc.cfg.SREnabled || svc.picker != nil {
		go svc.strikeSelLoop(ctx)
	}

	// ── Option chain warm-up: entries read the cached chain only ──
	if svc.needsEntryChain() {
		go svc.chainWarmLoop(ctx)
	}

	// ── Start snapshot loop ──
	go svc.snapshotLoop(ctx)

	// ── Stale position cleanup: exit any position from a previous day ──
	// Happens once at startup. Positions restore from snapshot but if the snapshot
	// is from yesterday they need to be force-exited so we start the day clean.
	svc.clearStalePositions()
	svc.publishLiveOrders(ctx)

	// ── EOD auto-exit: close all positions at STRAT_EOD_EXIT_TIME (default 15:15 IST) ──
	go svc.eodAutoExitLoop(ctx)

	// ── Daily PnL reset at 00:00 IST so the profit cap, win/loss tallies,
	// and trade counters do not leak yesterday's values into today.
	go svc.dailyResetLoop(ctx)

	// ── Banner ──
	log.Println("[stratengine] ╔════════════════════════════════════════════════════════╗")
	log.Println("[stratengine] ║  Strategy Engine Active                               ║")
	log.Println("[stratengine] ║                                                       ║")
	log.Println("[stratengine] ║  [Redis Streams] → [TFEngine] → [Journal + Notify]    ║")
	log.Printf("[stratengine] ║  Strategies: NIFTY50, HYBRID                         ║")
	log.Printf("[stratengine] ║  Qty: %d | Snapshot: %ds | Live: %v                ║", svc.cfg.Qty, svc.cfg.SnapshotIntervalS, svc.cfg.LiveOrders)
	if svc.cfg.KillSwitch {
		log.Println("[stratengine] ║  ⚠️  KILL SWITCH ACTIVE — entries blocked             ║")
	}
	log.Println("[stratengine] ╚════════════════════════════════════════════════════════╝")
	log.Println("[stratengine] ✅ all systems running.")

	// Block until context cancelled
	<-ctx.Done()

	// ── Graceful shutdown ──
	svc.shutdown()
	return nil
}

// tickRouterLoop reads raw ticks and fans them out to:
// 1. tfTickCh — for strategy index SL checks (RunTicks)
// 2. orderExecutor.UpdateLTP — for FNO price tracking
// 3. strikePicker resolution — resolve ATM on first NIFTY tick of the day
func (svc *Service) tickRouterLoop(ctx context.Context, tfTickCh chan<- model.Tick) {
	// niftyToken is the NIFTY50 index token used to detect spot price
	niftyToken := "99926000" // NIFTY 50 index token

	for {
		select {
		case <-ctx.Done():
			return
		case tick, ok := <-svc.tickCh:
			if !ok {
				return
			}
			// Update FNO LTP tracker
			svc.orderExecutor.UpdateLTP(tick)
			if svc.picker != nil {
				svc.picker.OnTick(tick)
			}
			for _, ex := range svc.srExtras {
				if ex.optPicker != nil {
					ex.optPicker.OnTick(tick)
				}
			}
			svc.updateLiveOrdersFromTick(ctx, tick)
			svc.exitPolicyTick(tick)
			if tick.Token == niftyToken && svc.cfg.SREnabled {
				svc.refreshStrikeLadder(ctx, tick.Price)
			}
			if svc.cfg.SREnabled {
				svc.extraIndexTick(ctx, tick)
			}

			// Dynamic strike resolution: retry on every NIFTY tick until successful.
			// Uses atomic flags to prevent concurrent goroutines and track success.
			now := time.Now()
			if tick.Token == niftyToken && tick.Price > 0 &&
				svc.cfg.DynamicStrikes &&
				atomic.LoadInt32(&svc.strikeResolved) == 0 &&
				atomic.LoadInt32(&svc.strikeResolving) == 0 &&
				now.UnixNano() > atomic.LoadInt64(&svc.strikeRetryAfterNS) {

				// Mark as resolving to prevent concurrent goroutines
				atomic.StoreInt32(&svc.strikeResolving, 1)

				go func(spotPrice int64) {
					defer atomic.StoreInt32(&svc.strikeResolving, 0) // allow retry on failure

					// Try to get SmartConnect session from OrderExecutor first.
					// If unavailable (e.g. LiveOrders=false / dry-run), create a
					// standalone session using the Angel credentials from config.
					sc := svc.orderExecutor.GetSmartConnect()
					if sc == nil {
						if svc.cfg.AngelAPIKey == "" || svc.cfg.AngelClientID == "" {
							log.Println("[stratengine] ⚠️  cannot resolve strikes: no Angel credentials configured")
							return
						}
						log.Println("[stratengine] 🔑 creating standalone SmartConnect session for strike resolution...")
						standaloneSC := smartconnect.NewSmartConnect(smartconnect.Config{
							APIKey: svc.cfg.AngelAPIKey,
						})
						totpCode, err := totp.GenerateCode(svc.cfg.AngelTOTP, time.Now())
						if err != nil {
							log.Printf("[stratengine] ⚠️  TOTP generation failed for strike picker: %v (will retry)", err)
							return
						}
						if _, err := standaloneSC.GenerateSession(svc.cfg.AngelClientID, svc.cfg.AngelPassword, totpCode); err != nil {
							log.Printf("[stratengine] ⚠️  standalone SmartConnect login failed: %v (will retry)", err)
							return
						}
						sc = standaloneSC
						log.Println("[stratengine] ✅ standalone SmartConnect session established for strike resolution")
					}
					picker := instruments.NewStrikePicker(sc)
					if !picker.ResolveATM(spotPrice) {
						atomic.StoreInt64(&svc.strikeRetryAfterNS, time.Now().Add(60*time.Second).UnixNano())
						log.Println("[stratengine] ⚠️  strike resolution failed (will retry in 60s)")
						return
					}

					svc.strikePicker.Store(picker)
					svc.orderExecutor.SetStrikePicker(picker)
					if svc.srStrategy != nil {
						svc.srStrategy.SetExpiry(picker.NextExpiry(time.Now()))
					}

					ce := picker.GetCallToken()
					pe := picker.GetPutToken()
					svc.syncStrategyFNOTokens(ce.Token, pe.Token)
					log.Printf("[stratengine] 🎯 ATM resolved: CE=%s (token=%s) PE=%s (token=%s)",
						ce.Symbol, ce.Token, pe.Symbol, pe.Token)

					// Notify about dynamic strike selection
					svc.notifier.Send(ctx, notification.Alert{
						Level:   notification.AlertInfo,
						Title:   "Dynamic Strike Selected",
						Message: fmt.Sprintf("ATM resolved: CE=%s PE=%s", ce.Symbol, pe.Symbol),
					})

					// Publish resolved FNO tokens to mdengine for WS subscription
					// (SnapQuote when the picker runs, so the ATM pair has
					// bid/ask like every other picker-tracked contract).
					svc.publishFNOSubscription(ctx, ce.Token, pe.Token)

					// Publish strike info for frontend FNO Instruments tab
					// Use lot size from StrikeInfo (fetched from Angel One), fallback to env/default
					lotSize := ce.LotSize
					if lotSize <= 0 {
						lotSize = int64(getEnvInt("NIFTY_LOT_SIZE", 65))
					}
					strikePayload, _ := json.Marshal(map[string]interface{}{
						"resolved":    true,
						"spot_price":  spotPrice,
						"atm_strike":  ce.Strike,
						"call":        map[string]string{"token": ce.Token, "symbol": ce.Symbol},
						"put":         map[string]string{"token": pe.Token, "symbol": pe.Symbol},
						"resolved_at": time.Now().UTC().Format(time.RFC3339),
						"lot_size":    lotSize,
						"qty":         svc.cfg.Qty,
						"call_ltp":    svc.orderExecutor.GetLTP(ce.Token),
						"put_ltp":     svc.orderExecutor.GetLTP(pe.Token),
					})
					svc.redisWriter.Client().Set(ctx, model.StrikeKey, strikePayload, 24*time.Hour)
					svc.redisWriter.Client().Publish(ctx, model.StrikeChannel, string(strikePayload))
					log.Println("[stratengine] 📡 published strike info to Redis")

					// ✅ Only mark as resolved AFTER everything succeeded
					atomic.StoreInt32(&svc.strikeResolved, 1)
				}(tick.Price)
			}

			// Forward to strategy engine for index SL checks
			select {
			case tfTickCh <- tick:
			default:
				// drop if channel full
			}
		}
	}
}

func (svc *Service) publishStrikeInfo(ctx context.Context, spotPrice int64) {
	sp := svc.strikePicker.Load()
	if sp == nil || !sp.Resolved() {
		return
	}

	ce := sp.GetCallToken()
	pe := sp.GetPutToken()
	atmStrike := ce.Strike
	if atmStrike == 0 {
		atmStrike = pe.Strike
	}

	// Use lot size from StrikeInfo (fetched from Angel One), fallback to env/default
	lotSize := ce.LotSize
	if lotSize <= 0 {
		lotSize = int64(getEnvInt("NIFTY_LOT_SIZE", 65))
	}

	strikePayload, _ := json.Marshal(map[string]interface{}{
		"resolved":    true,
		"spot_price":  spotPrice,
		"atm_strike":  atmStrike,
		"call":        map[string]string{"token": ce.Token, "symbol": ce.Symbol},
		"put":         map[string]string{"token": pe.Token, "symbol": pe.Symbol},
		"resolved_at": time.Now().UTC().Format(time.RFC3339),
		"lot_size":    lotSize,
		"qty":         svc.cfg.Qty,
		"call_ltp":    svc.orderExecutor.GetLTP(ce.Token),
		"put_ltp":     svc.orderExecutor.GetLTP(pe.Token),
	})
	svc.redisWriter.Client().Set(ctx, model.StrikeKey, strikePayload, 24*time.Hour)
	svc.redisWriter.Client().Publish(ctx, model.StrikeChannel, string(strikePayload))
}

// publishSessionHealth publishes session health to Redis every 30 seconds
// This allows the API Gateway to monitor StratEngine's session status
func (svc *Service) publishSessionHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	publish := func() {
		if svc.sessionManager == nil {
			return
		}

		sessionInfo := svc.sessionManager.GetSessionInfo()
		healthData := map[string]interface{}{
			"service":              "stratengine",
			"purpose":              "Order Execution",
			"healthy":              svc.sessionManager.IsHealthy(),
			"valid":                sessionInfo["valid"],
			"age_minutes":          sessionInfo["age_minutes"],
			"time_until_refresh":   sessionInfo["time_until_refresh"],
			"circuit_breaker_open": sessionInfo["circuit_breaker_open"],
			"total_refreshes":      sessionInfo["total_refreshes"],
			"successful_refreshes": sessionInfo["successful_refreshes"],
			"failed_refreshes":     sessionInfo["failed_refreshes"],
			"last_refresh":         sessionInfo["last_refresh"],
			"last_updated":         time.Now().UTC().Format(time.RFC3339),
		}

		data, err := json.Marshal(healthData)
		if err != nil {
			log.Printf("[stratengine] ⚠️  failed to marshal session health: %v", err)
			return
		}

		// Publish to Redis with 2-minute TTL (in case stratengine crashes)
		if err := svc.redisWriter.Client().Set(ctx, "session:stratengine:health", string(data), 2*time.Minute).Err(); err != nil {
			log.Printf("[stratengine] ⚠️  failed to publish session health: %v", err)
		}
	}

	// Publish immediately on start
	publish()

	// Then publish every 30 seconds
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}
