package strategy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 FnO SL2 — Ultra-Fast Dual-Layer Stop Loss + Resistance Filter
//
//  Entry:  SMA2 × SMA15 crossover (CALL when SMA2 > SMA15, PUT when SMA2 < SMA15)
//          confirmed by EMA6 × SMA2 crossover
//  Exit:   EMA6 × SMA2 crossover (opposite direction)
//  SL:     Trail SL + Hard SL on BOTH index price AND FNO option price
//  Filter: Resistance/support proximity blocking, fresh crossover gate
//
//  Indicator mapping:
//    SMA2  (period 2)  — ultra-fast entry signal
//    EMA6  (period 6)  — medium EMA exit signal
//    SMA15 (period 15) — baseline MA threshold
//
//  BUG FIX: Snapshot version=2. Old v1 snapshots discarded on restore.
// ════════════════════════════════════════════════════════════════════

// Nifty50FnOSL2 implements the NIFTY50 FNO SL2 strategy.
type Nifty50FnOSL2 struct {
	mu          sync.Mutex
	instruments map[string]*nifty50FnOSL2State
	qty         int64
	cfg         Nifty50FnOSL2Config
}

// NewNifty50FnOSL2 creates a new strategy with default config.
func NewNifty50FnOSL2(qty int64) *Nifty50FnOSL2 {
	return NewNifty50FnOSL2WithConfig(qty, DefaultNifty50FnOSL2Config())
}

// NewNifty50FnOSL2WithConfig creates a strategy with custom configuration.
func NewNifty50FnOSL2WithConfig(qty int64, cfg Nifty50FnOSL2Config) *Nifty50FnOSL2 {
	return &Nifty50FnOSL2{
		instruments: make(map[string]*nifty50FnOSL2State, 64),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty50FnOSL2) Name() string                { return "NIFTY50_FNO_SL2" }
func (s *Nifty50FnOSL2) Config() Nifty50FnOSL2Config { return s.cfg }

// SetFNOTokens updates the FNO option tokens at runtime.
func (s *Nifty50FnOSL2) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.FNOCallToken = callToken
	s.cfg.FNOPutToken = putToken
}

// SetResistanceLevels updates resistance levels at runtime.
func (s *Nifty50FnOSL2) SetResistanceLevels(levels []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.ResistanceLevels = levels
}

// SetSupportLevels updates support levels at runtime.
func (s *Nifty50FnOSL2) SetSupportLevels(levels []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.SupportLevels = levels
}

// SetFNOEntryPrice records the live FNO option premium at entry.
func (s *Nifty50FnOSL2) SetFNOEntryPrice(price int64) {
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

// CurrentFNOPosition returns the active FnO premium state.
func (s *Nifty50FnOSL2) CurrentFNOPosition() *LiveFNOPosition {
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

// tf2mSL2 is the 2-minute timeframe in seconds.
const tf2mSL2 = 120

func (s *Nifty50FnOSL2) OnTFCandle(candle model.TFCandle) *Signal {
	if candle.TF != tf2mSL2 {
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

	// Feed indicators
	st.updateIndicators(toCandle1m(candle))

	if !st.bufferReady() {
		return nil
	}

	_, curr := st.prevAndCurr()

	// Detect crossovers
	ema6CrossedAboveSMA2 := st.ema6CrossedAboveSMA2() // PUT exit
	ema6CrossedBelowSMA2 := st.ema6CrossedBelowSMA2() // CALL exit

	sma2CrossedAboveMA15 := st.sma2CrossedAboveMA15() // CALL entry
	sma2CrossedBelowMA15 := st.sma2CrossedBelowMA15() // PUT entry
	ema6CrossedAboveMA15 := st.ema6CrossedAboveMA15()
	ema6CrossedBelowMA15 := st.ema6CrossedBelowMA15()

	bothAboveMA15 := curr.EMA6 > curr.MA15 && curr.SMA2 > curr.MA15
	bothBelowMA15 := curr.EMA6 < curr.MA15 && curr.SMA2 < curr.MA15

	// ── Update Call setup (SMA2 > MA15) ──
	if curr.SMA2 > curr.MA15 {
		if !st.CallSetupActive {
			st.CallSetupActive = true
			log.Printf("[strategy] %s: %s CALL setup activated (SMA2=%.0f > MA15=%.0f)",
				s.Name(), key, curr.SMA2, curr.MA15)
		}
	} else {
		if st.CallSetupActive {
			st.CallSetupActive = false
			log.Printf("[strategy] %s: %s CALL setup deactivated (SMA2=%.0f <= MA15=%.0f)",
				s.Name(), key, curr.SMA2, curr.MA15)
		}
	}

	// ── Update Put setup (SMA2 < MA15) ──
	if curr.SMA2 < curr.MA15 {
		if !st.PutSetupActive {
			st.PutSetupActive = true
			log.Printf("[strategy] %s: %s PUT setup activated (SMA2=%.0f < MA15=%.0f)",
				s.Name(), key, curr.SMA2, curr.MA15)
		}
	} else {
		if st.PutSetupActive {
			st.PutSetupActive = false
			log.Printf("[strategy] %s: %s PUT setup deactivated (SMA2=%.0f >= MA15=%.0f)",
				s.Name(), key, curr.SMA2, curr.MA15)
		}
	}

	// Try exit first
	if inMarketHours {
		if sig := s.evaluateExit(candle, st, curr, ema6CrossedBelowSMA2, ema6CrossedAboveSMA2); sig != nil {
			return sig
		}
	}

	// Try entry
	if inMarketHours && inEntryWindow {
		if sig := s.evaluateEntry(candle, st, curr, candleIST,
			sma2CrossedAboveMA15, sma2CrossedBelowMA15,
			ema6CrossedAboveMA15, ema6CrossedBelowMA15,
			ema6CrossedAboveSMA2, ema6CrossedBelowSMA2,
			bothAboveMA15, bothBelowMA15, justExitedCooldown); sig != nil {
			return sig
		}
	}

	return nil
}

// OnTick implements dual-layer stop loss checking on every tick.
func (s *Nifty50FnOSL2) OnTick(tick model.Tick) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	tickKey := tick.Exchange + ":" + tick.Token

	isIndex := (s.cfg.IndexToken != "" && tickKey == s.cfg.IndexToken)
	isFNOCall := (s.cfg.FNOCallToken != "" && tickKey == s.cfg.FNOCallToken)
	isFNOPut := (s.cfg.FNOPutToken != "" && tickKey == s.cfg.FNOPutToken)

	if !isIndex && !isFNOCall && !isFNOPut {
		return nil
	}

	indexKey := s.cfg.IndexToken
	if indexKey == "" {
		return nil
	}
	st := s.instruments[indexKey]
	if st == nil || st.Side == SideNone {
		return nil
	}

	if isIndex && st.IndexEntryPrice != 0 {
		if sig := s.checkIndexSL(tick, st); sig != nil {
			return sig
		}
	}

	if isFNOCall && st.Side == SideCall && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOSL(tick, st); sig != nil {
			return sig
		}
	}

	if isFNOPut && st.Side == SidePut && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOSL(tick, st); sig != nil {
			return sig
		}
	}

	return nil
}

// checkIndexSL checks Trail SL and Hard SL on the NIFTY index price.
func (s *Nifty50FnOSL2) checkIndexSL(tick model.Tick, st *nifty50FnOSL2State) *Signal {
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
					s.resetPosition(st)
					return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
						Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
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
					s.resetPosition(st)
					return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
						Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
				}
			}
		}
	}

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
				s.resetPosition(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		case SidePut:
			rise := (priceF - entryF) / entryF * 100
			if rise >= s.cfg.IndexHardSLPct {
				reason := fmt.Sprintf("INDEX HARD SL PUT: idx_price=%d rose %.2f%% above idx_entry=%d (SL=%.2f%%)",
					tick.Price, rise, st.IndexEntryPrice, s.cfg.IndexHardSLPct)
				log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
				slSide := st.Side
				st.SLHitSide = slSide
				s.resetPosition(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	return nil
}

// checkFNOSL checks Trail SL and Hard SL on the FNO option premium.
// Both CALL and PUT are BUY orders, so premium UP = profit, premium DOWN = loss.
func (s *Nifty50FnOSL2) checkFNOSL(tick model.Tick, st *nifty50FnOSL2State) *Signal {
	entryF := float64(st.FNOEntryPrice)
	priceF := float64(tick.Price)

	// Update FNO BestPrice — always track highest (we BUY both CALL and PUT)
	if tick.Price > st.FNOBestPrice {
		st.FNOBestPrice = tick.Price
	}

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
				s.resetPosition(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	if s.cfg.FNOHardSLPct > 0 {
		drop := (entryF - priceF) / entryF * 100
		if drop >= s.cfg.FNOHardSLPct {
			reason := fmt.Sprintf("FNO HARD SL %s: fno_price=%d fell %.2f%% below fno_entry=%d (SL=%.2f%%)",
				st.Side, tick.Price, drop, st.FNOEntryPrice, s.cfg.FNOHardSLPct)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			st.SLHitSide = slSide
			s.resetPosition(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
				Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	return nil
}

// ── Exit Logic ──

func (s *Nifty50FnOSL2) evaluateExit(
	candle model.TFCandle, st *nifty50FnOSL2State,
	curr nifty50FnOSL2BufferEntry,
	ema6CrossedBelowSMA2, ema6CrossedAboveSMA2 bool,
) *Signal {
	if st.Side == SideNone {
		return nil
	}

	// CALL Exit: EMA6 crosses below SMA2
	if st.Side == SideCall && ema6CrossedBelowSMA2 {
		reason := fmt.Sprintf("2m CALL exit: EMA6(%.0f) crossed below SMA2(%.0f), close=%d",
			curr.EMA6, curr.SMA2, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		st.SLHitSide = SideCall // Mark for crossover consumption
		s.resetPosition(st)
		return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SideCall,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	// PUT Exit: EMA6 crosses above SMA2
	if st.Side == SidePut && ema6CrossedAboveSMA2 {
		reason := fmt.Sprintf("2m PUT exit: EMA6(%.0f) crossed above SMA2(%.0f), close=%d",
			curr.EMA6, curr.SMA2, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		st.SLHitSide = SidePut // Mark for crossover consumption
		s.resetPosition(st)
		return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SidePut,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	return nil
}

// ── Entry Logic ──

func (s *Nifty50FnOSL2) evaluateEntry(
	candle model.TFCandle, st *nifty50FnOSL2State,
	curr nifty50FnOSL2BufferEntry,
	candleIST time.Time,
	sma2CrossedAboveMA15, sma2CrossedBelowMA15 bool,
	ema6CrossedAboveMA15, ema6CrossedBelowMA15 bool,
	ema6CrossedAboveSMA2, ema6CrossedBelowSMA2 bool,
	bothAboveMA15, bothBelowMA15 bool,
	justExitedCooldown bool,
) *Signal {
	if st.Side != SideNone {
		return nil
	}

	// ── Queue crossover triggers during cooldown ──
	if st.InCooldown {
		if st.PutSetupActive && bothBelowMA15 {
			if sma2CrossedBelowMA15 || ema6CrossedBelowSMA2 {
				st.PendingEntry = SidePut
				log.Printf("[strategy] %s: %s PUT crossover queued during cooldown", s.Name(), candle.Key())
			}
		}
		if st.CallSetupActive && bothAboveMA15 {
			if sma2CrossedAboveMA15 || ema6CrossedAboveSMA2 {
				st.PendingEntry = SideCall
				log.Printf("[strategy] %s: %s CALL crossover queued during cooldown", s.Name(), candle.Key())
			}
		}
		return nil
	}

	// ── Execute pending entry from cooldown ──
	if justExitedCooldown && st.PendingEntry != SideNone {
		pending := st.PendingEntry
		st.PendingEntry = SideNone

		if pending == SidePut && st.PutSetupActive && bothBelowMA15 {
			if isNearSupport(float64(candle.Close), s.cfg.SupportLevels, s.cfg.SupportDistancePct) {
				log.Printf("[strategy] %s: %s PUT pending BLOCKED — near support, close=%d",
					s.Name(), candle.Key(), candle.Close)
			} else {
				st.Side = SidePut
				st.IndexEntryPrice = candle.Close
				st.IndexBestPrice = candle.Close
				reason := fmt.Sprintf("2m PUT entry (pending): EMA6(%.0f) SMA2(%.0f) < SMA15(%.0f), close=%d",
					curr.EMA6, curr.SMA2, curr.MA15, candle.Close)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
			}
		}
		if pending == SideCall && st.CallSetupActive && bothAboveMA15 {
			if isNearResistance(float64(candle.Close), s.cfg.ResistanceLevels, s.cfg.ResistanceDistancePct) {
				log.Printf("[strategy] %s: %s CALL pending BLOCKED — near resistance, close=%d",
					s.Name(), candle.Key(), candle.Close)
			} else {
				st.Side = SideCall
				st.IndexEntryPrice = candle.Close
				st.IndexBestPrice = candle.Close
				reason := fmt.Sprintf("2m CALL entry (pending): EMA6(%.0f) SMA2(%.0f) > SMA15(%.0f), close=%d",
					curr.EMA6, curr.SMA2, curr.MA15, candle.Close)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
			}
		}
		log.Printf("[strategy] %s: %s pending %s entry expired", s.Name(), candle.Key(), pending)
	}

	// ── Re-check pending EMA diff entries ──
	if st.PendingEMADiffEntry != SideNone && st.PendingEMADiffEntry != "" {
		pd := st.PendingEMADiffEntry
		if (pd == SideCall && (!st.CallSetupActive || curr.EMA6 <= curr.MA15)) ||
			(pd == SidePut && (!st.PutSetupActive || curr.EMA6 >= curr.MA15)) {
			log.Printf("[strategy] %s: %s pending %s EMA-diff entry expired (setup deactivated)",
				s.Name(), candle.Key(), pd)
			st.PendingEMADiffEntry = SideNone
		} else if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.SMA2 > 0 {
			diff := (curr.EMA6 - curr.SMA2) / curr.SMA2 * 100
			if diff < 0 {
				diff = -diff
			}
			if diff >= s.cfg.MinEMA6EMA9DiffPct {
				st.PendingEMADiffEntry = SideNone
				st.Side = pd
				st.IndexEntryPrice = candle.Close
				st.IndexBestPrice = candle.Close
				reason := fmt.Sprintf("2m %s entry (deferred EMA-diff): EMA6(%.0f) SMA2(%.0f) diff=%.3f%%, close=%d",
					pd, curr.EMA6, curr.SMA2, diff, candle.Close)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: pd,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	// ── Sideways market filter ──
	if s.cfg.SidewaysEnabled {
		sw := st.isSideways(s.cfg, candleIST)
		if sw.Sideways {
			log.Printf("[strategy] %s: %s SIDEWAYS — skipping entry (chop=%v flat=%v range=%v)",
				s.Name(), candle.Key(), sw.ChopFired, sw.FlatFired, sw.RangeFired)
			return nil
		}
	}

	// ── Trend direction filter ──
	trend := st.trendBias(s.cfg)

	// CALL Entry: SMA2 > SMA15, confirmed by EMA6 × SMA2
	if st.CallSetupActive && curr.SMA2 > curr.MA15 {
		if trend == TrendBearish {
			log.Printf("[strategy] %s: %s CALL blocked — bearish trend", s.Name(), candle.Key())
			return nil
		}
		isInitialEntry := sma2CrossedAboveMA15
		isReEntry := s.cfg.ReEntryEnabled && ema6CrossedAboveSMA2

		if isInitialEntry || isReEntry {
			if s.cfg.MinReEntrySeparationPct > 0 {
				sep := (curr.SMA2 - curr.MA15) / curr.MA15 * 100
				if sep < s.cfg.MinReEntrySeparationPct {
					return nil
				}
			}
			if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.SMA2 > 0 {
				diff := (curr.EMA6 - curr.SMA2) / curr.SMA2 * 100
				if diff < 0 {
					diff = -diff
				}
				if diff < s.cfg.MinEMA6EMA9DiffPct {
					st.PendingEMADiffEntry = SideCall
					return nil
				}
			}
			if isNearResistance(float64(candle.Close), s.cfg.ResistanceLevels, s.cfg.ResistanceDistancePct) {
				log.Printf("[strategy] %s: %s CALL entry BLOCKED — near resistance, close=%d",
					s.Name(), candle.Key(), candle.Close)
				return nil
			}
			st.Side = SideCall
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			entryType := "entry"
			if isReEntry && !isInitialEntry {
				entryType = "re-entry"
			}
			reason := fmt.Sprintf("2m CALL %s: EMA6(%.0f) SMA2(%.0f) > SMA15(%.0f), close=%d",
				entryType, curr.EMA6, curr.SMA2, curr.MA15, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	// PUT Entry: SMA2 < SMA15, confirmed by EMA6 × SMA2
	if st.PutSetupActive && curr.SMA2 < curr.MA15 {
		if trend == TrendBullish {
			log.Printf("[strategy] %s: %s PUT blocked — bullish trend", s.Name(), candle.Key())
			return nil
		}
		isInitialEntry := sma2CrossedBelowMA15
		isReEntry := s.cfg.ReEntryEnabled && ema6CrossedBelowSMA2

		if isInitialEntry || isReEntry {
			if s.cfg.MinReEntrySeparationPct > 0 {
				sep := (curr.MA15 - curr.SMA2) / curr.MA15 * 100
				if sep < s.cfg.MinReEntrySeparationPct {
					return nil
				}
			}
			if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.SMA2 > 0 {
				diff := (curr.EMA6 - curr.SMA2) / curr.SMA2 * 100
				if diff < 0 {
					diff = -diff
				}
				if diff < s.cfg.MinEMA6EMA9DiffPct {
					st.PendingEMADiffEntry = SidePut
					return nil
				}
			}
			if isNearSupport(float64(candle.Close), s.cfg.SupportLevels, s.cfg.SupportDistancePct) {
				log.Printf("[strategy] %s: %s PUT entry BLOCKED — near support, close=%d",
					s.Name(), candle.Key(), candle.Close)
				return nil
			}
			st.Side = SidePut
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			entryType := "entry"
			if isReEntry && !isInitialEntry {
				entryType = "re-entry"
			}
			reason := fmt.Sprintf("2m PUT %s: EMA6(%.0f) SMA2(%.0f) < SMA15(%.0f), close=%d",
				entryType, curr.EMA6, curr.SMA2, curr.MA15, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	return nil
}

// ── Helpers ──

func (s *Nifty50FnOSL2) getOrCreate(key string) *nifty50FnOSL2State {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty50FnOSL2State(s.cfg)
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50FnOSL2) resetPosition(st *nifty50FnOSL2State) {
	// Mark crossover as consumed on exit so lookback won't re-trigger
	if st.SLHitSide == SideCall || st.SLHitSide == SidePut {
		st.CrossConsumedAtBufIdx = (st.BufferIdx - 1 + len(st.Buffer)) % len(st.Buffer)
	}
	st.SLHitSide = SideNone
	st.Side = SideNone
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
func (s *Nifty50FnOSL2) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		st.Side = SideNone
		st.IndexEntryPrice = 0
		st.IndexBestPrice = 0
		st.FNOEntryPrice = 0
		st.FNOBestPrice = 0
		st.InCooldown = false
		st.CandlesSinceExit = 0
		st.CallSetupActive = false
		st.PutSetupActive = false
		st.CrossConsumedAtBufIdx = -1
	}
}

// ForceExitAll fires an ActionExit signal for every open position.
func (s *Nifty50FnOSL2) ForceExitAll(reason string) []Signal {
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
		log.Printf("[strategy] %s: ForceExitAll %s %s:%s — %s", s.Name(), side, exch, token, reason)
		s.resetPosition(st)
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
func (s *Nifty50FnOSL2) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty50FnOSL2Snapshot(s.Name(), s.instruments)
}

// Restore recovers strategy state from a snapshot.
func (s *Nifty50FnOSL2) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := unmarshalNifty50FnOSL2Snapshot(data)
	if err != nil {
		return err
	}
	s.instruments = restoreNifty50FnOSL2Instruments(snap)
	return nil
}
