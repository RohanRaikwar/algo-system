package strategy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// Nifty50FnO implements the NIFTY 50 F&O Call/Put strategy on 1m candles.
//
// Entry: EMA6 crosses above/below SMA21 (CALL/PUT respectively).
// Exit:  hard/trailing SL on ticks, or reverse on opposite EMA6/SMA21 cross.
// Reverse: EXIT current side and emit opposite BUY signal.
//
// Uses a 10-value prefetch buffer for line-crossing detection on closed candles.
type Nifty50FnO struct {
	mu          sync.Mutex
	instruments map[string]*nifty50FnOState
	qty         int64
	cfg         Nifty50FnOConfig
}

// NewNifty50FnO creates a new strategy with default config.
func NewNifty50FnO(qty int64) *Nifty50FnO {
	return NewNifty50FnOWithConfig(qty, DefaultNifty50FnOConfig())
}

// NewNifty50FnOWithConfig creates a strategy with custom configuration.
func NewNifty50FnOWithConfig(qty int64, cfg Nifty50FnOConfig) *Nifty50FnO {
	return &Nifty50FnO{
		instruments: make(map[string]*nifty50FnOState, 64),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty50FnO) Name() string {
	if s.cfg.NameOverride != "" {
		return s.cfg.NameOverride
	}
	return "NIFTY50_FNO"
}

func (s *Nifty50FnO) Config() Nifty50FnOConfig { return s.cfg }

// SetFNOTokens updates the FNO option tokens used for the dual-layer SL at runtime.
// Called by stratengine when strikes are dynamically resolved.
func (s *Nifty50FnO) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.FNOCallToken = callToken
	s.cfg.FNOPutToken = putToken
}

// SetFNOEntryPrice records the live FNO option premium at entry time so the
// FNO SL layer can start tracking from the correct baseline.
func (s *Nifty50FnO) SetFNOEntryPrice(price int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	indexKey := s.cfg.IndexToken
	if indexKey == "" {
		// In live mode we key by IndexToken; nothing to do without it.
		return
	}
	st := s.instruments[indexKey]
	if st == nil || st.Side == SideNone {
		return
	}
	st.FNOEntryPrice = price
	st.FNOBestPrice = price
}

// ApplySyntheticEntry syncs strategy state for an externally-orchestrated BUY
// signal (for example EXIT->BUY reverse expansion in stratengine).
func (s *Nifty50FnO) ApplySyntheticEntry(exchange, token string, side PositionSide, indexPrice int64) {
	if side == SideNone || indexPrice <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.cfg.IndexToken
	if key == "" {
		key = exchange + ":" + token
	}
	st := s.getOrCreateFnO(key)

	st.Side = side
	st.EntryPrice = indexPrice
	st.BestPrice = indexPrice
	st.IndexEntryPrice = indexPrice
	st.IndexBestPrice = indexPrice
	st.PendingEntry = SideNone
	st.PendingEMADiffEntry = SideNone
	st.InCooldown = false
	st.CandlesSinceExit = 0

	switch side {
	case SideCall:
		st.CallSetupActive = true
		st.PutSetupActive = false
	case SidePut:
		st.PutSetupActive = true
		st.CallSetupActive = false
	}
}

// CurrentFNOPosition returns the active position's FNO premium snapshot so
// stratengine can seed the live-orders table after a snapshot restore.
func (s *Nifty50FnO) CurrentFNOPosition() *LiveFNOPosition {
	s.mu.Lock()
	defer s.mu.Unlock()

	indexKey := s.cfg.IndexToken
	if indexKey == "" {
		return nil
	}
	st := s.instruments[indexKey]
	if st == nil || st.Side == SideNone || st.FNOEntryPrice == 0 {
		return nil
	}

	token := ""
	switch st.Side {
	case SideCall:
		token = s.cfg.FNOCallToken
	case SidePut:
		token = s.cfg.FNOPutToken
	}
	if token == "" {
		return nil
	}

	return &LiveFNOPosition{
		Side:       st.Side,
		Token:      token,
		EntryPrice: st.FNOEntryPrice,
		BestPrice:  st.FNOBestPrice,
	}
}

// EntryAutomationContext returns the current market context for the given
// instrument so the order layer can choose expiry and strike from live strategy
// state instead of parsing the signal reason string.
func (s *Nifty50FnO) EntryAutomationContext(exchange, token string) EntryAutomationContext {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := exchange + ":" + token
	st := s.instruments[key]
	if st == nil {
		return EntryAutomationContext{}
	}
	return st.entryAutomationContext(s.cfg)
}

// OnTFCandle evaluates on closed 1m candles.
func (s *Nifty50FnO) OnTFCandle(candle model.TFCandle) *Signal {
	if candle.TF != tf1m {
		return nil
	}

	// ── Only process index candle — ignore option candles ──
	if s.cfg.IndexToken != "" && candle.Key() != s.cfg.IndexToken {
		return nil
	}

	// ── Market hours guard: 9:16–15:29 IST ──
	// Feed indicators on all candles (so they stay warm), but only generate
	// signals during market hours. The 9:16 start skips the opening auction
	// candle; 15:29 gives 1 minute buffer before EOD auto-exit at 15:30.
	ist, _ := time.LoadLocation("Asia/Kolkata")
	candleIST := candle.TS.In(ist)
	marketOpen := candleIST.Hour() > 9 || (candleIST.Hour() == 9 && candleIST.Minute() >= 16)
	marketClose := candleIST.Hour() < 15 || (candleIST.Hour() == 15 && candleIST.Minute() <= 29)
	inMarketHours := marketOpen && marketClose
	minutesSinceOpen := (candleIST.Hour()-9)*60 + candleIST.Minute() - 15
	minutesUntilClose := (15*60 + 30) - (candleIST.Hour()*60 + candleIST.Minute())
	inEntryWindow := minutesSinceOpen >= s.cfg.SkipFirstMinutes && minutesUntilClose >= s.cfg.SkipLastMinutes

	s.mu.Lock()
	defer s.mu.Unlock()

	key := candle.Key()
	st := s.getOrCreateFnO(key)

	// ── Dedup: enforce monotonic candle close timestamp ──
	closeTS := candle.TS
	if !st.LastCloseTS.IsZero() && !closeTS.After(st.LastCloseTS) {
		return nil
	}
	st.LastCloseTS = closeTS

	// ── Track cooldown ──
	justExitedCooldown := false
	if st.InCooldown {
		st.CandlesSinceExit++
		if s.cfg.CooldownCandles > 0 && st.CandlesSinceExit >= s.cfg.CooldownCandles {
			st.InCooldown = false
			st.CandlesSinceExit = 0
			justExitedCooldown = true
		}
	}

	// ── Feed indicators ──
	entryCandle := toCandle1m(candle)

	if st.BufferCount > 0 {
		latest, ok := st.latestBufferEntry()
		if ok && latest.Time.Day() != candle.TS.Day() {
			st.VWAP.Reset()
		}
	}

	st.EMA6.Update(entryCandle)
	st.EMA9.Update(entryCandle)
	st.SMA21.Update(entryCandle)
	st.VWAP.Update(entryCandle)
	st.ADX.Update(entryCandle)

	// ── Push to prefetch buffer ──
	if st.EMA6.Ready() && st.EMA9.Ready() && st.SMA21.Ready() {
		st.pushBuffer(nifty50FnOBufferEntry{
			Time:  candle.TS,
			Open:  float64(candle.Open),
			High:  float64(candle.High),
			Low:   float64(candle.Low),
			Close: float64(candle.Close),
			EMA6:  st.EMA6.Value(),
			EMA9:  st.EMA9.Value(),
			MA21:  st.SMA21.Value(),
		})
	}

	// Need at least 2 buffer entries for crossover detection
	if !st.bufferReady() {
		return nil
	}

	prev, curr := st.prevAndCurr()

	// ── Detect crossovers (10-candle buffer lookback) ──
	ema6CrossedAboveMA21 := st.ema6CrossedAboveMA21()
	ema9CrossedAboveMA21 := false // unused in FNO
	ema6CrossedBelowMA21 := st.ema6CrossedBelowMA21()
	ema9CrossedBelowMA21 := false // unused in FNO

	ema6CrossedAboveEMA9 := st.ema6CrossedAboveEMA9()
	ema6CrossedBelowEMA9 := st.ema6CrossedBelowEMA9()

	// ── Current positions of lines ──
	bothAboveMA21 := curr.EMA6 > curr.MA21 && curr.EMA9 > curr.MA21
	bothBelowMA21 := curr.EMA6 < curr.MA21 && curr.EMA9 < curr.MA21

	// ── Update Call setup ──
	// Activate: EMA6 crosses above SMA21 (single-line, same as nifty50_fno_sl)
	if curr.EMA6 > curr.MA21 {
		if !st.CallSetupActive {
			st.CallSetupActive = true
			log.Printf("[strategy] %s: %s CALL setup activated (EMA6=%.0f > MA21=%.0f)",
				s.Name(), key, curr.EMA6, curr.MA21)
		}
	} else {
		if st.CallSetupActive {
			st.CallSetupActive = false
			log.Printf("[strategy] %s: %s CALL setup deactivated (EMA6=%.0f <= MA21=%.0f)",
				s.Name(), key, curr.EMA6, curr.MA21)
		}
	}

	// ── Update Put setup ──
	// Activate: EMA6 crosses below SMA21 (single-line)
	if curr.EMA6 < curr.MA21 {
		if !st.PutSetupActive {
			st.PutSetupActive = true
			log.Printf("[strategy] %s: %s PUT setup activated (EMA6=%.0f < MA21=%.0f)",
				s.Name(), key, curr.EMA6, curr.MA21)
		}
	} else {
		if st.PutSetupActive {
			st.PutSetupActive = false
			log.Printf("[strategy] %s: %s PUT setup deactivated (EMA6=%.0f >= MA21=%.0f)",
				s.Name(), key, curr.EMA6, curr.MA21)
		}
	}

	// ── Try exit first (market hours only) ──
	if inMarketHours {
		if sig := s.evaluateExitFnO(candle, st, prev, curr, ema6CrossedBelowMA21, ema6CrossedAboveMA21); sig != nil {
			return sig
		}
	}

	// ── Try entry (market hours only) ──
	if inMarketHours && inEntryWindow {
		if sig := s.evaluateEntryFnO(candle, st, curr, candleIST,
			ema6CrossedAboveMA21, ema9CrossedAboveMA21, ema6CrossedBelowMA21, ema9CrossedBelowMA21,
			ema6CrossedAboveEMA9, ema6CrossedBelowEMA9,
			bothAboveMA21, bothBelowMA21, justExitedCooldown); sig != nil {
			return sig
		}
	}

	return nil
}

// OnTick checks FNO target profit and stop losses on every price update.
func (s *Nifty50FnO) OnTick(tick model.Tick) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	tickKey := tick.Exchange + ":" + tick.Token

	isIndex := (s.cfg.IndexToken != "" && tickKey == s.cfg.IndexToken)
	isFNOCall := (s.cfg.FNOCallToken != "" && tickKey == s.cfg.FNOCallToken)
	isFNOPut := (s.cfg.FNOPutToken != "" && tickKey == s.cfg.FNOPutToken)

	// Fallback for backtest mode
	if s.cfg.IndexToken == "" && !isFNOCall && !isFNOPut {
		isIndex = true
	}

	if !isIndex && !isFNOCall && !isFNOPut {
		return nil
	}

	indexKey := s.cfg.IndexToken
	if indexKey == "" {
		indexKey = tickKey
	}
	st := s.instruments[indexKey]
	if st == nil || st.Side == SideNone {
		return nil
	}

	// ── FNO CALL TICK ──
	if isFNOCall && st.Side == SideCall && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOTargetAndSL(tick, st); sig != nil {
			return sig
		}
	}

	// ── FNO PUT TICK ──
	if isFNOPut && st.Side == SidePut && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOTargetAndSL(tick, st); sig != nil {
			return sig
		}
	}

	return nil
}

// checkFNOTargetAndSL checks FNO Target Profit, Trail SL, and Hard SL.
// Both CALL and PUT are BUY orders: premium UP = profit, premium DOWN = loss.
func (s *Nifty50FnO) checkFNOTargetAndSL(tick model.Tick, st *nifty50FnOState) *Signal {
	entryF := float64(st.FNOEntryPrice)
	priceF := float64(tick.Price)

	// Update FNO BestPrice — always track highest (we BUY both CALL and PUT)
	if tick.Price > st.FNOBestPrice {
		st.FNOBestPrice = tick.Price
	}

	// ── FNO Dynamic Target Profit (1.5% of entry price) ──
	// Exit when FNO premium gains the configured % of entry price.
	if s.cfg.FNOTargetProfitPct > 0 {
		profitPct := (priceF - entryF) / entryF * 100
		if profitPct >= s.cfg.FNOTargetProfitPct {
			reason := fmt.Sprintf("FNO TARGET PROFIT %s: fno_price=%d gained %.2f%% above fno_entry=%d (target=%.2f%%)",
				st.Side, tick.Price, profitPct, st.FNOEntryPrice, s.cfg.FNOTargetProfitPct)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			st.SLHitSide = slSide
			s.resetPositionFnO(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
				Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	// ── FNO Trailing SL ──
	if s.cfg.FNOTrailSLPct > 0 {
		bestF := float64(st.FNOBestPrice)
		profit := (bestF - entryF) / entryF * 100
		if profit >= s.cfg.FNOTrailStartPct {
			drop := (bestF - priceF) / bestF * 100
			if drop >= s.cfg.FNOTrailSLPct {
				reason := fmt.Sprintf("FNO TRAIL SL %s: fno_price=%d fell %.2f%% below fno_best=%d (trail=%.2f%%, fno_entry=%d)",
					st.Side, tick.Price, drop, st.FNOBestPrice, s.cfg.FNOTrailSLPct, st.FNOEntryPrice)
				log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
				slSide := st.Side
				st.SLHitSide = slSide
				s.resetPositionFnO(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	// ── FNO Hard SL ──
	if s.cfg.FNOHardSLPct > 0 {
		drop := (entryF - priceF) / entryF * 100
		if drop >= s.cfg.FNOHardSLPct {
			reason := fmt.Sprintf("FNO HARD SL %s: fno_price=%d fell %.2f%% below fno_entry=%d (SL=%.2f%%)",
				st.Side, tick.Price, drop, st.FNOEntryPrice, s.cfg.FNOHardSLPct)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			st.SLHitSide = slSide
			s.resetPositionFnO(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
				Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	// ── FNO Fixed Stop Loss (paise below entry) ──
	// Mirrors the GTT STOPLOSS rule placed at the broker so the system
	// fires an exit even if the GTT rule is missed (e.g. session expired,
	// network blip). Triggers when FNO LTP touches entry - StopLossPaise.
	if s.cfg.FNOStopLossPaise > 0 {
		slTrigger := st.FNOEntryPrice - s.cfg.FNOStopLossPaise
		if slTrigger > 0 && tick.Price <= slTrigger {
			reason := fmt.Sprintf("FNO FIXED SL %s: fno_price=%d at/below trigger=%d (fno_entry=%d, sl_pts=%d)",
				st.Side, tick.Price, slTrigger, st.FNOEntryPrice, s.cfg.FNOStopLossPaise/100)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			st.SLHitSide = slSide
			s.resetPositionFnO(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
				Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	return nil
}

// ── Exit Logic ──

func (s *Nifty50FnO) evaluateExitFnO(
	candle model.TFCandle, st *nifty50FnOState,
	prev, curr nifty50FnOBufferEntry,
	ema6CrossedBelowMA21, ema6CrossedAboveMA21 bool,
) *Signal {
	_ = prev

	if st.Side == SideNone {
		return nil
	}

	// Hard SL + trailing SL exits are handled in OnTick.
	// Candle-close exit is only used for deterministic opposite-side reversal.
	if st.Side == SideCall && ema6CrossedBelowMA21 {
		reason := fmt.Sprintf("1m CALL exit: EMA6(%.0f) crossed below SMA21(%.0f), reversing to PUT, close=%d",
			curr.EMA6, curr.MA21, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		st.SLHitSide = SideCall // mark for crossover consumption
		s.resetPositionFnO(st)
		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Side:         SideCall,
			ReverseTo:    SidePut,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
		}
	}

	if st.Side == SidePut && ema6CrossedAboveMA21 {
		reason := fmt.Sprintf("1m PUT exit: EMA6(%.0f) crossed above SMA21(%.0f), reversing to CALL, close=%d",
			curr.EMA6, curr.MA21, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		st.SLHitSide = SidePut // mark for crossover consumption
		s.resetPositionFnO(st)
		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Side:         SidePut,
			ReverseTo:    SideCall,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
		}
	}

	return nil
}

// ── Entry Logic ──

func (s *Nifty50FnO) evaluateEntryFnO(
	candle model.TFCandle, st *nifty50FnOState,
	curr nifty50FnOBufferEntry,
	candleIST time.Time,
	ema6CrossedAboveMA21, ema9CrossedAboveMA21 bool,
	ema6CrossedBelowMA21, ema9CrossedBelowMA21 bool,
	ema6CrossedAboveEMA9, ema6CrossedBelowEMA9 bool,
	bothAboveMA21, bothBelowMA21 bool,
	justExitedCooldown bool,
) *Signal {
	_ = ema9CrossedAboveMA21
	_ = ema9CrossedBelowMA21
	_ = ema6CrossedAboveEMA9
	_ = ema6CrossedBelowEMA9
	_ = bothAboveMA21
	_ = bothBelowMA21

	// Cannot enter if already in a position (mutual exclusion)
	if st.Side != SideNone {
		return nil
	}

	if s.cfg.AdxThreshold > 0 && st.ADX.Ready() {
		if st.ADX.Value() < float64(s.cfg.AdxThreshold) {
			return nil
		}
	}

	vwapOkForCall := true
	vwapOkForPut := true
	if s.cfg.VwapEnabled && st.VWAP.Ready() {
		vwap := st.VWAP.Value()
		vwapOkForCall = float64(candle.Close) > vwap
		vwapOkForPut = float64(candle.Close) < vwap
	}

	ema6AboveMA21 := curr.EMA6 > curr.MA21
	ema6BelowMA21 := curr.EMA6 < curr.MA21

	// ── Queue crossover triggers during cooldown ──
	if st.InCooldown {
		if st.PutSetupActive && ema6BelowMA21 && ema6CrossedBelowMA21 {
			st.PendingEntry = SidePut
			log.Printf("[strategy] %s: %s PUT crossover queued during cooldown", s.Name(), candle.Key())
		}
		if st.CallSetupActive && ema6AboveMA21 && ema6CrossedAboveMA21 {
			st.PendingEntry = SideCall
			log.Printf("[strategy] %s: %s CALL crossover queued during cooldown", s.Name(), candle.Key())
		}
		return nil
	}

	// ── Execute pending entry from cooldown ──
	if justExitedCooldown && st.PendingEntry != SideNone {
		pending := st.PendingEntry
		st.PendingEntry = SideNone

		if pending == SidePut && st.PutSetupActive && ema6BelowMA21 && vwapOkForPut {
			st.Side = SidePut
			st.EntryPrice = candle.Close
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.BestPrice = candle.Close
			reason := fmt.Sprintf("1m ENTRY PUT (pending cooldown): EMA6(%.0f) < SMA21(%.0f), close=%d",
				curr.EMA6, curr.MA21, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionBuy,
				Side:         SidePut,
				Token:        candle.Token,
				Exchange:     candle.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
		if pending == SideCall && st.CallSetupActive && ema6AboveMA21 && vwapOkForCall {
			st.Side = SideCall
			st.EntryPrice = candle.Close
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.BestPrice = candle.Close
			reason := fmt.Sprintf("1m ENTRY CALL (pending cooldown): EMA6(%.0f) > SMA21(%.0f), close=%d",
				curr.EMA6, curr.MA21, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionBuy,
				Side:         SideCall,
				Token:        candle.Token,
				Exchange:     candle.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
		log.Printf("[strategy] %s: %s pending %s entry expired (conditions no longer valid)",
			s.Name(), candle.Key(), pending)
	}

	// EMA9-based defer/re-entry flow intentionally disabled for this strategy.
	st.PendingEMADiffEntry = SideNone

	// ── Sideways market filter ──
	if s.cfg.SidewaysEnabled {
		sw := st.isSideways(s.cfg, candleIST)

		// Momentum bypass: if current close is far from MA21, market has clearly
		// broken out — force entry directly, bypassing sideways AND setup checks.
		if s.cfg.MomentumBypassPct > 0 && curr.MA21 > 0 {
			closeF := float64(candle.Close)
			dist := closeF - curr.MA21
			absDist := dist
			if absDist < 0 {
				absDist = -absDist
			}
			pct := absDist / curr.MA21 * 100
			if pct > s.cfg.MomentumBypassPct {
				if sw.Sideways && sw.ChopFired && classifyEntryStrength(pct, s.cfg.MomentumBypassPct) != EntryStrengthHigh {
					log.Printf("[strategy] %s: %s CHOPPY — skipping momentum bypass (momentum=%.2f%%, need high momentum >= %.2f%%)",
						s.Name(), candle.Key(), pct, s.cfg.MomentumBypassPct*2)
					return nil
				}

				// Close is far from MA21 → forced entry
				if dist < 0 {
					if !vwapOkForPut {
						return nil
					}
					// Close below MA21 → forced PUT
					st.Side = SidePut
					st.EntryPrice = candle.Close
					st.IndexEntryPrice = candle.Close
					st.IndexBestPrice = candle.Close
					st.BestPrice = candle.Close
					st.PutSetupActive = true
					reason := fmt.Sprintf("1m ENTRY PUT (momentum bypass): close=%d is %.2f%% below SMA21(%.0f), EMA6=%.0f",
						candle.Close, pct, curr.MA21, curr.EMA6)
					log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
					return &Signal{
						StrategyName: s.Name(),
						Action:       ActionBuy,
						Side:         SidePut,
						Token:        candle.Token,
						Exchange:     candle.Exchange,
						Qty:          s.qty,
						Reason:       reason,
					}
				}
				if !vwapOkForCall {
					return nil
				}
				// Close above MA21 → forced CALL
				st.Side = SideCall
				st.EntryPrice = candle.Close
				st.IndexEntryPrice = candle.Close
				st.IndexBestPrice = candle.Close
				st.BestPrice = candle.Close
				st.CallSetupActive = true
				reason := fmt.Sprintf("1m ENTRY CALL (momentum bypass): close=%d is %.2f%% above SMA21(%.0f), EMA6=%.0f",
					candle.Close, pct, curr.MA21, curr.EMA6)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{
					StrategyName: s.Name(),
					Action:       ActionBuy,
					Side:         SideCall,
					Token:        candle.Token,
					Exchange:     candle.Exchange,
					Qty:          s.qty,
					Reason:       reason,
				}
			}
		}

		if sw.Sideways {
			log.Printf("[strategy] %s: %s SIDEWAYS — skipping entry (chop=%v flat=%v range=%v)",
				s.Name(), candle.Key(), sw.ChopFired, sw.FlatFired, sw.RangeFired)
			return nil
		}
	}

	// ── Trend direction filter ──
	trend := st.trendBias(s.cfg)

	// ── CALL Entry ──
	if st.CallSetupActive && ema6AboveMA21 && vwapOkForCall {
		// Block CALL in bearish trend
		if trend == TrendBearish {
			log.Printf("[strategy] %s: %s CALL blocked — bearish trend (MA21 falling)", s.Name(), candle.Key())
			return nil
		}
		if ema6CrossedAboveMA21 {
			// ── Separation guard: block if EMA6 is too close to MA21 ──
			if s.cfg.MinReEntrySeparationPct > 0 {
				sep := (curr.EMA6 - curr.MA21) / curr.MA21 * 100 // % above SMA21
				if sep < s.cfg.MinReEntrySeparationPct {
					log.Printf("[strategy] %s: %s CALL entry SKIPPED — EMA6 only %.3f%% above SMA21 (min %.3f%%)",
						s.Name(), candle.Key(), sep, s.cfg.MinReEntrySeparationPct)
					return nil
				}
			}

			st.Side = SideCall
			st.EntryPrice = candle.Close
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.BestPrice = candle.Close

			reason := fmt.Sprintf("1m ENTRY CALL: EMA6(%.0f) crossed above SMA21(%.0f), close=%d",
				curr.EMA6, curr.MA21, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)

			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionBuy,
				Side:         SideCall,
				Token:        candle.Token,
				Exchange:     candle.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
	}

	// ── PUT Entry ──
	if st.PutSetupActive && ema6BelowMA21 && vwapOkForPut {
		// Block PUT in bullish trend
		if trend == TrendBullish {
			log.Printf("[strategy] %s: %s PUT blocked — bullish trend (MA21 rising)", s.Name(), candle.Key())
			return nil
		}
		if ema6CrossedBelowMA21 {
			// ── Separation guard: block if EMA6 is too close to MA21 ──
			if s.cfg.MinReEntrySeparationPct > 0 {
				sep := (curr.MA21 - curr.EMA6) / curr.MA21 * 100 // % below SMA21
				if sep < s.cfg.MinReEntrySeparationPct {
					log.Printf("[strategy] %s: %s PUT entry SKIPPED — EMA6 only %.3f%% below SMA21 (min %.3f%%)",
						s.Name(), candle.Key(), sep, s.cfg.MinReEntrySeparationPct)
					return nil
				}
			}

			st.Side = SidePut
			st.EntryPrice = candle.Close
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.BestPrice = candle.Close

			reason := fmt.Sprintf("1m ENTRY PUT: EMA6(%.0f) crossed below SMA21(%.0f), close=%d",
				curr.EMA6, curr.MA21, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)

			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionBuy,
				Side:         SidePut,
				Token:        candle.Token,
				Exchange:     candle.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
	}

	return nil
}

// ── Helpers ──

func (s *Nifty50FnO) getOrCreateFnO(key string) *nifty50FnOState {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty50FnOState(s.cfg)
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50FnO) resetPositionFnO(st *nifty50FnOState) {
	// Mark crossover as consumed on exit so lookback won't re-trigger
	if st.SLHitSide == SideCall || st.SLHitSide == SidePut {
		st.CrossConsumedAtBufIdx = (st.BufferIdx - 1 + len(st.Buffer)) % len(st.Buffer)
	}
	st.SLHitSide = SideNone
	st.Side = SideNone
	st.EntryPrice = 0
	st.BestPrice = 0
	st.IndexEntryPrice = 0
	st.IndexBestPrice = 0
	st.FNOEntryPrice = 0
	st.FNOBestPrice = 0
	st.PendingEntry = SideNone
	if s.cfg.CooldownCandles > 0 {
		st.InCooldown = true
		st.CandlesSinceExit = 0
	}
}

// ResetPositions resets position state while keeping indicators warm.
func (s *Nifty50FnO) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		st.Side = SideNone
		st.EntryPrice = 0
		st.IndexEntryPrice = 0
		st.IndexBestPrice = 0
		st.FNOEntryPrice = 0
		st.FNOBestPrice = 0
		st.InCooldown = false
		st.CandlesSinceExit = 0
		st.CallSetupActive = false
		st.PutSetupActive = false
		st.CrossConsumedAtBufIdx = -1
		// NOTE: indicators and buffer are NOT reset — they stay warm
	}
}

// ForceExitAll fires an ActionExit signal for every open CALL or PUT position
// and then resets the state. Used for EOD auto-exit and stale-position cleanup.
// reason is logged in the signal's Reason field (e.g. "EOD auto-exit").
func (s *Nifty50FnO) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sigs []Signal
	for key, st := range s.instruments {
		if st.Side == SideNone {
			continue
		}
		// Parse exchange:token from key
		exch, token := "", key
		for i, c := range key {
			if c == ':' {
				exch = key[:i]
				token = key[i+1:]
				break
			}
		}
		side := st.Side
		log.Printf("[strategy] %s: ForceExitAll %s %s:%s — %s", s.Name(), side, exch, token, reason)
		st.SLHitSide = side // Mark for crossover consumption
		s.resetPositionFnO(st)
		sigs = append(sigs, Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Side:         side,
			Token:        token,
			Exchange:     exch,
			Qty:          s.qty,
			Reason:       reason,
		})
	}
	return sigs
}

// Snapshot serializes all strategy state for crash recovery.
func (s *Nifty50FnO) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty50FnOSnapshot(s.Name(), s.instruments)
}

// Restore recovers strategy state from a snapshot.
func (s *Nifty50FnO) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := unmarshalNifty50FnOSnapshot(data)
	if err != nil {
		return err
	}
	s.instruments = restoreNifty50FnOInstruments(snap)
	return nil
}

// toCandle1m converts a TFCandle to a plain Candle for indicator updates.
func toCandle1m(tfc model.TFCandle) model.Candle {
	return model.Candle{
		Token:    tfc.Token,
		Exchange: tfc.Exchange,
		Open:     tfc.Open,
		High:     tfc.High,
		Low:      tfc.Low,
		Close:    tfc.Close,
		Volume:   tfc.Volume,
		TS:       tfc.TS,
	}
}

// Nifty50FnOIndicatorSnapshot holds the current indicator values for Redis publishing.
type Nifty50FnOIndicatorSnapshot struct {
	Strategy string  `json:"strategy"`
	EMA6     float64 `json:"ema6"`
	EMA9     float64 `json:"ema9"`
	SMA21    float64 `json:"sma21"`
	VWAP     float64 `json:"vwap"`
	ADX      float64 `json:"adx"`

	EMA6Ready  bool `json:"ema6_ready"`
	EMA9Ready  bool `json:"ema9_ready"`
	SMA21Ready bool `json:"sma21_ready"`
	VWAPReady  bool `json:"vwap_ready"`
	ADXReady   bool `json:"adx_ready"`

	Side            PositionSide `json:"side"`
	CallSetupActive bool         `json:"call_setup_active"`
	PutSetupActive  bool         `json:"put_setup_active"`
	EntryPrice      int64        `json:"entry_price"`
	IndexEntryPrice int64        `json:"index_entry_price"`
}

// IndicatorSnapshot returns the current indicator and position state for Redis publishing.
func (s *Nifty50FnO) IndicatorSnapshot() *Nifty50FnOIndicatorSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	indexKey := s.cfg.IndexToken
	if indexKey == "" {
		// Backtest / single instrument: pick the first available key
		for k := range s.instruments {
			indexKey = k
			break
		}
	}
	if indexKey == "" {
		return nil
	}

	st := s.instruments[indexKey]
	if st == nil {
		return nil
	}

	snap := &Nifty50FnOIndicatorSnapshot{
		Strategy:        s.Name(),
		Side:            st.Side,
		CallSetupActive: st.CallSetupActive,
		PutSetupActive:  st.PutSetupActive,
		EntryPrice:      st.EntryPrice,
		IndexEntryPrice: st.IndexEntryPrice,
	}

	if st.EMA6.Ready() {
		snap.EMA6 = st.EMA6.Value()
		snap.EMA6Ready = true
	}
	if st.EMA9.Ready() {
		snap.EMA9 = st.EMA9.Value()
		snap.EMA9Ready = true
	}
	if st.SMA21.Ready() {
		snap.SMA21 = st.SMA21.Value()
		snap.SMA21Ready = true
	}
	if st.VWAP.Ready() {
		snap.VWAP = st.VWAP.Value()
		snap.VWAPReady = true
	}
	if st.ADX.Ready() {
		snap.ADX = st.ADX.Value()
		snap.ADXReady = true
	}

	return snap
}
