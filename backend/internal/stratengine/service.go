package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trading-systemv1/internal/heartbeat"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/notification"
	"trading-systemv1/internal/orderexec"
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
// Runs only NIFTY50_FNO in production.
type Service struct {
	cfg Config

	tfEngine               *strategy.TFEngine
	nifty50Strategy        *strategy.Nifty50FnO
	nifty50SLStrategy      *strategy.Nifty50FnOSL
	nifty50SL2Strategy     *strategy.Nifty50FnOSL2
	nifty5010PtsStrategy   *strategy.Nifty5010Pts
	nifty50RangeStrategy   *strategy.Nifty50Range
	nifty50FnO5M3MStrategy *strategy.Nifty50FnO5M3M

	redisReader   *redisstore.Reader
	redisWriter   *redisstore.Writer
	journal       *strategy.SignalJournal
	notifier      notification.Notifier
	orderExecutor *orderexec.OrderExecutor
	strikePicker  *orderexec.StrikePicker

	// Portfolio & P&L tracking
	pnlTracker *portfolio.PnLTracker
	portfolio  *portfolio.Portfolio

	// Session manager for health monitoring
	sessionManager *smartconnect.SessionManager

	streams    []string
	tfCandleCh chan model.TFCandle
	tickCh     chan model.Tick // raw ticks for live stoploss checking

	// Atomic flags for strike resolution (shared between tickRouterLoop and eodAutoExitLoop)
	strikeResolved     int32 // 0 = not resolved, 1 = resolved
	strikeResolving    int32 // 0 = idle, 1 = resolution in progress
	strikeRetryAfterNS int64 // unix nanos; cooldown: don't retry until after this time

	// Dashboard kill switch (cmd:config_update); see killSwitchActive.
	uiKillSwitch atomic.Bool

	// snapshotMu serialises saveSnapshot (periodic loop + after each order)
	// so an older snapshot can never overwrite a newer one in Redis.
	snapshotMu sync.Mutex

	// Per-strategy FIFO order workers; see dispatchOrder.
	orderQueuesMu sync.Mutex
	orderQueues   map[string]chan strategy.Signal
	orderRunner   func(context.Context, strategy.Signal) // nil = executeAndPersist

	liveOrdersMu       sync.Mutex
	liveOrders         map[string]*liveOrderRuntime
	lastLiveOrdersJSON string
}

type trackedInstrument struct {
	token    string
	exchange string
}

type liveOrderRuntime struct {
	StrategyName string
	Side         strategy.PositionSide
	Token        string
	EntryPrice   int64
	BestPrice    int64
	CurrentPrice int64
}

type liveOrderPayload struct {
	StrategyName    string                `json:"strategy_name"`
	Side            strategy.PositionSide `json:"side"`
	FNOToken        string                `json:"fno_token,omitempty"`
	EntryFNOPrice   int64                 `json:"entry_fno_price,omitempty"`
	CurrentFNOPrice int64                 `json:"current_fno_price,omitempty"`
	BestFNOPrice    int64                 `json:"best_fno_price,omitempty"`
	StoplossPrice   int64                 `json:"stoploss_price,omitempty"`
	StoplossKind    string                `json:"stoploss_kind,omitempty"`
}

// New creates a new strategy engine Service.
func New(cfg Config) (*Service, error) {
	// Load holidays from NSE API / cache / hardcoded fallback
	configDir := markethours.GetConfigDir()
	markethours.InitHolidays(configDir)
	markethours.StartBackgroundRefresher(context.Background(), configDir)

	// ── Pre-load Instrument Master for dynamic lot sizes ──
	log.Println("[stratengine] 📥 Pre-loading instrument master for lot sizes...")
	im := orderexec.GetInstrumentMaster()
	if err := im.EnsureLoaded(30 * time.Second); err != nil {
		log.Printf("[stratengine] ⚠️  Instrument master load failed (will use fallback NIFTY_LOT_SIZE): %v", err)
	} else {
		log.Println("[stratengine] ✅ Instrument master loaded successfully")
	}

	svc := &Service{
		cfg:        cfg,
		tfCandleCh: make(chan model.TFCandle, 5000),
		tickCh:     make(chan model.Tick, 10000),
		liveOrders: make(map[string]*liveOrderRuntime),
	}

	// ── Redis reader (stream consumer) ──
	var err error
	svc.redisReader, err = redisstore.NewReader(redisstore.ReaderConfig{
		Addr:          cfg.RedisAddr,
		Password:      cfg.RedisPassword,
		ConsumerGroup: cfg.ConsumerGroup,
		ConsumerName:  cfg.ConsumerName,
	})
	if err != nil {
		return nil, fmt.Errorf("redis reader: %w", err)
	}

	// ── Redis writer (for snapshot persistence) ──
	svc.redisWriter, err = redisstore.New(redisstore.WriterConfig{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	if err != nil {
		svc.redisReader.Close()
		return nil, fmt.Errorf("redis writer: %w", err)
	}

	// ── Signal journal (SQLite) ──
	os.MkdirAll("data", 0o755)
	svc.journal, err = strategy.NewSignalJournal(cfg.JournalPath)
	if err != nil {
		svc.redisReader.Close()
		svc.redisWriter.Close()
		return nil, fmt.Errorf("signal journal: %w", err)
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

	// ── Strategies ──
	// Set IndexToken so live ticks are keyed correctly for dual-layer SL.
	nifty50Cfg := strategy.DefaultNifty50FnOConfig()
	nifty50Cfg.IndexToken = "NSE:99926000"
	svc.nifty50Strategy = strategy.NewNifty50FnOWithConfig(cfg.Qty, nifty50Cfg)
	svc.nifty50SLStrategy = strategy.NewNifty50FnOSL(cfg.Qty)
	svc.nifty50SL2Strategy = strategy.NewNifty50FnOSL2(cfg.Qty)

	nifty5010PtsCfg := strategy.DefaultNifty5010PtsConfig()
	svc.nifty5010PtsStrategy = strategy.NewNifty5010PtsWithConfig(cfg.Qty, nifty5010PtsCfg)

	// Initialize NIFTY50_RANGE strategy with 10-point threshold
	nifty50RangeCfg := strategy.DefaultNifty50RangeConfig()
	nifty50RangeCfg.IndexToken = "NSE:99926000"
	svc.nifty50RangeStrategy = strategy.NewNifty50RangeWithConfig(cfg.Qty, nifty50RangeCfg)

	// Initialize NIFTY50_FNO_5M3M strategy (5m entry, 3m exit with SMA21/EMA6)
	nifty50FnO5M3MCfg := strategy.DefaultNifty50FnO5M3MConfig()
	nifty50FnO5M3MCfg.IndexToken = "NSE:99926000"
	svc.nifty50FnO5M3MStrategy = strategy.NewNifty50FnO5M3MWithConfig(cfg.Qty, nifty50FnO5M3MCfg)

	// Wire external market state: 10PTS and FNO_SL query FNO strategy's regime detection.
	if svc.nifty50Strategy != nil {
		fnoStrat := svc.nifty50Strategy
		if svc.nifty5010PtsStrategy != nil {
			svc.nifty5010PtsStrategy.SetMarketStateProvider(func() strategy.EntryMarketState {
				ctx := fnoStrat.EntryAutomationContext("NSE", "99926000")
				if ctx.Available {
					return ctx.MarketState
				}
				return strategy.EntryMarketStateTrending
			})
		}
		if svc.nifty50SLStrategy != nil {
			svc.nifty50SLStrategy.SetMarketStateProvider(func() strategy.EntryMarketState {
				ctx := fnoStrat.EntryAutomationContext("NSE", "99926000")
				if ctx.Available {
					return ctx.MarketState
				}
				return strategy.EntryMarketStateTrending
			})
		}
	}

	svc.syncStrategyFNOTokens(cfg.CallFNOToken, cfg.PutFNOToken)

	svc.tfEngine = strategy.NewTFEngine(1000)

	// All strategies detached from the engine. Instances are still constructed
	// and wired above (market-state providers, FNO token sync, snapshots), but
	// none are registered, so the engine emits no signals.
	// Re-enable by uncommenting the desired line(s).
	// svc.tfEngine.Register(svc.nifty50Strategy)
	// svc.tfEngine.Register(svc.nifty50SLStrategy)
	// svc.tfEngine.Register(svc.nifty50SL2Strategy)
	// svc.tfEngine.Register(svc.nifty5010PtsStrategy)
	// svc.tfEngine.Register(svc.nifty50RangeStrategy)
	// svc.tfEngine.Register(svc.nifty50FnO5M3MStrategy)
	log.Println("[stratengine] ⚪ No strategies registered — all detached from engine")

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
		LiveOrders:       cfg.LiveOrders,
		Qty:              cfg.Qty,
		CallFNOToken:     cfg.CallFNOToken,
		CallFNOSymbol:    cfg.CallFNOSymbol,
		PutFNOToken:      cfg.PutFNOToken,
		PutFNOSymbol:     cfg.PutFNOSymbol,
		FNOExchange:      cfg.FNOExchange,
		FNOOrderType:     cfg.FNOOrderType,
		FNOProductType:   cfg.FNOProductType,
		FNOStopLossPaise: nifty50Cfg.FNOStopLossPaise, // mirror system SL in GTT
		AngelAPIKey:      cfg.AngelAPIKey,
		AngelClientID:    cfg.AngelClientID,
		AngelPassword:    cfg.AngelPassword,
		AngelTOTP:        cfg.AngelTOTP,
		LogPrefix:        "[order_executor]",
		SessionManager:   sessionManager, // Pass session manager for auto-refresh
	})

	// Attach P&L tracker for profit cap monitoring
	svc.orderExecutor.SetPnLTracker(svc.pnlTracker)

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

// Run starts all subsystems and blocks until ctx is cancelled.
func (svc *Service) Run(ctx context.Context) error {
	log.Println("[stratengine] starting Strategy Engine...")

	svc.restoreAndWire(ctx)
	stopMetrics := svc.startMetrics()
	defer stopMetrics()

	// ── Dashboard kill switch + broker position reconciliation ──
	go svc.configUpdateLoop(ctx)
	go svc.positionReconcileLoop(ctx)

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

	// ── Start snapshot loop ──
	go svc.snapshotLoop(ctx)

	// ── Stale position cleanup: exit any position from a previous day ──
	// Happens once at startup. Positions restore from snapshot but if the snapshot
	// is from yesterday they need to be force-exited so we start the day clean.
	svc.clearStalePositions()
	svc.publishLiveOrders(ctx)

	// ── EOD auto-exit: close all positions at 15:30:00 IST ──
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
			svc.updateLiveOrdersFromTick(ctx, tick)

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
					picker := orderexec.NewStrikePicker(sc)
					if !picker.ResolveATM(spotPrice) {
						atomic.StoreInt64(&svc.strikeRetryAfterNS, time.Now().Add(60*time.Second).UnixNano())
						log.Println("[stratengine] ⚠️  strike resolution failed (will retry in 60s)")
						return
					}

					svc.strikePicker = picker
					svc.orderExecutor.SetStrikePicker(picker)

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
					subCmd, _ := json.Marshal(map[string]interface{}{
						"exchange_type": 2, // NFO
						"tokens":        []string{ce.Token, pe.Token},
					})
					if pubErr := svc.redisWriter.Client().Publish(ctx, "cmd:subscribe_token", string(subCmd)).Err(); pubErr != nil {
						log.Printf("[stratengine] ⚠️  failed to publish subscribe command: %v", pubErr)
					} else {
						log.Printf("[stratengine] 📡 published FNO subscribe command: CE=%s PE=%s", ce.Token, pe.Token)
					}

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
					svc.redisWriter.Client().Set(ctx, "strike:info", strikePayload, 24*time.Hour)
					svc.redisWriter.Client().Publish(ctx, "pub:strike", string(strikePayload))
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

// signalLoop reads signals from the TFEngine and writes to journal + notifier + PubSub.
func (svc *Service) signalLoop(ctx context.Context) {
	// Track FNO entry prices: key = "strategy|side" → FNO LTP at entry (paise)
	entryFNOPrices := make(map[string]int64)
	// Track the exact option token used at entry so exit P&L stays pinned to the
	// same contract even after dynamic strikes roll/reset.
	entryFNOTokens := make(map[string]string)
	// Track market state captured at entry so exits keep the same regime label.
	entryMarketStates := make(map[string]string)
	// Track the original strategy instrument so exit bookkeeping closes the same
	// logical position even when the stop-loss was triggered by an option tick.
	entryInstruments := make(map[string]trackedInstrument)

	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-svc.tfEngine.Signals():
			if !ok {
				return
			}

			expandedSignals := expandReverseSignals(sig)

			for _, expandedSig := range expandedSignals {
				sig := expandedSig
				now := time.Now()

				svc.applySyntheticStrategyEntry(sig)

				// Kill switch: block new entries but allow exits
				if svc.killSwitchActive() && sig.Action == strategy.ActionBuy {
					log.Printf("[stratengine] KILL SWITCH: blocked entry signal %s %s:%s",
						sig.StrategyName, sig.Exchange, sig.Token)
					continue
				}

				// Never open fresh positions once the live market session is closed.
				// This prevents delayed 15:29/15:30 signals from re-entering after the
				// EOD auto-exit loop has already flattened the book.
				if sig.Action == strategy.ActionBuy && !markethours.IsMarketOpen(now) {
					log.Printf("[stratengine] MARKET CLOSED: blocked entry signal %s %s:%s at %s",
						sig.StrategyName, sig.Exchange, sig.Token, now.In(markethours.IST).Format("15:04:05"))
					continue
				}

				// Nor after the EOD auto-exit cutoff: the book is being (or has
				// been) flattened for the day.
				if sig.Action == strategy.ActionBuy && svc.pastEODCutoff(now) {
					log.Printf("[stratengine] EOD CUTOFF: blocked entry signal %s %s:%s at %s",
						sig.StrategyName, sig.Exchange, sig.Token, now.In(markethours.IST).Format("15:04:05"))
					continue
				}

				if sig.Action == strategy.ActionBuy {
					svc.applyOptionAutomation(ctx, &sig, now)
				}

				// ── Resolve FNO token for this signal ──
				var fnoToken string
				switch sig.Side {
				case strategy.SideCall:
					if svc.strikePicker != nil && svc.strikePicker.Resolved() {
						fnoToken = svc.strikePicker.GetCallToken().Token
					} else {
						fnoToken = svc.cfg.CallFNOToken
					}
				case strategy.SidePut:
					if svc.strikePicker != nil && svc.strikePicker.Resolved() {
						fnoToken = svc.strikePicker.GetPutToken().Token
					} else {
						fnoToken = svc.cfg.PutFNOToken
					}
				}

				// No strike picked yet and no static token: nothing to buy.
				// Exits still go through so an open position can always close.
				if sig.Action == strategy.ActionBuy && fnoToken == "" {
					log.Printf("[stratengine] NO STRIKE: blocked entry signal %s:%s — strike not resolved yet",
						sig.StrategyName, sig.Side)
					continue
				}

				// ── Get current FNO LTP ──
				var currentFNOPrice int64
				if fnoToken != "" {
					currentFNOPrice = svc.orderExecutor.GetLTP(fnoToken)
					if currentFNOPrice > 0 {
						sig.Price = currentFNOPrice
					}
				}

				// ── Track entry/exit FNO prices ──
				posKey := sig.StrategyName + "|" + string(sig.Side)
				marketState := normalizeMarketStateValue(sig.MarketState)
				if marketState == "" {
					if actx, ok := svc.strategyAutomationContext(sig); ok {
						marketState = normalizeMarketStateValue(string(actx.MarketState))
					}
				}
				if marketState == "" && sig.Action == strategy.ActionExit {
					if ms, ok := entryMarketStates[posKey]; ok {
						marketState = normalizeMarketStateValue(ms)
					}
				}
				if marketState == "" {
					marketState = inferMarketStateFromReason(sig.Reason)
				}
				sig.MarketState = marketState

				var stoplossPrice int64
				if sig.Action == strategy.ActionBuy {
					// Store entry FNO price
					entryFNOPrices[posKey] = currentFNOPrice
					entryFNOTokens[posKey] = fnoToken
					entryMarketStates[posKey] = marketState
					entryInstruments[posKey] = trackedInstrument{token: sig.Token, exchange: sig.Exchange}
					svc.setStrategyFNOEntryPrice(sig.StrategyName, currentFNOPrice)
					svc.upsertLiveOrder(sig.StrategyName, sig.Side, fnoToken, currentFNOPrice)
					if live, ok := svc.liveOrderPayload(sig.StrategyName, sig.Side); ok {
						stoplossPrice = live.StoplossPrice
					}
					// Compute FNO option SL price from strategy-specific FNO config
					if stoplossPrice == 0 {
						if hardSL, _, _, ok := svc.stopConfig(sig.StrategyName); ok && hardSL > 0 && currentFNOPrice > 0 {
							stoplossPrice = int64(float64(currentFNOPrice) * (1 - hardSL/100))
						}
					}
				}

				// ── Determine order mode BEFORE recording trade ──
				// This ensures EXIT orders that cause profit cap are still LIVE
				orderMode := "PAPER"
				profitCapHit := false
				liveMode := false
				if sig.StrategyName == "NIFTY50_FNO" && svc.cfg.LiveOrders {
					// Check profit cap BEFORE this trade
					dailyPnL := svc.pnlTracker.GetStrategyDailyPnL("NIFTY50_FNO")
					// 10 points profit cap for 65 qty = 65 × 10 × 100 paise = 65,000 paise
					const profitCapPaise int64 = 65000 // 65 qty × 10 points = ₹650.00

					// Profit cap only applies to new BUY signals to block new entries
					// EXIT signals must remain REAL to properly reflect they are closing real positions
					if dailyPnL >= profitCapPaise && sig.Action == strategy.ActionBuy {
						orderMode = "PAPER_CAP"
						profitCapHit = true
					} else {
						orderMode = "REAL"
						liveMode = true
					}
				}

				if sig.Action == strategy.ActionExit {
					svc.normalizeExitInstrument(&sig, entryInstruments, posKey)
					svc.removeLiveOrder(sig.StrategyName, sig.Side)
					if entryToken, ok := entryFNOTokens[posKey]; ok && entryToken != "" {
						if exitLTP := svc.orderExecutor.GetLTP(entryToken); exitLTP > 0 {
							currentFNOPrice = exitLTP
							sig.Price = exitLTP
						}
						delete(entryFNOTokens, posKey)
					}
					delete(entryInstruments, posKey)
					delete(entryMarketStates, posKey)
					// Attach entry FNO price to exit signal
					if entryP, ok := entryFNOPrices[posKey]; ok {
						sig.EntryFNOPrice = entryP
						delete(entryFNOPrices, posKey)
					}
				}

				// ── Execute FNO order ──
				// Dispatched before the journal, publish and notify below, so
				// none of that side work can delay an exit. Orders go to a
				// per-strategy FIFO worker: a reverse EXIT->BUY pair stays in
				// order without blocking this loop. P&L and portfolio are
				// booked from the executor's fills (see onFill), not here.
				// Skip executor entirely when profit cap is hit — the executor
				// independently decides REAL based on strategy name, so we must
				// gate it here to prevent real orders after the daily cap.
				if profitCapHit && sig.Action == strategy.ActionBuy {
					// Only block NEW entries — EXIT/SELL must always go through
					// so open REAL positions can be closed properly.
					log.Printf("[stratengine] 🛑 PROFIT CAP: skipping new BUY — dailyPnL >= ₹650 (mode=%s) %s",
						orderMode, sig.StrategyName)
				} else {
					svc.dispatchOrder(ctx, sig)
				}

				// Journal the signal with live mode and profit cap flags
				if err := svc.journal.Record(sig, now, nil, liveMode, profitCapHit); err != nil {
					log.Printf("[stratengine] journal write error: %v", err)
				}

				// Publish to Redis PubSub for frontend WS delivery
				sigPayload, err := json.Marshal(map[string]interface{}{
					"strategy_name":     sig.StrategyName,
					"action":            sig.Action,
					"side":              sig.Side,
					"reverse_to":        sig.ReverseTo,
					"market_state":      sig.MarketState,
					"token":             sig.Token,
					"exchange":          sig.Exchange,
					"qty":               sig.Qty,
					"price":             sig.Price,
					"entry_fno_price":   sig.EntryFNOPrice,
					"current_fno_price": currentFNOPrice,
					"stoploss_price":    stoplossPrice,
					"reason":            sig.Reason,
					"order_mode":        orderMode,
					"profit_cap_hit":    profitCapHit,
					"ts":                now.UTC().Format(time.RFC3339Nano),
				})
				pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
				if err == nil {
					if pubErr := svc.redisWriter.Client().Publish(pctx, "pub:signal", string(sigPayload)).Err(); pubErr != nil {
						log.Printf("[stratengine] signal publish error: %v", pubErr)
					}
				}

				// ── Publish P&L summary to Redis ──
				svc.publishPnLSummary(pctx)
				svc.publishLiveOrders(pctx)
				pcancel()

				// Notify
				alert := notification.Alert{
					Level:   notification.AlertInfo,
					Title:   fmt.Sprintf("[%s] %s %s:%s", sig.StrategyName, sig.Action, sig.Exchange, sig.Token),
					Message: sig.Reason,
				}
				if sig.Action == strategy.ActionBuy {
					alert.Level = notification.AlertWarning
					alert.Message = fmt.Sprintf("%s | FNO entry price: ₹%.2f", sig.Reason, float64(currentFNOPrice)/100)
				} else if sig.Action == strategy.ActionExit {
					pnl := float64(currentFNOPrice-sig.EntryFNOPrice) / 100
					alert.Message = fmt.Sprintf("%s | Entry: ₹%.2f → Exit: ₹%.2f | P&L: ₹%.2f",
						sig.Reason, float64(sig.EntryFNOPrice)/100, float64(currentFNOPrice)/100, pnl)
				}
				svc.notifyAsync(alert)

				log.Printf("[stratengine] SIGNAL: %s %s %s:%s fno_ltp=%d entry_fno=%d — %s",
					sig.Action, sig.StrategyName, sig.Exchange, sig.Token, currentFNOPrice, sig.EntryFNOPrice, sig.Reason)
			}
		}
	}
}

func expandReverseSignals(sig strategy.Signal) []strategy.Signal {
	if sig.Action != strategy.ActionExit {
		return []strategy.Signal{sig}
	}
	if sig.ReverseTo != strategy.SideCall && sig.ReverseTo != strategy.SidePut {
		return []strategy.Signal{sig}
	}

	closePrice := parseSignalClose(sig.Reason)
	if closePrice <= 0 && sig.Price > 0 {
		closePrice = sig.Price
	}
	buyReason := fmt.Sprintf("ENTRY %s reason=REVERSE_FROM_%s close=%d", sig.ReverseTo, sig.Side, closePrice)

	buySig := sig
	buySig.Action = strategy.ActionBuy
	buySig.Side = sig.ReverseTo
	buySig.ReverseTo = strategy.SideNone
	buySig.EntryFNOPrice = 0
	buySig.Price = 0
	buySig.Reason = buyReason

	return []strategy.Signal{sig, buySig}
}

func (svc *Service) applySyntheticStrategyEntry(sig strategy.Signal) {
	if sig.Action != strategy.ActionBuy {
		return
	}
	if !strings.Contains(sig.Reason, "REVERSE_FROM_") {
		return
	}
	if svc.nifty50Strategy == nil || sig.StrategyName != svc.nifty50Strategy.Name() {
		return
	}

	indexPrice := parseSignalClose(sig.Reason)
	if indexPrice <= 0 {
		return
	}

	svc.nifty50Strategy.ApplySyntheticEntry(sig.Exchange, sig.Token, sig.Side, indexPrice)
}

func (svc *Service) applyOptionAutomation(ctx context.Context, sig *strategy.Signal, now time.Time) {
	if !svc.cfg.OptionAutomation || !svc.cfg.DynamicStrikes || sig == nil || sig.Action != strategy.ActionBuy {
		return
	}
	if sig.Side != strategy.SideCall && sig.Side != strategy.SidePut {
		return
	}
	if svc.strikePicker == nil {
		log.Printf("[stratengine] option automation skipped: strike picker not ready for %s", sig.Side)
		return
	}

	spotPrice := svc.orderExecutor.GetLTP(sig.Token)
	if spotPrice <= 0 {
		spotPrice = parseSignalClose(sig.Reason)
	}
	if spotPrice <= 0 {
		log.Printf("[stratengine] option automation skipped: no spot price for %s", sig.StrategyName)
		return
	}

	input := svc.buildAutomationInput(*sig, now, spotPrice)
	pick, err := svc.strikePicker.ResolveForEntry(input)
	if err != nil {
		log.Printf("[stratengine] option automation fallback: %v", err)
		return
	}

	var previousToken string
	switch sig.Side {
	case strategy.SideCall:
		previousToken = svc.strikePicker.GetCallToken().Token
	case strategy.SidePut:
		previousToken = svc.strikePicker.GetPutToken().Token
	}

	svc.strikePicker.ApplyPick(pick)
	svc.orderExecutor.SetStrikePicker(svc.strikePicker)
	svc.syncStrategyFNOTokens(svc.strikePicker.GetCallToken().Token, svc.strikePicker.GetPutToken().Token)

	log.Printf("[stratengine] 🤖 option automation selected %s expiry=%s strike=%d symbol=%s token=%s fallback=%v reason=%s",
		sig.Side,
		pick.SelectedExpiry.In(time.FixedZone("IST", 5*3600+30*60)).Format("02Jan2006"),
		pick.Strike,
		pick.Symbol,
		pick.Token,
		pick.UsedFallback,
		pick.Reason,
	)

	if pick.Token != "" && pick.Token != previousToken {
		svc.publishFNOSubscription(ctx, pick.Token)
	}
	svc.publishStrikeInfo(ctx, spotPrice)
}

func (svc *Service) buildAutomationInput(sig strategy.Signal, now time.Time, spotPrice int64) orderexec.AutomationInput {
	marketState := orderexec.MarketStateSideways
	trendStrength := orderexec.StrengthLow
	momentumStrength := orderexec.StrengthLow

	if ctx, ok := svc.strategyAutomationContext(sig); ok {
		marketState = automationMarketStateFromStrategy(ctx.MarketState)
		trendStrength = automationStrengthFromStrategy(ctx.TrendStrength)
		momentumStrength = automationStrengthFromStrategy(ctx.MomentumStrength)
	} else {
		pct := parseSignalDistancePct(sig.Reason)
		if strings.Contains(strings.ToLower(sig.Reason), "momentum") && pct >= 0.12 {
			marketState = orderexec.MarketStateTrending
		}
		strength := automationStrengthFromPct(pct)
		trendStrength = strength
		momentumStrength = strength
	}

	return orderexec.AutomationInput{
		SignalType:       automationSignalType(sig.Side),
		IsExpiryDay:      isNIFTYExpiryDay(now),
		EntryTime:        now,
		MarketState:      marketState,
		TrendStrength:    trendStrength,
		MomentumStrength: momentumStrength,
		HoldType:         automationHoldType(svc.cfg.OptionHoldType),
		ExpectedIVMove:   automationIVMove(svc.cfg.ExpectedIVMove),
		SpotPrice:        spotPrice,
	}
}

func (svc *Service) qualifyFNOToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.Contains(token, ":") {
		return token
	}
	exchange := strings.TrimSpace(svc.cfg.FNOExchange)
	if exchange == "" {
		exchange = "NFO"
	}
	return exchange + ":" + token
}

func (svc *Service) syncStrategyFNOTokens(callToken, putToken string) {
	callToken = svc.qualifyFNOToken(callToken)
	putToken = svc.qualifyFNOToken(putToken)

	if svc.nifty50SLStrategy != nil {
		svc.nifty50SLStrategy.SetFNOTokens(callToken, putToken)
	}
	if svc.nifty50Strategy != nil {
		svc.nifty50Strategy.SetFNOTokens(callToken, putToken)
	}
	if svc.nifty5010PtsStrategy != nil {
		svc.nifty5010PtsStrategy.SetFNOTokens(callToken, putToken)
	}
	if svc.nifty50SL2Strategy != nil {
		svc.nifty50SL2Strategy.SetFNOTokens(callToken, putToken)
	}
	if svc.nifty50RangeStrategy != nil {
		svc.nifty50RangeStrategy.SetFNOTokens(callToken, putToken)
	}
	if svc.nifty50FnO5M3MStrategy != nil {
		svc.nifty50FnO5M3MStrategy.SetFNOTokens(callToken, putToken)
	}
}

func (svc *Service) setStrategyFNOEntryPrice(strategyName string, price int64) {
	if price <= 0 {
		return
	}
	if svc.nifty50SLStrategy != nil && strategyName == svc.nifty50SLStrategy.Name() {
		svc.nifty50SLStrategy.SetFNOEntryPrice(price)
		return
	}
	if svc.nifty50Strategy != nil && strategyName == svc.nifty50Strategy.Name() {
		svc.nifty50Strategy.SetFNOEntryPrice(price)
		return
	}
	if svc.nifty5010PtsStrategy != nil && strategyName == svc.nifty5010PtsStrategy.Name() {
		svc.nifty5010PtsStrategy.SetFNOEntryPrice(price)
		return
	}
	if svc.nifty50SL2Strategy != nil && strategyName == svc.nifty50SL2Strategy.Name() {
		svc.nifty50SL2Strategy.SetFNOEntryPrice(price)
		return
	}
	if svc.nifty50FnO5M3MStrategy != nil && strategyName == svc.nifty50FnO5M3MStrategy.Name() {
		svc.nifty50FnO5M3MStrategy.SetFNOEntryPrice(price)
	}
}

func (svc *Service) normalizeExitInstrument(sig *strategy.Signal, entries map[string]trackedInstrument, posKey string) {
	if sig == nil {
		return
	}
	entry, ok := entries[posKey]
	if !ok || entry.token == "" || entry.exchange == "" {
		return
	}
	sig.Token = entry.token
	sig.Exchange = entry.exchange
}

func liveOrderKey(strategyName string, side strategy.PositionSide) string {
	return strategyName + "|" + string(side)
}

func (svc *Service) unqualifyToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if idx := strings.IndexByte(token, ':'); idx >= 0 && idx < len(token)-1 {
		return token[idx+1:]
	}
	return token
}

func (svc *Service) seedLiveOrdersFromStrategies() {
	if svc.nifty50SLStrategy != nil {
		if pos := svc.nifty50SLStrategy.CurrentFNOPosition(); pos != nil {
			svc.setLiveOrderFromPosition(svc.nifty50SLStrategy.Name(), pos)
		}
	}
	if svc.nifty50SL2Strategy != nil {
		if pos := svc.nifty50SL2Strategy.CurrentFNOPosition(); pos != nil {
			svc.setLiveOrderFromPosition(svc.nifty50SL2Strategy.Name(), pos)
		}
	}
	if svc.nifty50Strategy != nil {
		if pos := svc.nifty50Strategy.CurrentFNOPosition(); pos != nil {
			svc.setLiveOrderFromPosition(svc.nifty50Strategy.Name(), pos)
		}
	}
	if svc.nifty5010PtsStrategy != nil {
		if pos := svc.nifty5010PtsStrategy.CurrentFNOPosition(); pos != nil {
			svc.setLiveOrderFromPosition(svc.nifty5010PtsStrategy.Name(), pos)
		}
	}
	if svc.nifty50FnO5M3MStrategy != nil {
		if pos := svc.nifty50FnO5M3MStrategy.CurrentFNOPosition(); pos != nil {
			svc.setLiveOrderFromPosition(svc.nifty50FnO5M3MStrategy.Name(), pos)
		}
	}
}

func (svc *Service) setLiveOrderFromPosition(strategyName string, pos *strategy.LiveFNOPosition) {
	if pos == nil || pos.Side == strategy.SideNone || pos.EntryPrice <= 0 {
		return
	}
	token := svc.unqualifyToken(pos.Token)
	svc.liveOrdersMu.Lock()
	svc.liveOrders[liveOrderKey(strategyName, pos.Side)] = &liveOrderRuntime{
		StrategyName: strategyName,
		Side:         pos.Side,
		Token:        token,
		EntryPrice:   pos.EntryPrice,
		BestPrice:    pos.BestPrice,
		CurrentPrice: svc.orderExecutor.GetLTP(token),
	}
	svc.liveOrdersMu.Unlock()
}

func (svc *Service) upsertLiveOrder(strategyName string, side strategy.PositionSide, token string, entryPrice int64) {
	if strategyName == "" || side == strategy.SideNone {
		return
	}
	token = svc.unqualifyToken(token)
	svc.liveOrdersMu.Lock()
	rt, ok := svc.liveOrders[liveOrderKey(strategyName, side)]
	if !ok {
		rt = &liveOrderRuntime{
			StrategyName: strategyName,
			Side:         side,
		}
		svc.liveOrders[liveOrderKey(strategyName, side)] = rt
	}
	rt.Token = token
	if entryPrice > 0 {
		rt.EntryPrice = entryPrice
		rt.BestPrice = entryPrice
		rt.CurrentPrice = entryPrice
	} else if token != "" {
		rt.CurrentPrice = svc.orderExecutor.GetLTP(token)
	}
	svc.liveOrdersMu.Unlock()
}

func (svc *Service) removeLiveOrder(strategyName string, side strategy.PositionSide) {
	if strategyName == "" || side == strategy.SideNone {
		return
	}
	svc.liveOrdersMu.Lock()
	delete(svc.liveOrders, liveOrderKey(strategyName, side))
	svc.liveOrdersMu.Unlock()
}

func (svc *Service) updateLiveOrdersFromTick(ctx context.Context, tick model.Tick) {
	if tick.Token == "" || tick.Price <= 0 {
		return
	}

	changed := false

	svc.liveOrdersMu.Lock()
	for _, rt := range svc.liveOrders {
		if rt == nil || rt.Token != tick.Token {
			continue
		}
		if rt.CurrentPrice != tick.Price {
			rt.CurrentPrice = tick.Price
			changed = true
		}
		switch rt.Side {
		case strategy.SideCall, strategy.SidePut:
			// Both CALL and PUT are BUY orders — track highest as best
			if tick.Price > rt.BestPrice {
				rt.BestPrice = tick.Price
				changed = true
			}
		}
	}
	svc.liveOrdersMu.Unlock()

	if changed {
		svc.publishLiveOrders(ctx)
	}
}

func (svc *Service) stopConfig(strategyName string) (hardSL, trailSL, trailStart float64, ok bool) {
	switch {
	case svc.nifty50SLStrategy != nil && strategyName == svc.nifty50SLStrategy.Name():
		cfg := svc.nifty50SLStrategy.Config()
		return cfg.FNOHardSLPct, cfg.FNOTrailSLPct, cfg.FNOTrailStartPct, true
	case svc.nifty50SL2Strategy != nil && strategyName == svc.nifty50SL2Strategy.Name():
		cfg := svc.nifty50SL2Strategy.Config()
		return cfg.FNOHardSLPct, cfg.FNOTrailSLPct, cfg.FNOTrailStartPct, true
	case svc.nifty50Strategy != nil && strategyName == svc.nifty50Strategy.Name():
		cfg := svc.nifty50Strategy.Config()
		return cfg.FNOHardSLPct, cfg.FNOTrailSLPct, cfg.FNOTrailStartPct, true
	case svc.nifty5010PtsStrategy != nil && strategyName == svc.nifty5010PtsStrategy.Name():
		cfg := svc.nifty5010PtsStrategy.Config()
		return cfg.FNOHardSLPct, cfg.FNOTrailSLPct, cfg.FNOTrailStartPct, true
	default:
		return 0, 0, 0, false
	}
}

// computeLiveStoploss computes the SL price for a live FNO order.
// Both CALL and PUT are BUY orders: premium UP = profit, premium DOWN = loss.
func computeLiveStoploss(side strategy.PositionSide, entryPrice, bestPrice int64, hardSL, trailSL, trailStart float64) (int64, string) {
	if entryPrice <= 0 {
		return 0, ""
	}

	// Hard SL: exit if premium drops hardSL% below entry (same for CALL and PUT)
	var hardStop int64
	if hardSL > 0 {
		hardStop = int64(float64(entryPrice) * (1 - hardSL/100))
	}

	// Trail SL: exit if premium drops trailSL% below best (same for CALL and PUT)
	var trailStop int64
	trailActive := false
	if trailSL > 0 && bestPrice > 0 {
		entryF := float64(entryPrice)
		bestF := float64(bestPrice)
		profit := (bestF - entryF) / entryF * 100
		if profit >= trailStart {
			trailStop = int64(bestF * (1 - trailSL/100))
			trailActive = trailStop > 0
		}
	}

	// Return the tighter (higher) SL price
	if trailActive && (hardStop == 0 || trailStop > hardStop) {
		return trailStop, "TRAIL"
	}
	if hardStop > 0 {
		return hardStop, "HARD"
	}
	if trailActive {
		return trailStop, "TRAIL"
	}
	return 0, ""
}

func (svc *Service) snapshotLiveOrdersLocked() []liveOrderPayload {
	orders := make([]liveOrderPayload, 0, len(svc.liveOrders))
	for _, rt := range svc.liveOrders {
		if rt == nil || rt.Side == strategy.SideNone || rt.EntryPrice <= 0 {
			continue
		}
		payload := liveOrderPayload{
			StrategyName:    rt.StrategyName,
			Side:            rt.Side,
			FNOToken:        rt.Token,
			EntryFNOPrice:   rt.EntryPrice,
			CurrentFNOPrice: rt.CurrentPrice,
			BestFNOPrice:    rt.BestPrice,
		}
		if hardSL, trailSL, trailStart, ok := svc.stopConfig(rt.StrategyName); ok {
			payload.StoplossPrice, payload.StoplossKind = computeLiveStoploss(rt.Side, rt.EntryPrice, rt.BestPrice, hardSL, trailSL, trailStart)
		}
		orders = append(orders, payload)
	}
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].StrategyName == orders[j].StrategyName {
			return orders[i].Side < orders[j].Side
		}
		return orders[i].StrategyName < orders[j].StrategyName
	})
	return orders
}

func (svc *Service) liveOrderPayload(strategyName string, side strategy.PositionSide) (liveOrderPayload, bool) {
	svc.liveOrdersMu.Lock()
	defer svc.liveOrdersMu.Unlock()

	rt, ok := svc.liveOrders[liveOrderKey(strategyName, side)]
	if !ok || rt == nil || rt.EntryPrice <= 0 {
		return liveOrderPayload{}, false
	}

	payload := liveOrderPayload{
		StrategyName:    rt.StrategyName,
		Side:            rt.Side,
		FNOToken:        rt.Token,
		EntryFNOPrice:   rt.EntryPrice,
		CurrentFNOPrice: rt.CurrentPrice,
		BestFNOPrice:    rt.BestPrice,
	}
	if hardSL, trailSL, trailStart, ok := svc.stopConfig(rt.StrategyName); ok {
		payload.StoplossPrice, payload.StoplossKind = computeLiveStoploss(rt.Side, rt.EntryPrice, rt.BestPrice, hardSL, trailSL, trailStart)
	}
	return payload, true
}

func (svc *Service) publishLiveOrders(ctx context.Context) {
	if svc.redisWriter == nil {
		return
	}

	svc.liveOrdersMu.Lock()
	orders := svc.snapshotLiveOrdersLocked()
	payload, err := json.Marshal(map[string]interface{}{
		"orders": orders,
	})
	if err != nil {
		svc.liveOrdersMu.Unlock()
		log.Printf("[stratengine] live orders marshal error: %v", err)
		return
	}
	payloadStr := string(payload)
	if payloadStr == svc.lastLiveOrdersJSON {
		svc.liveOrdersMu.Unlock()
		return
	}
	svc.lastLiveOrdersJSON = payloadStr
	svc.liveOrdersMu.Unlock()

	svc.redisWriter.Client().Set(ctx, "orders:live", payload, 24*time.Hour)
	svc.redisWriter.Client().Publish(ctx, "pub:orders", payloadStr)
}

func (svc *Service) publishFNOSubscription(ctx context.Context, tokens ...string) {
	uniq := make([]string, 0, len(tokens))
	seen := make(map[string]struct{})
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		uniq = append(uniq, token)
	}
	if len(uniq) == 0 {
		return
	}

	subCmd, _ := json.Marshal(map[string]interface{}{
		"exchange_type": 2,
		"tokens":        uniq,
	})
	if err := svc.redisWriter.Client().Publish(ctx, "cmd:subscribe_token", string(subCmd)).Err(); err != nil {
		log.Printf("[stratengine] ⚠️  failed to publish FNO subscribe command: %v", err)
		return
	}
	log.Printf("[stratengine] 📡 published FNO subscribe command: tokens=%v", uniq)
}

func (svc *Service) publishStrikeInfo(ctx context.Context, spotPrice int64) {
	if svc.strikePicker == nil || !svc.strikePicker.Resolved() {
		return
	}

	ce := svc.strikePicker.GetCallToken()
	pe := svc.strikePicker.GetPutToken()
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
	svc.redisWriter.Client().Set(ctx, "strike:info", strikePayload, 24*time.Hour)
	svc.redisWriter.Client().Publish(ctx, "pub:strike", string(strikePayload))
}

func automationSignalType(side strategy.PositionSide) orderexec.AutomationSignalType {
	switch side {
	case strategy.SideCall:
		return orderexec.SignalBuyCall
	case strategy.SidePut:
		return orderexec.SignalBuyPut
	default:
		return ""
	}
}

func (svc *Service) strategyAutomationContext(sig strategy.Signal) (strategy.EntryAutomationContext, bool) {
	if svc.nifty50Strategy != nil && sig.StrategyName == svc.nifty50Strategy.Name() {
		ctx := svc.nifty50Strategy.EntryAutomationContext(sig.Exchange, sig.Token)
		if ctx.Available {
			return ctx, true
		}
	}

	// FNO_SL / FNO_SL2 / 10PTS strategies trade the same NIFTY50 index but
	// don't compute their own market state.  Fall back to the primary
	// NIFTY50_FNO strategy's regime detection so signals always carry a
	// market_state label in the journal.
	if svc.nifty50Strategy != nil && sig.StrategyName != svc.nifty50Strategy.Name() {
		ctx := svc.nifty50Strategy.EntryAutomationContext(sig.Exchange, sig.Token)
		if ctx.Available {
			return ctx, true
		}
	}

	return strategy.EntryAutomationContext{}, false
}

func automationMarketStateFromStrategy(state strategy.EntryMarketState) orderexec.AutomationMarketState {
	switch state {
	case strategy.EntryMarketStateTrending:
		return orderexec.MarketStateTrending
	case strategy.EntryMarketStateChoppy:
		return orderexec.MarketStateChoppy
	default:
		return orderexec.MarketStateSideways
	}
}

func automationStrengthFromStrategy(strength strategy.EntryStrength) orderexec.AutomationStrength {
	switch strength {
	case strategy.EntryStrengthHigh:
		return orderexec.StrengthHigh
	case strategy.EntryStrengthMedium:
		return orderexec.StrengthMedium
	default:
		return orderexec.StrengthLow
	}
}

func automationStrengthFromPct(pct float64) orderexec.AutomationStrength {
	switch {
	case pct >= 0.20:
		return orderexec.StrengthHigh
	case pct >= 0.12:
		return orderexec.StrengthMedium
	default:
		return orderexec.StrengthLow
	}
}

func automationHoldType(v string) orderexec.AutomationHoldType {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "carry":
		return orderexec.HoldTypeCarry
	default:
		return orderexec.HoldTypeIntraday
	}
}

func normalizeMarketStateValue(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "trending":
		return "trending"
	case "choppy":
		return "choppy"
	case "range":
		return "range"
	case "sideways":
		return "sideways"
	default:
		return ""
	}
}

func inferMarketStateFromReason(reason string) string {
	hay := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(hay, "range "):
		return "range"
	case strings.Contains(hay, "choppy"):
		return "choppy"
	case strings.Contains(hay, "sideways"):
		return "sideways"
	case strings.Contains(hay, "momentum"):
		return "trending"
	default:
		return ""
	}
}

func automationIVMove(v string) orderexec.AutomationIVMove {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "rise":
		return orderexec.IVMoveRise
	case "fall":
		return orderexec.IVMoveFall
	default:
		return orderexec.IVMoveNeutral
	}
}

func parseSignalDistancePct(reason string) float64 {
	marker := " is "
	start := strings.Index(reason, marker)
	if start < 0 {
		return 0
	}
	rest := reason[start+len(marker):]
	end := strings.Index(rest, "%")
	if end <= 0 {
		return 0
	}
	pct, err := strconv.ParseFloat(strings.TrimSpace(rest[:end]), 64)
	if err != nil {
		return 0
	}
	return pct
}

func parseSignalClose(reason string) int64 {
	marker := "close="
	start := strings.Index(reason, marker)
	if start < 0 {
		return 0
	}
	rest := reason[start+len(marker):]
	end := len(rest)
	for i, r := range rest {
		if r < '0' || r > '9' {
			end = i
			break
		}
	}
	if end <= 0 {
		return 0
	}
	price, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return 0
	}
	return price
}

func isNIFTYExpiryDay(now time.Time) bool {
	now = now.In(time.FixedZone("IST", 5*3600+30*60))
	if now.Weekday() != time.Tuesday {
		return false
	}
	return now.Hour() < 15 || (now.Hour() == 15 && now.Minute() < 30)
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
