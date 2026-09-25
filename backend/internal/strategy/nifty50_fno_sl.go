package strategy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 FnO SL — EMA6 × EMA9 + FNO-only SL
//
//  Entry:  EMA6 × EMA9 crossover (10-candle buffer lookback)
//            EMA6 > EMA9 (EMA6 crossed above EMA9) → BUY CALL
//            EMA9 > EMA6 (EMA6 crossed below EMA9) → BUY PUT
//  Exit:   Opposite crossover on candle close
//            CALL → exit when EMA6 crosses below EMA9
//            PUT  → exit when EMA6 crosses above EMA9
//  SL:     FNO Trail SL + FNO Hard SL only (index SL disabled)
//  Filter: Momentum bypass at 0.3%; EMA diff guard at 0.1%;
//          all other filters disabled.
// ════════════════════════════════════════════════════════════════════

// Nifty50FnOSL implements the NIFTY50 FNO strategy with EMA6/EMA9 crossovers
// and dual FNO stop-loss layers.
type Nifty50FnOSL struct {
	mu                  sync.Mutex
	instruments         map[string]*nifty50FnOSLState
	qty                 int64
	cfg                 Nifty50FnOSLConfig
	marketStateProvider func() EntryMarketState // injected by service layer
}

// NewNifty50FnOSL creates a new strategy with default config.
func NewNifty50FnOSL(qty int64) *Nifty50FnOSL {
	return NewNifty50FnOSLWithConfig(qty, DefaultNifty50FnOSLConfig())
}

// NewNifty50FnOSLWithConfig creates a strategy with custom configuration.
func NewNifty50FnOSLWithConfig(qty int64, cfg Nifty50FnOSLConfig) *Nifty50FnOSL {
	return &Nifty50FnOSL{
		instruments: make(map[string]*nifty50FnOSLState, 64),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty50FnOSL) Name() string               { return "NIFTY50_FNO_SL" }
func (s *Nifty50FnOSL) Config() Nifty50FnOSLConfig { return s.cfg }

// SetMarketStateProvider injects the external market state source.
// The provider should return the current EntryMarketState from
// the primary NIFTY50_FNO strategy's EntryAutomationContext.
func (s *Nifty50FnOSL) SetMarketStateProvider(fn func() EntryMarketState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.marketStateProvider = fn
}

// SetFNOTokens updates the FNO option tokens at runtime.
func (s *Nifty50FnOSL) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.FNOCallToken = callToken
	s.cfg.FNOPutToken = putToken
}

// SetResistanceLevels updates resistance levels at runtime.
func (s *Nifty50FnOSL) SetResistanceLevels(levels []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.ResistanceLevels = levels
}

// SetSupportLevels updates support levels at runtime.
func (s *Nifty50FnOSL) SetSupportLevels(levels []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.SupportLevels = levels
}

// CurrentFNOPosition returns the active FnO premium state for the current position.
func (s *Nifty50FnOSL) CurrentFNOPosition() *LiveFNOPosition {
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

// OnTFCandle evaluates on closed 1m candles.
func (s *Nifty50FnOSL) OnTFCandle(candle model.TFCandle) *Signal {
	if candle.TF != tf1m {
		return nil
	}

	// Only process index candle
	if s.cfg.IndexToken != "" && candle.Key() != s.cfg.IndexToken {
		return nil
	}

	// Market hours guard: 9:16–15:29 IST
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
	st := s.getOrCreate(key)

	// Dedup: enforce monotonic candle close timestamp
	closeTS := candle.TS
	if !st.LastCloseTS.IsZero() && !closeTS.After(st.LastCloseTS) {
		return nil
	}
	st.LastCloseTS = closeTS

	// ── Fresh-day cross guard ──
	// On new trading day, invalidate previous-day crossovers so the strategy
	// waits for a fresh intraday EMA6×EMA9 cross.
	candleDate := candleIST.Truncate(24 * time.Hour)
	if !st.LastTradingDate.IsZero() && !candleDate.Equal(st.LastTradingDate) {
		if st.BufferCount > 0 {
			st.CrossConsumedAtBufIdx = (st.BufferIdx - 1 + len(st.Buffer)) % len(st.Buffer)
		}
		log.Printf("[strategy] %s: %s FRESH_DAY | new trading day %s detected — previous crosses invalidated",
			s.Name(), key, candleDate.Format("2006-01-02"))
	}
	st.LastTradingDate = candleDate

	// Track cooldown
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
	st.EMA6.Update(entryCandle)
	st.EMA9.Update(entryCandle)

	// ── Push to buffer ──
	if st.EMA6.Ready() && st.EMA9.Ready() {
		st.pushBuffer(nifty50FnOSLBufferEntry{
			Time:  candle.TS,
			Open:  float64(candle.Open),
			High:  float64(candle.High),
			Low:   float64(candle.Low),
			Close: float64(candle.Close),
			EMA6:  st.EMA6.Value(),
			EMA9:  st.EMA9.Value(),
		})
	}

	if !st.bufferReady() {
		return nil
	}

	prev, curr := st.prevAndCurr()

	// ── Detect crossovers (10-candle buffer lookback) ──
	ema6CrossedAboveEMA9 := st.ema6CrossedAboveEMA9() // EMA6 > EMA9 → CALL entry / PUT exit
	ema6CrossedBelowEMA9 := st.ema6CrossedBelowEMA9() // EMA6 < EMA9 → PUT entry / CALL exit

	// ── Update Call setup (EMA6 > EMA9) ──
	if curr.EMA6 > curr.EMA9 {
		if !st.CallSetupActive {
			st.CallSetupActive = true
			log.Printf("[strategy] %s: %s CALL setup activated (EMA6=%.2f > EMA9=%.2f)",
				s.Name(), key, curr.EMA6, curr.EMA9)
		}
	} else {
		if st.CallSetupActive {
			st.CallSetupActive = false
			log.Printf("[strategy] %s: %s CALL setup deactivated (EMA6=%.2f <= EMA9=%.2f)",
				s.Name(), key, curr.EMA6, curr.EMA9)
		}
	}

	// ── Update Put setup (EMA9 > EMA6) ──
	if curr.EMA9 > curr.EMA6 {
		if !st.PutSetupActive {
			st.PutSetupActive = true
			log.Printf("[strategy] %s: %s PUT setup activated (EMA9=%.2f > EMA6=%.2f)",
				s.Name(), key, curr.EMA9, curr.EMA6)
		}
	} else {
		if st.PutSetupActive {
			st.PutSetupActive = false
			log.Printf("[strategy] %s: %s PUT setup deactivated (EMA9=%.2f <= EMA6=%.2f)",
				s.Name(), key, curr.EMA9, curr.EMA6)
		}
	}

	// ── Try exit first (market hours only) ──
	if inMarketHours {
		if sig := s.evaluateExitSL(candle, st, prev, curr, ema6CrossedBelowEMA9, ema6CrossedAboveEMA9); sig != nil {
			return sig
		}
	}

	// ── Try entry (market hours only) ──
	if inMarketHours && inEntryWindow {
		if sig := s.evaluateEntrySL(candle, st, curr, candleIST,
			ema6CrossedAboveEMA9, ema6CrossedBelowEMA9,
			justExitedCooldown); sig != nil {

			// Extract and log dynamic targets for smart SR exit
			closeF := float64(candle.Close)
			if st.Side == SideCall {
				st.DynamicIndexTarget = nearestResistance(closeF, s.cfg.ResistanceLevels, 500)
				if st.DynamicIndexTarget > 0 {
					distPts := st.DynamicIndexTarget - closeF
					distPct := distPts / closeF * 100
					log.Printf("[strategy] %s: %s SR_TARGET_SET CALL | entry_close=%d | resistance_target=%.2f | distance=%.0f pts (%.2f%%)",
						s.Name(), key, candle.Close, st.DynamicIndexTarget, distPts, distPct)
				} else {
					log.Printf("[strategy] %s: %s SR_TARGET_SKIP CALL | entry_close=%d | no valid resistance found (requiring min 5 pts buffer)",
						s.Name(), key, candle.Close)
				}
			} else if st.Side == SidePut {
				st.DynamicIndexTarget = nearestSupport(closeF, s.cfg.SupportLevels, 500)
				if st.DynamicIndexTarget > 0 {
					distPts := closeF - st.DynamicIndexTarget
					distPct := distPts / closeF * 100
					log.Printf("[strategy] %s: %s SR_TARGET_SET PUT | entry_close=%d | support_target=%.2f | distance=%.0f pts (%.2f%%)",
						s.Name(), key, candle.Close, st.DynamicIndexTarget, distPts, distPct)
				} else {
					log.Printf("[strategy] %s: %s SR_TARGET_SKIP PUT | entry_close=%d | no valid support found (requiring min 5 pts buffer)",
						s.Name(), key, candle.Close)
				}
			}

			return sig
		}
	}

	return nil
}

// OnTick implements FNO-only stop loss checking on every tick.
func (s *Nifty50FnOSL) OnTick(tick model.Tick) *Signal {
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

	// ── INDEX TICK: index SL disabled — kept for future use ──
	if isIndex && st.IndexEntryPrice != 0 {
		if sig := s.checkIndexSL(tick, st); sig != nil {
			return sig
		}
	}

	// ── FNO CALL TICK ──
	if isFNOCall && st.Side == SideCall && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOSL(tick, st); sig != nil {
			return sig
		}
	}

	// ── FNO PUT TICK ──
	if isFNOPut && st.Side == SidePut && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOSL(tick, st); sig != nil {
			return sig
		}
	}

	return nil
}

// checkIndexSL checks index-based SL (disabled by default — IndexHardSLPct/IndexTrailSLPct = 0).
func (s *Nifty50FnOSL) checkIndexSL(tick model.Tick, st *nifty50FnOSLState) *Signal {
	entryF := float64(st.IndexEntryPrice)
	priceF := float64(tick.Price)

	switch st.Side {
	case SideCall:
		if tick.Price > st.IndexBestPrice {
			st.IndexBestPrice = tick.Price
		}
	case SidePut:
		if st.IndexBestPrice == 0 || tick.Price < st.IndexBestPrice {
			st.IndexBestPrice = tick.Price
		}
	}

	// ── Index Trailing SL ──
	if s.cfg.IndexTrailSLPct > 0 {
		bestF := float64(st.IndexBestPrice)
		switch st.Side {
		case SideCall:
			profit := (bestF - entryF) / entryF * 100
			if profit >= s.cfg.IndexTrailStartPct {
				drop := (bestF - priceF) / bestF * 100
				if drop >= s.cfg.IndexTrailSLPct {
					reason := fmt.Sprintf("INDEX TRAIL SL CALL: idx_price=%d fell %.2f%% below idx_best=%d (trail=%.2f%%, idx_entry=%d)",
						tick.Price, drop, st.IndexBestPrice, s.cfg.IndexTrailSLPct, st.IndexEntryPrice)
					log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
					slSide := st.Side
					st.SLHitSide = slSide
					s.resetPositionSL(st)
					return &Signal{
						StrategyName: s.Name(),
						Action:       ActionExit,
						Side:         slSide,
						Token:        tick.Token,
						Exchange:     tick.Exchange,
						Qty:          s.qty,
						Reason:       reason,
					}
				}
			}
		case SidePut:
			profit := (entryF - bestF) / entryF * 100
			if profit >= s.cfg.IndexTrailStartPct {
				rise := (priceF - bestF) / bestF * 100
				if rise >= s.cfg.IndexTrailSLPct {
					reason := fmt.Sprintf("INDEX TRAIL SL PUT: idx_price=%d rose %.2f%% above idx_best=%d (trail=%.2f%%, idx_entry=%d)",
						tick.Price, rise, st.IndexBestPrice, s.cfg.IndexTrailSLPct, st.IndexEntryPrice)
					log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
					slSide := st.Side
					st.SLHitSide = slSide
					s.resetPositionSL(st)
					return &Signal{
						StrategyName: s.Name(),
						Action:       ActionExit,
						Side:         slSide,
						Token:        tick.Token,
						Exchange:     tick.Exchange,
						Qty:          s.qty,
						Reason:       reason,
					}
				}
			}
		}
	}

	// ── Index Hard SL ──
	if s.cfg.IndexHardSLPct > 0 {
		switch st.Side {
		case SideCall:
			drop := (entryF - priceF) / entryF * 100
			if drop >= s.cfg.IndexHardSLPct {
				reason := fmt.Sprintf("INDEX HARD SL CALL: idx_price=%d fell %.2f%% below idx_entry=%d (SL=%.2f%%)",
					tick.Price, drop, st.IndexEntryPrice, s.cfg.IndexHardSLPct)
				log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
				slSide := st.Side
				st.SLHitSide = slSide
				s.resetPositionSL(st)
				return &Signal{
					StrategyName: s.Name(),
					Action:       ActionExit,
					Side:         slSide,
					Token:        tick.Token,
					Exchange:     tick.Exchange,
					Qty:          s.qty,
					Reason:       reason,
				}
			}
		case SidePut:
			rise := (priceF - entryF) / entryF * 100
			if rise >= s.cfg.IndexHardSLPct {
				reason := fmt.Sprintf("INDEX HARD SL PUT: idx_price=%d rose %.2f%% above idx_entry=%d (SL=%.2f%%)",
					tick.Price, rise, st.IndexEntryPrice, s.cfg.IndexHardSLPct)
				log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
				slSide := st.Side
				st.SLHitSide = slSide
				s.resetPositionSL(st)
				return &Signal{
					StrategyName: s.Name(),
					Action:       ActionExit,
					Side:         slSide,
					Token:        tick.Token,
					Exchange:     tick.Exchange,
					Qty:          s.qty,
					Reason:       reason,
				}
			}
		}
	}

	return nil
}

// checkFNOSL checks Trail SL, Hard SL, and Target Profit on the FNO option premium.
// Both CALL and PUT are BUY orders, so premium UP = profit, premium DOWN = loss
// for both sides. The SL logic is identical for CALL and PUT.
func (s *Nifty50FnOSL) checkFNOSL(tick model.Tick, st *nifty50FnOSLState) *Signal {
	entryF := float64(st.FNOEntryPrice)
	priceF := float64(tick.Price)

	// Update FNO BestPrice — always track highest (we BUY both CALL and PUT)
	if tick.Price > st.FNOBestPrice {
		st.FNOBestPrice = tick.Price
	}

	// ── FNO Target Profit ──
	// Exit when FNO premium gains the target % (set at entry based on config).
	if s.cfg.FNOTargetProfitPct > 0 {
		profitPct := (priceF - entryF) / entryF * 100
		if profitPct >= s.cfg.FNOTargetProfitPct {
			reason := fmt.Sprintf("FNO TARGET PROFIT %s: fno_price=%d gained %.2f%% above fno_entry=%d (target=%.2f%%)",
				st.Side, tick.Price, profitPct, st.FNOEntryPrice, s.cfg.FNOTargetProfitPct)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			st.SLHitSide = slSide
			s.resetPositionSL(st)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionExit,
				Side:         slSide,
				Token:        tick.Token,
				Exchange:     tick.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
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
				s.resetPositionSL(st)
				return &Signal{
					StrategyName: s.Name(),
					Action:       ActionExit,
					Side:         slSide,
					Token:        tick.Token,
					Exchange:     tick.Exchange,
					Qty:          s.qty,
					Reason:       reason,
				}
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
			s.resetPositionSL(st)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionExit,
				Side:         slSide,
				Token:        tick.Token,
				Exchange:     tick.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
	}

	return nil
}

// ── Exit Logic ──

func (s *Nifty50FnOSL) evaluateExitSL(
	candle model.TFCandle, st *nifty50FnOSLState,
	prev, curr nifty50FnOSLBufferEntry,
	ema6CrossedBelowEMA9, ema6CrossedAboveEMA9 bool,
) *Signal {
	if st.Side == SideNone {
		return nil
	}

	// ── Dynamic SR Target (Smart Support/Resistance Action) ──
	if st.DynamicIndexTarget > 0 {
		closeF := float64(candle.Close)
		entryF := float64(st.IndexEntryPrice)

		if st.Side == SideCall && float64(candle.High) >= st.DynamicIndexTarget {
			// Reached resistance. Check for Bounce vs Breach
			if float64(candle.Close) <= st.DynamicIndexTarget {
				// Bounce (rejection) at Resistance -> Exit immediately
				pnlPts := closeF - entryF
				pnlPct := pnlPts / entryF * 100
				reason := fmt.Sprintf("SR_BOUNCE_EXIT CALL | resistance=%.2f | high=%d close=%d | entry=%d pnl=%.0f pts (%.2f%%)",
					st.DynamicIndexTarget, candle.High, candle.Close, st.IndexEntryPrice, pnlPts, pnlPct)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				s.resetPositionSL(st)
				return &Signal{
					StrategyName: s.Name(), Action: ActionExit, Side: SideCall,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason,
				}
			} else {
				// Breach (breakout) -> Hold for next target or max % profit
				oldTarget := st.DynamicIndexTarget
				st.DynamicIndexTarget = nearestResistance(closeF, s.cfg.ResistanceLevels, 0)
				if st.DynamicIndexTarget > 0 {
					log.Printf("[strategy] %s: %s SR_BREACH_HOLD CALL | broke resistance=%.2f | close=%d | next_resistance=%.2f | holding",
						s.Name(), candle.Key(), oldTarget, candle.Close, st.DynamicIndexTarget)
				} else {
					log.Printf("[strategy] %s: %s SR_BREACH_HOLD CALL | broke resistance=%.2f | close=%d | no more resistance levels | holding for max %%",
						s.Name(), candle.Key(), oldTarget, candle.Close)
				}
			}
		} else if st.Side == SidePut && float64(candle.Low) <= st.DynamicIndexTarget {
			// Reached support. Check for Bounce vs Breach
			if float64(candle.Close) >= st.DynamicIndexTarget {
				// Bounce (rejection) at Support -> Exit immediately
				pnlPts := entryF - closeF
				pnlPct := pnlPts / entryF * 100
				reason := fmt.Sprintf("SR_BOUNCE_EXIT PUT | support=%.2f | low=%d close=%d | entry=%d pnl=%.0f pts (%.2f%%)",
					st.DynamicIndexTarget, candle.Low, candle.Close, st.IndexEntryPrice, pnlPts, pnlPct)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				s.resetPositionSL(st)
				return &Signal{
					StrategyName: s.Name(), Action: ActionExit, Side: SidePut,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason,
				}
			} else {
				// Breach (breakdown) -> Hold for next target or max % profit
				oldTarget := st.DynamicIndexTarget
				st.DynamicIndexTarget = nearestSupport(closeF, s.cfg.SupportLevels, 0)
				if st.DynamicIndexTarget > 0 {
					log.Printf("[strategy] %s: %s SR_BREACH_HOLD PUT | broke support=%.2f | close=%d | next_support=%.2f | holding",
						s.Name(), candle.Key(), oldTarget, candle.Close, st.DynamicIndexTarget)
				} else {
					log.Printf("[strategy] %s: %s SR_BREACH_HOLD PUT | broke support=%.2f | close=%d | no more support levels | holding for max %%",
						s.Name(), candle.Key(), oldTarget, candle.Close)
				}
			}
		}
	}

	// ── CALL Exit: EMA6 crosses below EMA9 ──
	if st.Side == SideCall && ema6CrossedBelowEMA9 {
		reason := fmt.Sprintf("1m CALL exit: EMA6(%.2f) crossed below EMA9(%.2f), close=%d",
			curr.EMA6, curr.EMA9, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		s.resetPositionSL(st)
		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Side:         SideCall,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
		}
	}

	// ── PUT Exit: EMA6 crosses above EMA9 ──
	if st.Side == SidePut && ema6CrossedAboveEMA9 {
		reason := fmt.Sprintf("1m PUT exit: EMA6(%.2f) crossed above EMA9(%.2f), close=%d",
			curr.EMA6, curr.EMA9, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		s.resetPositionSL(st)
		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Side:         SidePut,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
		}
	}

	return nil
}

// ── Entry Logic ──

func (s *Nifty50FnOSL) evaluateEntrySL(
	candle model.TFCandle, st *nifty50FnOSLState,
	curr nifty50FnOSLBufferEntry,
	candleIST time.Time,
	ema6CrossedAboveEMA9, ema6CrossedBelowEMA9 bool,
	justExitedCooldown bool,
) *Signal {
	if st.Side != SideNone {
		return nil
	}

	// ── Queue crossover triggers during cooldown ──
	if st.InCooldown {
		if ema6CrossedBelowEMA9 {
			st.PendingEntry = SidePut
			log.Printf("[strategy] %s: %s PUT crossover queued during cooldown", s.Name(), candle.Key())
		}
		if ema6CrossedAboveEMA9 {
			st.PendingEntry = SideCall
			log.Printf("[strategy] %s: %s CALL crossover queued during cooldown", s.Name(), candle.Key())
		}
		return nil
	}

	// ── Execute pending entry from cooldown ──
	if justExitedCooldown && st.PendingEntry != SideNone {
		pending := st.PendingEntry
		st.PendingEntry = SideNone
		if pending == SidePut && curr.EMA9 > curr.EMA6 {
			st.Side = SidePut
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.CrossConsumedAtBufIdx = -1
			reason := fmt.Sprintf("1m PUT entry (pending): EMA9(%.2f) > EMA6(%.2f), close=%d",
				curr.EMA9, curr.EMA6, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
		if pending == SideCall && curr.EMA6 > curr.EMA9 {
			st.Side = SideCall
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.CrossConsumedAtBufIdx = -1
			reason := fmt.Sprintf("1m CALL entry (pending): EMA6(%.2f) > EMA9(%.2f), close=%d",
				curr.EMA6, curr.EMA9, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
		log.Printf("[strategy] %s: %s pending %s entry expired (conditions no longer valid)",
			s.Name(), candle.Key(), pending)
	}

	// ── Re-check pending EMA diff entries ──
	if st.PendingEMADiffEntry != SideNone && st.PendingEMADiffEntry != "" {
		pd := st.PendingEMADiffEntry
		conditionOK := (pd == SideCall && curr.EMA6 > curr.EMA9) ||
			(pd == SidePut && curr.EMA9 > curr.EMA6)
		if !conditionOK {
			log.Printf("[strategy] %s: %s pending %s EMA-diff entry expired (setup deactivated)",
				s.Name(), candle.Key(), pd)
			st.PendingEMADiffEntry = SideNone
		} else if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.EMA9 > 0 {
			diff := (curr.EMA6 - curr.EMA9) / curr.EMA9 * 100
			if diff < 0 {
				diff = -diff
			}
			if diff >= s.cfg.MinEMA6EMA9DiffPct {
				st.PendingEMADiffEntry = SideNone
				st.Side = pd
				st.IndexEntryPrice = candle.Close
				st.IndexBestPrice = candle.Close
				st.CrossConsumedAtBufIdx = -1
				reason := fmt.Sprintf("1m %s entry (deferred EMA-diff): EMA6(%.2f) EMA9(%.2f) diff=%.3f%%, close=%d",
					pd, curr.EMA6, curr.EMA9, diff, candle.Close)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: pd,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
			}
			log.Printf("[strategy] %s: %s pending %s EMA-diff still too small (%.3f%% < %.3f%%)",
				s.Name(), candle.Key(), pd, diff, s.cfg.MinEMA6EMA9DiffPct)
		}
	}

	// ── Momentum bypass ──
	// If EMA9 is used as the anchor baseline (replaces MA21)
	if s.cfg.MomentumBypassPct > 0 && curr.EMA9 > 0 {
		closeF := float64(candle.Close)
		dist := closeF - curr.EMA9
		absDist := dist
		if absDist < 0 {
			absDist = -absDist
		}
		pct := absDist / curr.EMA9 * 100
		if pct > s.cfg.MomentumBypassPct {
			if dist < 0 {
				st.Side = SidePut
				st.IndexEntryPrice = candle.Close
				st.IndexBestPrice = candle.Close
				st.CrossConsumedAtBufIdx = -1
				reason := fmt.Sprintf("1m PUT entry (momentum): close=%d is %.2f%% below EMA9(%.2f), EMA6=%.2f",
					candle.Close, pct, curr.EMA9, curr.EMA6)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
			}
			st.Side = SideCall
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.CrossConsumedAtBufIdx = -1
			reason := fmt.Sprintf("1m CALL entry (momentum): close=%d is %.2f%% above EMA9(%.2f), EMA6=%.2f",
				candle.Close, pct, curr.EMA9, curr.EMA6)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	// ── External market state filter (choppy/sideways from NIFTY50_FNO) ──
	if s.marketStateProvider != nil {
		ms := s.marketStateProvider()
		switch ms {
		case EntryMarketStateChoppy:
			log.Printf("[strategy] %s: %s CHOPPY_BLOCK | external market state = CHOPPY — skipping entry",
				s.Name(), candle.Key())
			return nil
		case EntryMarketStateSideways:
			log.Printf("[strategy] %s: %s SIDEWAYS_WARN | external market state = SIDEWAYS — allowing entry with caution",
				s.Name(), candle.Key())
		}
	} else if s.cfg.SidewaysEnabled {
		// Fallback: internal detection if no external provider
		sw := st.isSideways(s.cfg, candleIST)
		if sw.Sideways {
			log.Printf("[strategy] %s: %s SIDEWAYS — skipping entry (chop=%v flat=%v range=%v)",
				s.Name(), candle.Key(), sw.ChopFired, sw.FlatFired, sw.RangeFired)
			return nil
		}
	}

	// ── Trend direction filter ──
	trend := st.trendBias(s.cfg)

	// ── CALL Entry: EMA6 crosses above EMA9 ──
	if curr.EMA6 > curr.EMA9 && ema6CrossedAboveEMA9 {
		if trend == TrendBearish {
			log.Printf("[strategy] %s: %s CALL blocked — bearish trend", s.Name(), candle.Key())
			return nil
		}

		// ── EMA diff guard ──
		if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.EMA9 > 0 {
			diff := (curr.EMA6 - curr.EMA9) / curr.EMA9 * 100
			if diff < 0 {
				diff = -diff
			}
			if diff < s.cfg.MinEMA6EMA9DiffPct {
				log.Printf("[strategy] %s: %s CALL entry DEFERRED — EMA6-EMA9 diff only %.3f%% (min %.3f%%)",
					s.Name(), candle.Key(), diff, s.cfg.MinEMA6EMA9DiffPct)
				st.PendingEMADiffEntry = SideCall
				return nil
			}
		}
		// ── Narrow SR Box Block ──
		res := nearestResistance(float64(candle.Close), s.cfg.ResistanceLevels, 0)
		sup := nearestSupport(float64(candle.Close), s.cfg.SupportLevels, 0)
		if res > 0 && sup > 0 {
			boxSize := res - sup
			if boxSize <= 2000 {
				log.Printf("[strategy] %s: %s CALL blocked — trapped in narrow SR box (res=%.2f, sup=%.2f, diff=%.0f pts <= 20)",
					s.Name(), candle.Key(), res, sup, boxSize/100)
				return nil
			}
		}

		st.Side = SideCall
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		st.CrossConsumedAtBufIdx = -1
		reason := fmt.Sprintf("1m CALL entry: EMA6(%.2f) crossed above EMA9(%.2f), close=%d",
			curr.EMA6, curr.EMA9, candle.Close)
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

	// ── PUT Entry: EMA9 crosses above EMA6 (EMA6 crossed below EMA9) ──
	if curr.EMA9 > curr.EMA6 && ema6CrossedBelowEMA9 {
		if trend == TrendBullish {
			log.Printf("[strategy] %s: %s PUT blocked — bullish trend", s.Name(), candle.Key())
			return nil
		}

		// ── EMA diff guard ──
		if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.EMA9 > 0 {
			diff := (curr.EMA9 - curr.EMA6) / curr.EMA9 * 100
			if diff < 0 {
				diff = -diff
			}
			if diff < s.cfg.MinEMA6EMA9DiffPct {
				log.Printf("[strategy] %s: %s PUT entry DEFERRED — EMA6-EMA9 diff only %.3f%% (min %.3f%%)",
					s.Name(), candle.Key(), diff, s.cfg.MinEMA6EMA9DiffPct)
				st.PendingEMADiffEntry = SidePut
				return nil
			}
		}
		// ── Narrow SR Box Block ──
		res := nearestResistance(float64(candle.Close), s.cfg.ResistanceLevels, 0)
		sup := nearestSupport(float64(candle.Close), s.cfg.SupportLevels, 0)
		if res > 0 && sup > 0 {
			boxSize := res - sup
			if boxSize <= 2000 {
				log.Printf("[strategy] %s: %s PUT blocked — trapped in narrow SR box (res=%.2f, sup=%.2f, diff=%.0f pts <= 20)",
					s.Name(), candle.Key(), res, sup, boxSize/100)
				return nil
			}
		}

		st.Side = SidePut
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		st.CrossConsumedAtBufIdx = -1
		reason := fmt.Sprintf("1m PUT entry: EMA9(%.2f) crossed above EMA6(%.2f), close=%d",
			curr.EMA9, curr.EMA6, candle.Close)
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

	return nil
}

// ── Helpers ──

func (s *Nifty50FnOSL) getOrCreate(key string) *nifty50FnOSLState {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty50FnOSLState(s.cfg)
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50FnOSL) resetPositionSL(st *nifty50FnOSLState) {
	// Mark crossover as consumed on SL exit so lookback won't re-trigger
	if st.SLHitSide == SideCall || st.SLHitSide == SidePut {
		st.CrossConsumedAtBufIdx = (st.BufferIdx - 1 + len(st.Buffer)) % len(st.Buffer)
	}
	st.SLHitSide = SideNone // clear after use — only SL exits should consume
	st.Side = SideNone
	st.IndexEntryPrice = 0
	st.IndexBestPrice = 0
	st.FNOEntryPrice = 0
	st.FNOBestPrice = 0
	st.DynamicIndexTarget = 0
	st.PendingEntry = SideNone
	if s.cfg.CooldownCandles > 0 {
		st.InCooldown = true
		st.CandlesSinceExit = 0
	}
}

// SetFNOEntryPrice sets the FNO option entry price after entry signal is emitted.
func (s *Nifty50FnOSL) SetFNOEntryPrice(price int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	indexKey := s.cfg.IndexToken
	if indexKey == "" {
		return
	}
	st := s.instruments[indexKey]
	if st == nil || st.Side == SideNone {
		return
	}
	st.FNOEntryPrice = price
	st.FNOBestPrice = price
}

// ResetPositions resets position state while keeping indicators warm.
func (s *Nifty50FnOSL) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		st.Side = SideNone
		st.IndexEntryPrice = 0
		st.IndexBestPrice = 0
		st.FNOEntryPrice = 0
		st.FNOBestPrice = 0
		st.DynamicIndexTarget = 0
		st.InCooldown = false
		st.CandlesSinceExit = 0
		st.CallSetupActive = false
		st.PutSetupActive = false
		st.CrossConsumedAtBufIdx = -1
		st.SLHitSide = SideNone
	}
}

// ForceExitAll fires an ActionExit signal for every open position.
func (s *Nifty50FnOSL) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sigs []Signal
	for key, st := range s.instruments {
		if st.Side == SideNone {
			continue
		}
		exch, token := "", key
		for i, c := range key {
			if c == ':' {
				exch = key[:i]
				token = key[i+1:]
				break
			}
		}
		side := st.Side
		log.Printf("[strategy] NIFTY50_FNO_SL: ForceExitAll %s %s:%s — %s", side, exch, token, reason)
		s.resetPositionSL(st)
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
func (s *Nifty50FnOSL) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty50FnOSLSnapshot(s.Name(), s.instruments)
}

// Restore recovers strategy state from a snapshot.
func (s *Nifty50FnOSL) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := unmarshalNifty50FnOSLSnapshot(data)
	if err != nil {
		return err
	}
	s.instruments = restoreNifty50FnOSLInstruments(snap)
	return nil
}

// Nifty50FnOSLIndicatorSnapshot holds the current indicator values for Redis publishing.
type Nifty50FnOSLIndicatorSnapshot struct {
	Strategy        string       `json:"strategy"`
	EMA6            float64      `json:"ema6"`
	EMA9            float64      `json:"ema9"`
	EMA6Ready       bool         `json:"ema6_ready"`
	EMA9Ready       bool         `json:"ema9_ready"`
	Side            PositionSide `json:"side"`
	CallSetupActive bool         `json:"call_setup_active"`
	PutSetupActive  bool         `json:"put_setup_active"`
	IndexEntryPrice int64        `json:"index_entry_price"`
	FNOEntryPrice   int64        `json:"fno_entry_price"`
	FNOBestPrice    int64        `json:"fno_best_price"`
}

// IndicatorSnapshot returns the current indicator and position state for Redis publishing.
func (s *Nifty50FnOSL) IndicatorSnapshot() *Nifty50FnOSLIndicatorSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	indexKey := s.cfg.IndexToken
	if indexKey == "" {
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

	snap := &Nifty50FnOSLIndicatorSnapshot{
		Strategy:        s.Name(),
		Side:            st.Side,
		CallSetupActive: st.CallSetupActive,
		PutSetupActive:  st.PutSetupActive,
		IndexEntryPrice: st.IndexEntryPrice,
		FNOEntryPrice:   st.FNOEntryPrice,
		FNOBestPrice:    st.FNOBestPrice,
	}

	if st.EMA6.Ready() {
		snap.EMA6 = st.EMA6.Value()
		snap.EMA6Ready = true
	}
	if st.EMA9.Ready() {
		snap.EMA9 = st.EMA9.Value()
		snap.EMA9Ready = true
	}

	return snap
}
