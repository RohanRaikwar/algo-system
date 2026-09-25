// Package orderexec provides a shared FNO order executor used by both
// stratengine and stratengine_ind services.
//
// It tracks live LTP for CALL/PUT FNO tokens from tick data,
// and places buy/sell orders when strategy signals fire.
//
// Mutual exclusion: only one of CALL or PUT can be active at a time.
// Buying CALL blocks PUT entries. Selling CALL unblocks PUT entries.
//
// Safety features:
//   - Circuit breaker: trips after 3 consecutive broker failures, reopens after 30s
//   - Rate limiter: max 10 orders per 60s window
//   - Idempotency keys: logged for audit trail
package orderexec

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/circuitbreaker"
	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

// Config holds FNO order execution configuration.
// Extracted from the per-service Config to avoid duplication.
type Config struct {
	LiveOrders     bool   // true = place real orders, false = dry-run (log only)
	PaperTrade     bool   // true = use Angel One paper trading, false = real orders (only if LiveOrders=true)
	Qty            int64  // default quantity per trade
	CallFNOToken   string // Angel One symbol token for CALL option
	CallFNOSymbol  string // trading symbol for CALL option
	PutFNOToken    string // Angel One symbol token for PUT option
	PutFNOSymbol   string // trading symbol for PUT option
	FNOExchange    string // exchange (e.g. "NFO")
	FNOOrderType   string // "MARKET" or "LIMIT"
	FNOProductType string // "CARRYFORWARD" or "INTRADAY"

	// FNOStopLossPaise: fixed stop loss distance below FNO entry premium,
	// in paise. 1000 = ₹10 below buy price. Placed at the exchange via a
	// paired GTT rule alongside the 1.5% target. 0 disables the SL GTT.
	FNOStopLossPaise int64
	FNOTickPaise     int64 // exchange tick size for GTT prices (default 5 paise)
	AngelAPIKey      string
	AngelClientID    string
	AngelPassword    string
	AngelTOTP        string
	LogPrefix        string // log prefix, e.g. "[order_executor]" or "[order_executor_ind]"

	// Circuit breaker config (optional — defaults used if zero)
	CBMaxFailures  int           // consecutive failures before circuit opens (default: 3)
	CBResetTimeout time.Duration // time to wait before half-open probe (default: 30s)

	// Rate limiter config (optional — defaults used if zero)
	RLMaxOrders int           // max orders per window (default: 10)
	RLWindow    time.Duration // rate limit window (default: 60s)

	// Session manager (optional — if provided, uses session manager instead of creating new session)
	SessionManager *smartconnect.SessionManager
}

// OrderExecutor handles FNO order placement via SmartConnect.
type OrderExecutor struct {
	cfg Config
	sc  *smartconnect.SmartConnect
	api broker // what orders go through; the same client as sc in production

	// keyLocks serialises orders per position key; see keyLock.
	keyLocks sync.Map

	// Rate-limited order-book reader, created on first use.
	bookOnce sync.Once
	books    *bookReader

	// Optional operator alert sink; set via SetAlerter.
	alerter func(msg string)

	// Optional background-state-change hook; set via SetStateListener.
	onState func()

	// Optional synchronous persister run before a real order is sent; set
	// via SetIntentPersister.
	persistIntent func() error

	// exitRetries counts automatic resends of a failed real exit per
	// position key; see scheduleExitRetry.
	exitRetries map[string]int
	cb          *circuitbreaker.CircuitBreaker
	rl          *RateLimiter

	// Optional Prometheus metrics sink; set via AttachMetrics.
	prom *metrics.Metrics

	// Optional dynamic strike picker; set via SetStrikePicker.
	picker *StrikePicker

	// Optional PnL tracker for profit cap checking; set via SetPnLTracker.
	pnlTracker PnLTracker

	// Optional fill listener; set via SetFillListener.
	onFill func(FillReport)

	mu           sync.RWMutex
	ltp          map[string]int64 // token → latest price (paise)
	positionInst map[string]fnoInstrument
	entryOrders  map[string]OrderRecord // position key → entry order details (value type; mutations write the whole record back under mu)
	inflight     map[string]inflightBuy // BUYs between the position gate and a stored entry

	live      bool // true = real orders, false = dry-run log
	sessionOK bool
}

// FillReport is a completed fill: broker-confirmed for a real order,
// simulated at LTP for a paper one. P&L is recorded from these, not from
// signals, so an order that never filled never shows as a trade.
type FillReport struct {
	Signal         strategy.Signal
	Direction      string // BUY or SELL
	FillPricePaise int64
	Qty            int64 // filled quantity
	Real           bool  // true when the fill happened at the broker
}

// SetFillListener registers fn to be called for every fill, real or paper.
// fn runs on the order goroutine and must not block.
func (oe *OrderExecutor) SetFillListener(fn func(FillReport)) {
	oe.mu.Lock()
	oe.onFill = fn
	oe.mu.Unlock()
}

// PnLTracker interface for profit cap checking
type PnLTracker interface {
	GetStrategyDailyPnL(strategyName string) int64
}

type fnoInstrument struct {
	Token  string
	Symbol string
}

// OrderRecord tracks order details for BUY/SELL pairing
type OrderRecord struct {
	OrderID       string    // Broker order ID
	ClientOrderID string    // Our idempotency key
	Symbol        string    // Trading symbol
	Token         string    // Exchange token
	Side          string    // BUY or SELL
	Quantity      int64     // Order quantity
	Price         int64     // Order price (paise)
	Timestamp     time.Time // Order placement time
	StrategyName  string    // Strategy that placed the order
	PositionSide  string    // CALL or PUT
	IndexToken    string    // token of the signal that opened the position (P&L key)
	IndexExchange string    // exchange of that signal
	GttRuleID     string    // Angel One GTT rule ID for 1.5% target (empty if none)
	GttSLRuleID   string    // Angel One GTT rule ID for fixed stop loss (empty if none)
	Real          bool      // true when the order went to the broker (not paper/dry-run)
	Pending       bool      // real BUY whose outcome is not yet known; being settled in background
	ExitOrderID   string    // real SELL sent but not yet confirmed filled
	ExitRequested bool      // exit asked for while the BUY was Pending; sent once it settles
}

// NewOrderExecutor creates a new order executor.
// If live=true, it initialises a SmartConnect session.
func NewOrderExecutor(cfg Config) *OrderExecutor {
	// Apply defaults for circuit breaker config
	cbMaxFailures := cfg.CBMaxFailures
	if cbMaxFailures <= 0 {
		cbMaxFailures = 3
	}
	cbTimeout := cfg.CBResetTimeout
	if cbTimeout <= 0 {
		cbTimeout = 30 * time.Second
	}

	// Apply defaults for rate limiter config
	rlMaxOrders := cfg.RLMaxOrders
	if rlMaxOrders <= 0 {
		rlMaxOrders = 10
	}
	rlWindow := cfg.RLWindow
	if rlWindow <= 0 {
		rlWindow = 60 * time.Second
	}

	// Persist resolved defaults back into cfg so log lines and gauges report
	// the values the runtime actually uses, not the zero from input cfg.
	cfg.CBMaxFailures = cbMaxFailures
	cfg.CBResetTimeout = cbTimeout
	cfg.RLMaxOrders = rlMaxOrders
	cfg.RLWindow = rlWindow

	oe := &OrderExecutor{
		cfg:          cfg,
		ltp:          make(map[string]int64),
		inflight:     make(map[string]inflightBuy),
		positionInst: make(map[string]fnoInstrument),
		entryOrders:  make(map[string]OrderRecord),
		exitRetries:  make(map[string]int),
		live:         cfg.LiveOrders,
		cb:           circuitbreaker.NewCircuitBreaker(cbMaxFailures, cbTimeout),
		rl:           NewRateLimiter(rlMaxOrders, rlWindow),
	}

	prefix := cfg.LogPrefix
	if prefix == "" {
		prefix = "[order_executor]"
	}

	// Log circuit breaker state transitions
	oe.cb.OnStateChange = func(from, to circuitbreaker.State) {
		log.Printf("%s 🔌 Circuit breaker: %s → %s", prefix, from, to)
		// Runs under the breaker's lock: set the gauge from `to` directly.
		if oe.prom != nil {
			oe.prom.OrderCBState.Set(float64(to))
		}
	}

	log.Printf("%s ⚡ Circuit breaker: max_failures=%d reset_timeout=%v", prefix, cbMaxFailures, cbTimeout)
	log.Printf("%s ⚡ Rate limiter: max_orders=%d window=%v", prefix, rlMaxOrders, rlWindow)

	if cfg.LiveOrders {
		// Use session manager if provided (recommended for production)
		if cfg.SessionManager != nil {
			oe.sc = cfg.SessionManager.GetClient()
			if oe.sc != nil {
				oe.api = oe.sc
			}
			oe.sessionOK = oe.sc != nil
			log.Printf("%s ✅ Using session manager for order execution (auto-refresh enabled)", prefix)
			if cfg.PaperTrade {
				log.Printf("%s 📋 PAPER TRADING MODE (test orders only)", prefix)
			} else {
				log.Printf("%s 🔴 LIVE ORDER MODE (real money)", prefix)
			}
			return oe
		}

		// Fallback: create own session (will expire without auto-refresh)
		if cfg.AngelAPIKey == "" || cfg.AngelClientID == "" {
			log.Printf("%s ⚠️  LIVE_ORDERS=true but Angel credentials missing — falling back to dry-run", prefix)
			oe.live = false
			return oe
		}

		log.Printf("%s ⚠️  WARNING: Creating session without session manager - will expire after 90 minutes!", prefix)
		sc := smartconnect.NewSmartConnect(smartconnect.Config{
			APIKey: cfg.AngelAPIKey,
		})

		// Generate TOTP
		totpCode, err := totp.GenerateCode(cfg.AngelTOTP, time.Now())
		if err != nil {
			log.Printf("%s ⚠️  TOTP generation failed: %v — falling back to dry-run", prefix, err)
			oe.live = false
			return oe
		}

		// Login
		_, err = sc.GenerateSession(cfg.AngelClientID, cfg.AngelPassword, totpCode)
		if err != nil {
			log.Printf("%s ⚠️  SmartConnect login failed: %v — falling back to dry-run", prefix, err)
			oe.live = false
			return oe
		}

		oe.sc = sc
		oe.api = sc
		oe.sessionOK = true

		if cfg.PaperTrade {
			log.Printf("%s ✅ SmartConnect session established — PAPER TRADING MODE (test orders only)", prefix)
		} else {
			log.Printf("%s ✅ SmartConnect session established — LIVE ORDER MODE", prefix)
		}
	} else {
		log.Printf("%s 📋 Dry-run mode — orders will be logged but NOT placed", prefix)
	}

	return oe
}

// UpdateLTP updates the latest traded price for a token from tick data.
func (oe *OrderExecutor) UpdateLTP(tick model.Tick) {
	oe.mu.Lock()
	oe.ltp[tick.Token] = tick.Price
	oe.mu.Unlock()
}

// GetLTP returns the current LTP for a token (0 if unknown).
func (oe *OrderExecutor) GetLTP(token string) int64 {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	return oe.ltp[token]
}

// RefreshSession re-authenticates with SmartConnect.
// Call this when an order fails due to an expired session.
func (oe *OrderExecutor) RefreshSession() error {
	prefix := oe.logPrefix()

	if oe.cfg.SessionManager != nil {
		log.Printf("%s 🔄 delegating session refresh to SessionManager...", prefix)
		return oe.cfg.SessionManager.RefreshSession()
	}

	if oe.cfg.AngelAPIKey == "" || oe.cfg.AngelClientID == "" {
		return fmt.Errorf("missing Angel credentials")
	}

	sc := smartconnect.NewSmartConnect(smartconnect.Config{
		APIKey: oe.cfg.AngelAPIKey,
	})

	totpCode, err := totp.GenerateCode(oe.cfg.AngelTOTP, time.Now())
	if err != nil {
		return fmt.Errorf("TOTP generation: %w", err)
	}

	_, err = sc.GenerateSession(oe.cfg.AngelClientID, oe.cfg.AngelPassword, totpCode)
	if err != nil {
		return fmt.Errorf("SmartConnect login: %w", err)
	}

	oe.mu.Lock()
	oe.sc = sc
	oe.api = sc
	oe.sessionOK = true
	oe.mu.Unlock()

	log.Printf("%s 🔄 SmartConnect session refreshed successfully (standalone)", prefix)
	return nil
}

func (oe *OrderExecutor) logPrefix() string {
	if oe.cfg.LogPrefix != "" {
		return oe.cfg.LogPrefix
	}
	return "[order_executor]"
}

// SetStrikePicker attaches a dynamic strike picker.
// When set, ExecuteSignal uses the picker's resolved tokens instead of
// the static config tokens. If the picker is nil or not yet resolved,
// static config tokens are used as fallback.
func (oe *OrderExecutor) SetStrikePicker(picker *StrikePicker) {
	oe.picker = picker
	log.Printf("%s 🎯 dynamic strike picker attached", oe.logPrefix())
}

// SetPnLTracker attaches a P&L tracker for profit cap checking.
func (oe *OrderExecutor) SetPnLTracker(tracker PnLTracker) {
	oe.pnlTracker = tracker
	log.Printf("%s 💰 P&L tracker attached for profit cap monitoring", oe.logPrefix())
}

// GetSmartConnect returns the internal SmartConnect client (e.g. for
// passing to a StrikePicker). Returns nil if not authenticated.
func (oe *OrderExecutor) GetSmartConnect() *smartconnect.SmartConnect {
	return oe.sc
}

// AttachMetrics wires the executor to a Prometheus metrics sink.
// Call this once after creating the executor.
func (oe *OrderExecutor) AttachMetrics(prom *metrics.Metrics) {
	if prom == nil {
		return
	}
	oe.prom = prom
	// Initialise the max gauge once so the dashboard always shows it.
	prom.OrderRLMax.Set(float64(oe.cfg.RLMaxOrders))
}

// reportMetrics pushes the current CB state, CB failures, and RL count
// to Prometheus. Safe to call from any goroutine.
// ReportMetrics pushes breaker and limiter state to Prometheus; call it on
// a ticker so the gauges don't go stale between orders.
func (oe *OrderExecutor) ReportMetrics() { oe.reportMetrics() }

func (oe *OrderExecutor) reportMetrics() {
	if oe.prom == nil {
		return
	}
	oe.prom.OrderCBState.Set(float64(oe.cb.CurrentState()))
	oe.prom.OrderCBFailures.Set(float64(oe.cb.Failures()))
	oe.prom.OrderRLCount.Set(float64(oe.rl.Count()))
}

func (oe *OrderExecutor) positionKey(sig strategy.Signal) string {
	return sig.StrategyName + "|" + string(sig.Side)
}

func (oe *OrderExecutor) getPositionInstrument(sig strategy.Signal) (fnoInstrument, bool) {
	key := oe.positionKey(sig)
	oe.mu.RLock()
	inst, ok := oe.positionInst[key]
	oe.mu.RUnlock()
	return inst, ok
}

func (oe *OrderExecutor) setPositionInstrument(sig strategy.Signal, token, symbol string) {
	key := oe.positionKey(sig)
	oe.mu.Lock()
	oe.positionInst[key] = fnoInstrument{Token: token, Symbol: symbol}
	oe.mu.Unlock()
}

func (oe *OrderExecutor) clearPositionInstrument(sig strategy.Signal) {
	key := oe.positionKey(sig)
	oe.mu.Lock()
	delete(oe.positionInst, key)
	oe.mu.Unlock()
}

func (oe *OrderExecutor) resolveInstrument(sig strategy.Signal, direction string) (string, string, error) {
	prefix := oe.logPrefix()
	isCall := sig.Side == strategy.SideCall
	isPut := sig.Side == strategy.SidePut

	if !isCall && !isPut {
		return "", "", fmt.Errorf("%s signal side %q — cannot determine CALL/PUT side", prefix, sig.Side)
	}

	// Exits must use the exact instrument used at entry, even if dynamic strikes
	// have shifted or picker/static fallback changed later.
	if direction == "SELL" {
		if inst, ok := oe.getPositionInstrument(sig); ok {
			return inst.Token, inst.Symbol, nil
		}
		log.Printf("%s ⚠️  no locked entry instrument for %s %s:%s — falling back to current side mapping",
			prefix, direction, sig.StrategyName, sig.Side)
	}

	if oe.picker != nil && oe.picker.Resolved() {
		if isCall {
			ce := oe.picker.GetCallToken()
			return ce.Token, ce.Symbol, nil
		}
		pe := oe.picker.GetPutToken()
		return pe.Token, pe.Symbol, nil
	}

	// No resolved strike: use the static tokens, if any are configured.
	token, symbol := oe.cfg.PutFNOToken, oe.cfg.PutFNOSymbol
	if isCall {
		token, symbol = oe.cfg.CallFNOToken, oe.cfg.CallFNOSymbol
	}
	if token == "" {
		if direction == "SELL" {
			return "", "", fmt.Errorf("%s 🚫 no strike resolved and no entry instrument for %s %s:%s — exit has nothing to close",
				prefix, direction, sig.StrategyName, sig.Side)
		}
		return "", "", fmt.Errorf("%s 🚫 no strike resolved yet — entry skipped for %s:%s",
			prefix, sig.StrategyName, sig.Side)
	}
	return token, symbol, nil
}

// ExecuteSignal places a real or dry-run order based on the strategy signal.
// It enforces mutual exclusion: CALL and PUT cannot be active simultaneously.
//
// Safety pipeline: rate limiter → circuit breaker → order placement (with retry).
// If the circuit breaker is open, orders are rejected immediately.
// If the rate limit is exceeded, orders are rejected immediately.
//
// STRATEGY-SPECIFIC ORDER ROUTING:
// - NIFTY50_FNO: Real orders (if LiveOrders=true AND daily profit < threshold)
// - All other strategies: Paper trading only
//
// DAILY PROFIT CAP:
// - If NIFTY50_FNO daily profit >= 10 points (1000 paise), switch to paper trading
// - Resets at market open next day
func (oe *OrderExecutor) ExecuteSignal(sig strategy.Signal) {
	prefix := oe.logPrefix()
	defer oe.reportMetrics()

	var direction string
	switch sig.Action {
	case strategy.ActionBuy:
		direction = "BUY"
	case strategy.ActionExit, strategy.ActionSell:
		direction = "SELL"
	default:
		log.Printf("%s unknown action %q", prefix, sig.Action)
		return
	}
	if sig.Side != strategy.SideCall && sig.Side != strategy.SidePut {
		log.Printf("%s signal side %q — cannot determine CALL/PUT side", prefix, sig.Side)
		return
	}
	isExit := direction == "SELL"

	// Every order for one position runs one at a time: a SELL waits for its
	// own in-flight BUY instead of racing it onto a different contract.
	posKey := oe.positionKey(sig)
	kl := oe.keyLock(posKey)
	kl.Lock()
	defer kl.Unlock()

	// ── Routing ──
	// Only NIFTY50_FNO places real entries. A real exit is sent only against
	// a real tracked entry, so an exit for an entry that was never sent (kill
	// switch, profit cap, paper) can never open a naked short.
	live := oe.liveSession()
	isRealOrderStrategy := sig.StrategyName == "NIFTY50_FNO"
	entry, hasEntry := oe.entry(posKey)
	realEntry := hasEntry && entry.Real
	sendReal := isRealOrderStrategy && live
	if isExit {
		switch {
		case realEntry && entry.ExitOrderID != "":
			log.Printf("%s ⏳ EXIT already pending for %s (order %s) — not sending another SELL", prefix, posKey, entry.ExitOrderID)
			return
		case realEntry && entry.Pending:
			// The BUY's fill is not known yet. Remember the exit; it is sent
			// the moment the BUY settles as filled (see onBuySettled).
			oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitRequested = true })
			log.Printf("%s ⏳ EXIT for %s deferred — BUY still settling; will be sent once it fills", prefix, posKey)
			return
		case realEntry && !live:
			oe.alertf("EXIT for REAL position %s %s but no live broker session — position left OPEN, close manually", posKey, entry.Symbol)
			return
		}
		if !realEntry && sendReal {
			log.Printf("%s ℹ️  EXIT for %s has no real entry tracked — handled as paper, no broker order", prefix, posKey)
		}
		sendReal = realEntry && live
	}
	orderMode := "PAPER"
	if sendReal {
		orderMode = "REAL"
	}

	fnoToken, fnoSymbol, err := oe.resolveInstrument(sig, direction)
	if err != nil {
		log.Printf("%s %v", prefix, err)
		return
	}

	idempotencyKey := generateIdempotencyKey(sig)
	log.Printf("%s 🔑 idempotency_key=%s", prefix, idempotencyKey)

	// ── Position gate (entries) ──
	// A BUY is refused while this position is open or pending, or while an
	// opposite-side position exists that it must not coexist with: one of
	// the same strategy, or — for a real BUY — any real one. Paper and real
	// positions don't block each other. The check and the reservation are
	// one step under oe.mu, so two BUYs on different keys can't both pass.
	if !isExit {
		oe.mu.Lock()
		_, instLocked := oe.positionInst[posKey]
		switch {
		case hasEntry || instLocked:
			oe.mu.Unlock()
			log.Printf("%s 🚫 BLOCKED: %s BUY while %s is already open or pending", prefix, sig.Side, posKey)
			return
		case oe.oppositeConflictLocked(sig, sendReal):
			oe.mu.Unlock()
			log.Printf("%s 🚫 BLOCKED: %s BUY for %s while an opposite-side position is active — wait for its exit", prefix, sig.Side, sig.StrategyName)
			return
		}
		oe.inflight[posKey] = inflightBuy{strategy: sig.StrategyName, side: sig.Side, real: sendReal}
		oe.mu.Unlock()
		defer func() {
			oe.mu.Lock()
			delete(oe.inflight, posKey)
			oe.mu.Unlock()
		}()
	}

	// Rate limiter and circuit breaker guard the broker: they apply to real
	// entries only. Paper orders never reach the broker, and exits are never
	// refused — an open position must always be closable.
	if !isExit && sendReal {
		if err := oe.rl.Allow(); err != nil {
			log.Printf("%s 🚫 RATE LIMITED: %s %s %s — too many orders in window (count=%d)",
				prefix, direction, sig.StrategyName, fnoSymbol, oe.rl.Count())
			return
		}
		if oe.cb.CurrentState() == circuitbreaker.StateOpen {
			log.Printf("%s 🔌 CIRCUIT OPEN: %s %s %s — broker unreachable, skipping order (failures=%d)",
				prefix, direction, sig.StrategyName, fnoSymbol, oe.cb.Failures())
			return
		}
	}

	ltp := oe.GetLTP(fnoToken)
	log.Printf("%s 📊 [%s] %s %s | token=%s symbol=%s qty=%d ltp=%d reason=%s",
		prefix, orderMode, direction, sig.StrategyName, fnoToken, fnoSymbol, oe.cfg.Qty, ltp, sig.Reason)

	if !sendReal {
		oe.executePaper(sig, posKey, direction, fnoToken, fnoSymbol, ltp, idempotencyKey, isRealOrderStrategy)
		return
	}
	qty := oe.cfg.Qty
	if isExit && realEntry && entry.Quantity > 0 {
		qty = entry.Quantity // sell what is held, not the configured size
	}
	oe.executeReal(sig, posKey, direction, fnoToken, fnoSymbol, ltp, qty, idempotencyKey)
}

// executePaper simulates an order: position tracking moves as if filled.
// It never touches a real entry (routing sends those to executeReal or
// refuses them).
func (oe *OrderExecutor) executePaper(sig strategy.Signal, posKey, direction, fnoToken, fnoSymbol string, ltp int64, key string, isRealOrderStrategy bool) {
	prefix := oe.logPrefix()
	dryRunOrderID := fmt.Sprintf("PAPER_%s_%s_%d", sig.StrategyName, direction, time.Now().UnixMilli())
	paperReason := "DRY-RUN"
	if !isRealOrderStrategy {
		paperReason = "PAPER (strategy not enabled for real orders)"
	}
	log.Printf("%s 📋 %s: would %s %d lots of %s (%s) at LTP=%d",
		prefix, paperReason, direction, oe.cfg.Qty, fnoSymbol, fnoToken, ltp)

	if direction == "BUY" {
		oe.setPositionInstrument(sig, fnoToken, fnoSymbol)
		oe.mu.Lock()
		oe.entryOrders[posKey] = OrderRecord{
			OrderID:       dryRunOrderID,
			ClientOrderID: key,
			Symbol:        fnoSymbol,
			Token:         fnoToken,
			Side:          direction,
			Quantity:      oe.cfg.Qty,
			Price:         ltp,
			Timestamp:     time.Now(),
			StrategyName:  sig.StrategyName,
			PositionSide:  string(sig.Side),
			IndexToken:    sig.Token,
			IndexExchange: sig.Exchange,
		}
		oe.mu.Unlock()
		log.Printf("%s 📝 DRY-RUN: Entry order tracked: %s → orderID=%s", prefix, posKey, dryRunOrderID)
		oe.reportFill(sig, direction, ltp, oe.cfg.Qty, false)
		return
	}

	entryOrder, hadEntry := oe.entry(posKey)
	if hadEntry {
		log.Printf("%s 🔗 DRY-RUN: SELL order %s closes BUY order %s | Entry: ₹%.2f → Exit: ₹%.2f | P&L: ₹%.2f",
			prefix, dryRunOrderID, entryOrder.OrderID,
			float64(entryOrder.Price)/100, float64(ltp)/100,
			float64(ltp-entryOrder.Price)/100*float64(oe.cfg.Qty))
	}
	oe.mu.Lock()
	delete(oe.entryOrders, posKey)
	oe.mu.Unlock()
	oe.clearPositionInstrument(sig)
	// An exit with nothing to close is not a trade.
	if hadEntry {
		oe.reportFill(sig, direction, ltp, entryOrder.Quantity, false)
	}
}

// executeReal sends an order to the broker and settles it. The position
// only changes state on broker evidence: an entry is recorded once accepted
// (provisionally if the outcome is unknown), and an exit closes the
// position only once the broker reports it complete or already flat.
func (oe *OrderExecutor) executeReal(sig strategy.Signal, posKey, direction, fnoToken, fnoSymbol string, ltp, qty int64, key string) {
	prefix := oe.logPrefix()
	isExit := direction == "SELL"

	// ── Intent ──
	// Record what is about to be sent, and persist it, before the broker
	// sees the order. A crash anywhere after placeOrder then restarts with
	// a Pending entry (or an exit marked by tag) that ResumeSettlement
	// settles from the order book by ordertag — never a real position the
	// executor does not know about.
	if isExit {
		oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "tag:" + key })
		if err := oe.persistIntentNow(); err != nil {
			// Never block an exit: send it anyway.
			log.Printf("%s ⚠️  exit intent for %s not persisted (%v) — sending anyway", prefix, posKey, err)
		}
	} else {
		oe.storeEntry(sig, OrderRecord{
			ClientOrderID: key,
			Symbol:        fnoSymbol,
			Token:         fnoToken,
			Side:          direction,
			Quantity:      qty,
			Price:         ltp,
			Real:          true,
			Pending:       true,
			Timestamp:     time.Now(),
			StrategyName:  sig.StrategyName,
			PositionSide:  string(sig.Side),
		})
		if err := oe.persistIntentNow(); err != nil {
			oe.dropEntry(sig)
			oe.alertf("BUY %s %s NOT sent: order intent could not be persisted (%v)", sig.StrategyName, fnoSymbol, err)
			return
		}
	}

	// SELL ordering: send SELL to broker first, cancel GTT after. If a GTT
	// fired first, the broker rejects the SELL as "position flat"; that is
	// a completed exit, not a failure.
	var orderID string
	var sellPositionFlat bool
	runCB := oe.cb.Execute
	if isExit {
		runCB = oe.cb.ExecuteAlways
	}
	cbErr := runCB(func() error {
		attempt := orderAttempt{
			tag: key,
			place: func(tag string) (string, error) {
				return oe.placeOrder(fnoSymbol, fnoToken, direction, ltp, qty, tag)
			},
			orderBook:    oe.readOrderBook,
			refresh:      oe.RefreshSession,
			isFinal:      func(err error) bool { return isExit && isPositionFlatError(err) },
			lookupDelays: defaultLookupDelays,
		}
		var err error
		orderID, err = attempt.run()
		if err == nil {
			return nil
		}
		if isExit && isPositionFlatError(err) {
			log.Printf("%s ✅ SELL skipped — position already flat at broker (GTT booked first): %v", prefix, err)
			sellPositionFlat = true
			return nil
		}
		log.Printf("%s ❌ ORDER FAILED: %s %s %s tag=%s — %v", prefix, direction, fnoSymbol, fnoToken, key, err)
		return err
	})

	if cbErr != nil {
		switch {
		case errors.Is(cbErr, ErrOrderStateUnknown):
			oe.settleUnknown(sig, posKey, direction, fnoToken, fnoSymbol, ltp, qty, key)
		case cbErr == circuitbreaker.ErrCircuitOpen:
			log.Printf("%s 🔌 CIRCUIT OPEN during execution — order rejected", prefix)
			if isExit {
				oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "" })
				oe.scheduleExitRetry(sig)
			} else {
				oe.dropEntry(sig)
			}
		default:
			log.Printf("%s ❌ ORDER FAILED (circuit breaker failures %d/%d): %v",
				prefix, oe.cb.Failures(), oe.cfg.CBMaxFailures, cbErr)
			if isExit {
				oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "" })
				oe.alertf("EXIT FAILED — %s position still OPEN at broker: %v", fnoSymbol, cbErr)
				oe.scheduleExitRetry(sig)
			} else {
				oe.dropEntry(sig)
				log.Printf("%s 🔄 BUY failed — no entry recorded, %s free for the next signal", prefix, posKey)
			}
		}
		return
	}

	if sellPositionFlat {
		// "No holdings" / "net quantity" can also mean the SELL was larger
		// than what is held. Only the broker's net position says flat.
		held, perr := oe.brokerNetQty(fnoToken)
		switch {
		case perr != nil:
			oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "" })
			oe.alertf("SELL %s rejected as flat but broker positions unreadable (%v) — %s kept OPEN with GTT protection; verify manually", fnoSymbol, perr, posKey)
		case held != 0:
			oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "" })
			oe.alertf("SELL %s rejected as flat but broker still holds %d — %s kept OPEN with GTT protection; close manually", fnoSymbol, held, posKey)
		default:
			// Closed at the exchange (GTT) at a price we don't know: book
			// the exit at LTP so P&L doesn't carry a phantom open trade.
			oe.finalizeExit(sig)
			oe.reportFill(sig, direction, ltp, qty, true)
		}
		return
	}

	// ── Fill confirmation ──
	// An accepted order is not a filled one: the broker can still reject it
	// (e.g. margin). Use the real average price, not the signal-time LTP.
	fill, ferr := confirmFill(oe.readOrderBook, orderID, defaultFillPollDelays)
	if ferr == nil && fill.Rejected() && !isExit {
		log.Printf("%s ❌ BUY %s AFTER ACCEPT: %s orderID=%s — %s",
			prefix, strings.ToUpper(fill.Status), fnoSymbol, orderID, fill.Text)
		oe.dropEntry(sig)
		return
	}
	if ferr == nil && isExit && fill.terminal() && fill.Status != "complete" {
		oe.applyExitResult(sig, orderID, fill)
		return
	}
	filled := ferr == nil && (fill.Status == "complete" || fill.Partial())
	if filled && fill.AvgPricePaise <= 0 {
		log.Printf("%s ⚠️  FILLED but average price unreadable: %s orderID=%s — using ltp", prefix, fnoSymbol, orderID)
	}
	fillPrice := ltp
	if filled && fill.AvgPricePaise > 0 {
		fillPrice = fill.AvgPricePaise
		log.Printf("%s ✅ FILLED: %s %s orderID=%s avg=%d paise (ltp was %d)", prefix, direction, fnoSymbol, orderID, fillPrice, ltp)
	}

	if !isExit {
		rec := OrderRecord{
			OrderID:       orderID,
			ClientOrderID: key,
			Symbol:        fnoSymbol,
			Token:         fnoToken,
			Side:          direction,
			Quantity:      qty,
			Price:         fillPrice,
			Real:          true,
			Timestamp:     time.Now(),
			StrategyName:  sig.StrategyName,
			PositionSide:  string(sig.Side),
		}
		if filled {
			rec, _ = oe.filledEntry(rec, fill)
			oe.commitEntry(sig, rec)
			log.Printf("%s 📝 Entry tracked: %s → orderID=%s price=%d qty=%d", prefix, posKey, orderID, rec.Price, rec.Quantity)
			oe.reportFill(sig, direction, rec.Price, rec.Quantity, true)
			return
		}
		// Accepted but not confirmed filled: track it as Pending (blocks a
		// second BUY, defers exits, no GTTs on an unfilled order) and settle
		// it from the book in the background.
		rec.Pending = true
		oe.storeEntry(sig, rec)
		log.Printf("%s ⚠️  BUY %s not confirmed filled yet (status=%q, err=%v) — pending, following in background", prefix, orderID, fill.Status, ferr)
		oe.pollOrderAsync(posKey, "BUY "+orderID, matchByID(orderID), func(oid string, f fillResult) {
			oe.onBuySettled(sig, oid, f)
		}, nil)
		return
	}

	if filled && fill.Status == "complete" {
		entryOrder, _ := oe.entry(posKey)
		log.Printf("%s 🔗 SELL %s closes BUY %s | Entry: ₹%.2f → Exit: ₹%.2f | P&L: ₹%.2f",
			prefix, orderID, entryOrder.OrderID,
			float64(entryOrder.Price)/100, float64(fillPrice)/100,
			float64(fillPrice-entryOrder.Price)/100*float64(oe.cfg.Qty))
		oe.finalizeExit(sig)
		oe.reportFill(sig, direction, fillPrice, qty, true)
		return
	}

	// Exit accepted but not complete: the position is still (partly) open.
	// Keep the entry and its GTT stop loss until the broker says complete.
	log.Printf("%s ⏳ SELL %s not confirmed filled (status=%q, err=%v) — position kept open with GTT protection, following in background",
		prefix, orderID, fill.Status, ferr)
	oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = orderID })
	oe.pollOrderAsync(posKey, "SELL "+orderID, matchByID(orderID), func(_ string, f fillResult) {
		oe.applyExitResult(sig, orderID, f)
	}, nil)
}

// settleUnknown handles an order whose outcome could not be settled
// inline. The position is marked so nothing else is sent for it, and the
// order book is followed in the background by the attempt's tags.
func (oe *OrderExecutor) settleUnknown(sig strategy.Signal, posKey, direction, fnoToken, fnoSymbol string, ltp, qty int64, key string) {
	prefix := oe.logPrefix()
	oe.alertf("ORDER STATE UNKNOWN — %s %s ordertag=%s/%s; following order book in background",
		direction, fnoSymbol, key, retryTag(key))
	match := matchByTags(key, retryTag(key))

	if direction == "BUY" {
		// Provisional real entry: blocks a second BUY, defers exits, and
		// lets the background settle adopt it with GTT protection.
		oe.storeEntry(sig, OrderRecord{
			ClientOrderID: key,
			Symbol:        fnoSymbol,
			Token:         fnoToken,
			Side:          direction,
			Quantity:      qty,
			Price:         ltp,
			Real:          true,
			Pending:       true,
			Timestamp:     time.Now(),
			StrategyName:  sig.StrategyName,
			PositionSide:  string(sig.Side),
		})
		oe.pollOrderAsync(posKey, "unknown BUY "+key, match, func(orderID string, f fillResult) {
			log.Printf("%s unknown BUY %s settled: orderID=%s status=%s", prefix, key, orderID, f.Status)
			oe.onBuySettled(sig, orderID, f)
		}, func() {
			log.Printf("%s ✅ unknown BUY %s settled: never reached broker — clearing", prefix, key)
			oe.dropEntry(sig)
		})
		return
	}

	oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "tag:" + key })
	oe.pollOrderAsync(posKey, "unknown SELL "+key, match, func(orderID string, f fillResult) {
		log.Printf("%s unknown SELL %s settled: orderID=%s status=%s", prefix, key, orderID, f.Status)
		oe.applyExitResult(sig, orderID, f)
	}, func() {
		oe.updateEntry(posKey, func(r *OrderRecord) { r.ExitOrderID = "" })
		oe.alertf("unknown SELL %s never reached broker — %s position still OPEN, close manually", key, fnoSymbol)
	})
}

// placeOrder builds and sends the order request.
func (oe *OrderExecutor) placeOrder(fnoSymbol, fnoToken, direction string, ltp, qty int64, tag string) (string, error) {
	params := oe.buildOrderParams(fnoSymbol, fnoToken, direction, ltp, qty, tag)

	// Use paper trading method if enabled
	api := oe.brokerAPI()
	if api == nil {
		return "", fmt.Errorf("no broker session")
	}
	if oe.cfg.PaperTrade {
		log.Printf("%s 📋 Placing PAPER TRADE order: %s %s", oe.logPrefix(), direction, fnoSymbol)
		return api.PlaceOrderPaperTrade(params)
	}

	return api.PlaceOrder(params)
}

// buildOrderParams builds the Angel One placeOrder payload. tag is sent as
// ordertag so the order can be found in the order book if the placement
// response is lost.
func (oe *OrderExecutor) buildOrderParams(fnoSymbol, fnoToken, direction string, ltp, qty int64, tag string) map[string]any {
	params := map[string]any{
		"variety":         "NORMAL",
		"tradingsymbol":   fnoSymbol,
		"symboltoken":     fnoToken,
		"transactiontype": direction,
		"exchange":        oe.cfg.FNOExchange,
		"ordertype":       oe.cfg.FNOOrderType,
		"producttype":     oe.cfg.FNOProductType,
		"duration":        "DAY",
		"quantity":        fmt.Sprintf("%d", qty),
		"ordertag":        tag,
	}

	// For LIMIT orders, set the price (rupees string, tick-aligned, no float)
	if strings.EqualFold(oe.cfg.FNOOrderType, "LIMIT") && ltp > 0 {
		params["price"] = paiseToRupees(floorTick(ltp, oe.tickPaise()))
	}
	return params
}

// placeTargetGTT places a GTT SELL rule at 1.5% above the entry price.
// This lets the exchange auto-sell when the target is hit, with zero system dependency.
// gttTargetPaise is the GTT target at +1.5% of entry, floored to the tick.
func gttTargetPaise(entryPaise, tickPaise int64) int64 {
	return floorTick(entryPaise*1015/1000, tickPaise)
}

// gttStopLossPaise is the GTT stop trigger (entry − sl, raised to the tick
// so it fires no later than intended) and its limit 50 paise below
// (lowered to the tick so the sell still fills on a fast drop).
func gttStopLossPaise(entryPaise, slPaise, tickPaise int64) (trigger, limit int64) {
	trigger = ceilTick(entryPaise-slPaise, tickPaise)
	limit = floorTick(trigger-50, tickPaise)
	if limit <= 0 {
		limit = trigger
	}
	return trigger, limit
}

func floorTick(p, tick int64) int64 {
	if tick <= 1 {
		return p
	}
	return p / tick * tick
}

func ceilTick(p, tick int64) int64 {
	if tick <= 1 {
		return p
	}
	return (p + tick - 1) / tick * tick
}

func paiseToRupees(p int64) string { return fmt.Sprintf("%d.%02d", p/100, p%100) }

func (oe *OrderExecutor) tickPaise() int64 {
	if oe.cfg.FNOTickPaise > 0 {
		return oe.cfg.FNOTickPaise
	}
	return 5 // NSE F&O options tick
}

func (oe *OrderExecutor) placeTargetGTT(posKey, fnoSymbol, fnoToken string, entryPricePaise, qty int64) string {
	api := oe.brokerAPI()
	if api == nil || entryPricePaise <= 0 {
		oe.alertf("GTT TARGET not placed for %s: entry price unknown (%d) — position has no exchange-side target", posKey, entryPricePaise)
		return ""
	}
	targetPriceRupees := paiseToRupees(gttTargetPaise(entryPricePaise, oe.tickPaise()))
	params := map[string]any{
		"tradingsymbol":   fnoSymbol,
		"symboltoken":     fnoToken,
		"exchange":        oe.cfg.FNOExchange,
		"transactiontype": "SELL",
		"producttype":     oe.cfg.FNOProductType,
		"price":           targetPriceRupees,
		"triggerprice":    targetPriceRupees, // trigger = target for limit sell
		"qty":             fmt.Sprintf("%d", qty),
		"timeperiod":      365,
	}
	gttID, err := api.GTTCreateRule(params)
	if err != nil {
		oe.alertf("GTT TARGET failed for %s: %v — position has no exchange-side target", posKey, err)
		return ""
	}
	log.Printf("%s 🎯 GTT TARGET placed: %s SELL %d @ %s (1.5%% over entry %s) gttID=%s",
		oe.logPrefix(), fnoSymbol, qty, targetPriceRupees, paiseToRupees(entryPricePaise), gttID)
	return gttID
}

// placeStopLossGTT places a GTT SELL rule at FNO entry price minus
// FNOStopLossPaise, so the exchange can square off the position even if
// our process is unreachable or the WS feed stalls when SL hits. It runs
// synchronously under the position lock and returns the rule ID ("" on
// failure, with an operator alert).
//
// Trigger: entry - StopLossPaise. Limit price: trigger - 50 paise so the
// SELL fills even when the option premium drops fast through the level.
func (oe *OrderExecutor) placeStopLossGTT(posKey, fnoSymbol, fnoToken string, entryPricePaise, qty int64) string {
	if oe.cfg.FNOStopLossPaise <= 0 {
		return ""
	}
	api := oe.brokerAPI()
	if api == nil || entryPricePaise <= 0 {
		oe.alertf("GTT STOPLOSS not placed for %s: entry price unknown (%d) — position has NO exchange-side stop loss", posKey, entryPricePaise)
		return ""
	}
	if entryPricePaise-oe.cfg.FNOStopLossPaise <= 0 {
		oe.alertf("GTT STOPLOSS skipped for %s: trigger would be ≤0 (entry=%d, sl=%d) — position has NO exchange-side stop loss",
			posKey, entryPricePaise, oe.cfg.FNOStopLossPaise)
		return ""
	}
	triggerPaise, limitPaise := gttStopLossPaise(entryPricePaise, oe.cfg.FNOStopLossPaise, oe.tickPaise())
	params := map[string]any{
		"tradingsymbol":   fnoSymbol,
		"symboltoken":     fnoToken,
		"exchange":        oe.cfg.FNOExchange,
		"transactiontype": "SELL",
		"producttype":     oe.cfg.FNOProductType,
		"price":           paiseToRupees(limitPaise),
		"triggerprice":    paiseToRupees(triggerPaise),
		"qty":             fmt.Sprintf("%d", qty),
		"timeperiod":      365,
	}
	gttID, err := api.GTTCreateRule(params)
	if err != nil {
		oe.alertf("GTT STOPLOSS failed for %s: %v — position has NO exchange-side stop loss", posKey, err)
		return ""
	}
	log.Printf("%s 🛡️  GTT STOPLOSS placed: %s SELL %d @ %s (trigger %s) gttID=%s",
		oe.logPrefix(), fnoSymbol, qty, paiseToRupees(limitPaise), paiseToRupees(triggerPaise), gttID)
	return gttID
}

// cancelPairedGTTs removes both paired GTT rules (target + stop loss) of
// a position that is closed or never filled. Called from dropEntry under
// the position lock, before the entry (and its rule IDs) is deleted.
func (oe *OrderExecutor) cancelPairedGTTs(posKey string) {
	api := oe.brokerAPI()
	if api == nil {
		return
	}
	entryOrder, hasEntry := oe.entry(posKey)
	if !hasEntry || (entryOrder.GttRuleID == "" && entryOrder.GttSLRuleID == "") {
		return
	}
	// Synchronous, under the position lock: a leftover SELL rule on a flat
	// position can open a naked short when it triggers, so every cancel is
	// confirmed (one retry) and a failure reaches an operator. A rule that
	// already triggered (it may be what closed the position) can't be
	// cancelled; that is expected and only logged.
	for _, g := range []struct{ label, id string }{{"TARGET", entryOrder.GttRuleID}, {"STOPLOSS", entryOrder.GttSLRuleID}} {
		if g.id == "" {
			continue
		}
		var err error
		for attempt := 0; attempt < 2; attempt++ {
			if _, err = api.GTTCancelRule(map[string]any{
				"id":          g.id,
				"symboltoken": entryOrder.Token,
				"exchange":    oe.cfg.FNOExchange,
			}); err == nil {
				break
			}
		}
		switch {
		case err == nil:
			log.Printf("%s 🗑️  GTT %s cancelled: %s gttID=%s", oe.logPrefix(), g.label, posKey, g.id)
		case gttAlreadyGone(err):
			log.Printf("%s GTT %s for %s gttID=%s already triggered/gone: %v", oe.logPrefix(), g.label, posKey, g.id, err)
		default:
			oe.alertf("GTT %s rule %s for %s %s not cancelled (%v) — cancel it manually in Angel One: if it triggers on a flat position it sells short",
				g.label, g.id, posKey, entryOrder.Symbol, err)
		}
	}
}

// gttAlreadyGone reports a cancel failure meaning the rule no longer exists
// or already fired.
func gttAlreadyGone(err error) bool {
	s := strings.ToLower(err.Error())
	for _, m := range []string{"triggered", "not found", "does not exist", "already cancelled", "invalid rule"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// isPositionFlatError returns true if the broker error indicates the SELL
// was rejected because there is no open position to square off. This
// happens when a paired GTT TARGET already booked the exit at the
// exchange before our system SELL reached the broker.
//
// Conservative substring match: covers Angel One's known rejection codes
// for "no holdings" / "net quantity less than order quantity". If a new
// rejection phrase appears, log and add it here — do NOT widen the match
// to anything that could swallow real broker failures.
func isPositionFlatError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "ab1018"): // No Holdings Available
		return true
	case strings.Contains(s, "ab1014"): // Order Quantity Cannot Be More Than Net Quantity
		return true
	case strings.Contains(s, "no holdings"):
		return true
	case strings.Contains(s, "net quantity"):
		return true
	case strings.Contains(s, "no position"):
		return true
	case strings.Contains(s, "insufficient holdings"):
		return true
	case strings.Contains(s, "square off"):
		return true
	}
	return false
}

// generateIdempotencyKey creates a unique key for an order signal
// based on strategy name, side, action, and current timestamp.
// Used for audit trail and duplicate detection.
func generateIdempotencyKey(sig strategy.Signal) string {
	data := fmt.Sprintf("%s:%s:%s:%d", sig.StrategyName, sig.Side, sig.Action, time.Now().UnixMilli())
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:8]) // 16-char hex prefix
}

// GetEntryOrders returns a snapshot of all tracked entry orders.
// Useful for debugging and frontend display.
func (oe *OrderExecutor) GetEntryOrders() map[string]OrderRecord {
	oe.mu.RLock()
	defer oe.mu.RUnlock()

	snapshot := make(map[string]OrderRecord, len(oe.entryOrders))
	for k, v := range oe.entryOrders {
		snapshot[k] = v
	}
	return snapshot
}

// GetEntryOrder retrieves the entry order for a specific position.
// Returns the record and true if found, zero value and false otherwise.
func (oe *OrderExecutor) GetEntryOrder(strategyName string, side strategy.PositionSide) (OrderRecord, bool) {
	key := strategyName + "|" + string(side)
	oe.mu.RLock()
	defer oe.mu.RUnlock()

	order, ok := oe.entryOrders[key]
	return order, ok
}
