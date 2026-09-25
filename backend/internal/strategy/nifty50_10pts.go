package strategy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 10PTS — EMA6 × EMA9 + FNO 10-Point Target Exit
//
//  Same entry/exit as NIFTY50_FNO_SL (EMA6/EMA9 crossovers + momentum
//  bypass), with one key difference:
//    • OnTick: exit when FNO premium gains FNOTargetProfitPts paise.
//    • After target exit, position is cleared — next momentum signal
//      on the same candle or next candle re-enters immediately.
//  FNO Trail SL + Hard SL also active. Index SL disabled.
// ════════════════════════════════════════════════════════════════════

// Nifty5010Pts implements the NIFTY 50 FnO strategy with a 10-point FNO target.
type Nifty5010Pts struct {
	mu                 sync.Mutex
	instruments        map[string]*nifty5010PtsState
	qty                int64
	cfg                Nifty5010PtsConfig
	marketStateProvider func() EntryMarketState // injected by service layer
}

// NewNifty5010Pts creates a new strategy with default config.
func NewNifty5010Pts(qty int64) *Nifty5010Pts {
	return NewNifty5010PtsWithConfig(qty, DefaultNifty5010PtsConfig())
}

// NewNifty5010PtsWithConfig creates a strategy with custom configuration.
func NewNifty5010PtsWithConfig(qty int64, cfg Nifty5010PtsConfig) *Nifty5010Pts {
	return &Nifty5010Pts{
		instruments: make(map[string]*nifty5010PtsState, 64),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty5010Pts) Name() string {
	if s.cfg.NameOverride != "" {
		return s.cfg.NameOverride
	}
	return "NIFTY50_10PTS"
}

func (s *Nifty5010Pts) Config() Nifty5010PtsConfig { return s.cfg }

// SetFNOTokens updates the FNO option tokens at runtime.
func (s *Nifty5010Pts) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.FNOCallToken = callToken
	s.cfg.FNOPutToken = putToken
}

// SetMarketStateProvider injects the external market state source.
// The provider should return the current EntryMarketState from
// the primary NIFTY50_FNO strategy's EntryAutomationContext.
func (s *Nifty5010Pts) SetMarketStateProvider(fn func() EntryMarketState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.marketStateProvider = fn
}

// SetFNOEntryPrice records the live FNO option premium at entry time.
func (s *Nifty5010Pts) SetFNOEntryPrice(price int64) {
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

// CurrentFNOPosition returns the active position's FNO premium snapshot.
func (s *Nifty5010Pts) CurrentFNOPosition() *LiveFNOPosition {
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
func (s *Nifty5010Pts) OnTFCandle(candle model.TFCandle) *Signal {
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
	st := s.getOrCreate10Pts(key)

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
	entryCandle := toCandle1m(candle)
	st.updateEMA(entryCandle)

	if !st.bufferReady() {
		return nil
	}

	_, curr := st.prevAndCurr()

	// Detect crossovers (10-candle buffer lookback)
	ema6CrossedAboveEMA9 := st.ema6CrossedAboveEMA9() // CALL entry / PUT exit
	ema6CrossedBelowEMA9 := st.ema6CrossedBelowEMA9() // PUT entry / CALL exit

	// Update Call setup (EMA6 > EMA9)
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

	// Update Put setup (EMA9 > EMA6)
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

	// Try exit first (market hours only)
	if inMarketHours {
		if sig := s.evaluateExit10Pts(candle, st, curr, ema6CrossedBelowEMA9, ema6CrossedAboveEMA9); sig != nil {
			return sig
		}
	}

	// Try entry (market hours only)
	if inMarketHours && inEntryWindow {
		if sig := s.evaluateEntry10Pts(candle, st, curr, candleIST,
			ema6CrossedAboveEMA9, ema6CrossedBelowEMA9,
			justExitedCooldown); sig != nil {
			return sig
		}
	}

	return nil
}

// OnTick checks FNO-only SL and FNO target profit on every price update.
func (s *Nifty5010Pts) OnTick(tick model.Tick) *Signal {
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

	// Index SL — disabled by default (IndexHardSLPct/IndexTrailSLPct = 0)
	if isIndex && st.IndexEntryPrice != 0 {
		if sig := s.checkIndexSL10Pts(tick, st); sig != nil {
			return sig
		}
	}

	// FNO CALL tick
	if isFNOCall && st.Side == SideCall && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOSL10Pts(tick, st); sig != nil {
			return sig
		}
	}

	// FNO PUT tick
	if isFNOPut && st.Side == SidePut && st.FNOEntryPrice != 0 {
		if sig := s.checkFNOSL10Pts(tick, st); sig != nil {
			return sig
		}
	}

	return nil
}

// checkIndexSL10Pts checks index-based SL (disabled by default).
func (s *Nifty5010Pts) checkIndexSL10Pts(tick model.Tick, st *nifty5010PtsState) *Signal {
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
					reason := fmt.Sprintf("INDEX TRAIL SL CALL: idx_price=%d fell %.2f%% (trail=%.2f%%, entry=%d)",
						tick.Price, drop, s.cfg.IndexTrailSLPct, st.IndexEntryPrice)
					log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
					slSide := st.Side
					st.SLHitSide = slSide
					s.resetPosition10Pts(st)
					return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
						Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
				}
			}
		case SidePut:
			profit := (entryF - bestF) / entryF * 100
			if profit >= s.cfg.IndexTrailStartPct {
				rise := (priceF - bestF) / bestF * 100
				if rise >= s.cfg.IndexTrailSLPct {
					reason := fmt.Sprintf("INDEX TRAIL SL PUT: idx_price=%d rose %.2f%% (trail=%.2f%%, entry=%d)",
						tick.Price, rise, s.cfg.IndexTrailSLPct, st.IndexEntryPrice)
					log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
					slSide := st.Side
					st.SLHitSide = slSide
					s.resetPosition10Pts(st)
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
				reason := fmt.Sprintf("INDEX HARD SL CALL: idx_price=%d fell %.2f%% (SL=%.2f%%, entry=%d)",
					tick.Price, drop, s.cfg.IndexHardSLPct, st.IndexEntryPrice)
				log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
				slSide := st.Side
				st.SLHitSide = slSide
				s.resetPosition10Pts(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		case SidePut:
			rise := (priceF - entryF) / entryF * 100
			if rise >= s.cfg.IndexHardSLPct {
				reason := fmt.Sprintf("INDEX HARD SL PUT: idx_price=%d rose %.2f%% (SL=%.2f%%, entry=%d)",
					tick.Price, rise, s.cfg.IndexHardSLPct, st.IndexEntryPrice)
				log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
				slSide := st.Side
				st.SLHitSide = slSide
				s.resetPosition10Pts(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	return nil
}

// checkFNOSL10Pts checks FNO Trail SL, Hard SL, Target Profit, and Step Trailing.
// Both CALL and PUT are BUY orders: premium UP = profit, premium DOWN = loss.
func (s *Nifty5010Pts) checkFNOSL10Pts(tick model.Tick, st *nifty5010PtsState) *Signal {
	entryF := float64(st.FNOEntryPrice)
	priceF := float64(tick.Price)

	// Update FNO BestPrice — always track highest (we BUY both CALL and PUT)
	if tick.Price > st.FNOBestPrice {
		st.FNOBestPrice = tick.Price
	}

	// ── Step Trailing Logic ──
	if s.cfg.StepTrailEnabled && st.FNOEntryPrice > 0 {
		profitPts := tick.Price - st.FNOEntryPrice

		// Step 1: Move SL to cost at +3 points
		if !st.Step1Activated && profitPts >= s.cfg.StepTrail1Pts {
			st.Step1Activated = true
			st.TrailingSL = st.FNOEntryPrice // SL at entry (cost)
			log.Printf("[strategy] %s: %s:%s Step 1 activated — SL moved to cost (profit=%d pts, entry=%d)",
				s.Name(), tick.Exchange, tick.Token, profitPts/100, st.FNOEntryPrice)
		}

		// Step 2: Activate trailing at +4 points
		if !st.Step2Activated && profitPts >= s.cfg.StepTrail2Pts {
			st.Step2Activated = true
			log.Printf("[strategy] %s: %s:%s Step 2 activated — trailing enabled (profit=%d pts)",
				s.Name(), tick.Exchange, tick.Token, profitPts/100)
		}

		// Trailing with 2-point buffer (after Step 2)
		if st.Step2Activated && s.cfg.TrailingBufferPts > 0 {
			// Trail SL = Best Price - Buffer
			trailSL := st.FNOBestPrice - s.cfg.TrailingBufferPts
			
			// Update trailing SL if it's higher than current
			if trailSL > st.TrailingSL {
				st.TrailingSL = trailSL
			}

			// Exit if price falls below trailing SL
			if tick.Price < st.TrailingSL {
				profitPct := (priceF - entryF) / entryF * 100
				reason := fmt.Sprintf("FNO TRAILING SL %s: price=%d fell below trail=%d (buffer=%d pts, profit=%.2f%%, best=%d)",
					st.Side, tick.Price, st.TrailingSL, s.cfg.TrailingBufferPts/100, profitPct, st.FNOBestPrice)
				log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
				slSide := st.Side
				st.SLHitSide = slSide
				s.resetPosition10Pts(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		}

		// Check Step 1 SL (if Step 2 not activated yet)
		if st.Step1Activated && !st.Step2Activated && tick.Price < st.TrailingSL {
			profitPct := (priceF - entryF) / entryF * 100
			reason := fmt.Sprintf("FNO SL AT COST %s: price=%d fell below cost=%d (profit=%.2f%%)",
				st.Side, tick.Price, st.TrailingSL, profitPct)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			st.SLHitSide = slSide
			s.resetPosition10Pts(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
				Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	// ── FNO Dynamic Target Profit ──
	// Exit when FNO premium gains the active target % (set at entry based on regime).
	activeTarget := st.ActiveFNOTargetProfitPct
	if activeTarget <= 0 {
		activeTarget = s.cfg.FNOTargetProfitPct // fallback to default
	}
	if activeTarget > 0 {
		profitPct := (priceF - entryF) / entryF * 100
		if profitPct >= activeTarget {
			reason := fmt.Sprintf("FNO TARGET PROFIT %s: fno_price=%d gained %.2f%% above fno_entry=%d (target=%.2f%%)",
				st.Side, tick.Price, profitPct, st.FNOEntryPrice, activeTarget)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			// Consume the current crossover regime so one target exit does not
			// immediately re-enter from the same stale 10-candle signal.
			st.SLHitSide = slSide
			s.resetPosition10Pts(st) // clear — no cooldown, ready for re-entry
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
				s.resetPosition10Pts(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	// ── FNO Hard SL (fixed from entry price) ──
	if s.cfg.FNOHardSLPct > 0 {
		drop := (entryF - priceF) / entryF * 100
		if drop >= s.cfg.FNOHardSLPct {
			reason := fmt.Sprintf("FNO HARD SL %s: fno_price=%d fell %.2f%% below fno_entry=%d (SL=%.2f%%)",
				st.Side, tick.Price, drop, st.FNOEntryPrice, s.cfg.FNOHardSLPct)
			log.Printf("[strategy] %s: %s:%s %s", s.Name(), tick.Exchange, tick.Token, reason)
			slSide := st.Side
			st.SLHitSide = slSide
			s.resetPosition10Pts(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
				Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	return nil
}

// ── Exit Logic ──

func (s *Nifty5010Pts) evaluateExit10Pts(
	candle model.TFCandle, st *nifty5010PtsState,
	curr nifty5010PtsBufferEntry,
	ema6CrossedBelowEMA9, ema6CrossedAboveEMA9 bool,
) *Signal {
	if st.Side == SideNone {
		return nil
	}

	// ── Exit Rule 1: Close below/above EMA9 (optional, disabled by default) ──
	if s.cfg.CloseVsEMA9ExitEnabled {
		// CALL: exit if candle closes below EMA9
		if st.Side == SideCall && curr.Close < curr.EMA9 {
			reason := fmt.Sprintf("1m CALL exit: close=%d below EMA9(%.2f), EMA6=%.2f",
				candle.Close, curr.EMA9, curr.EMA6)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			s.resetPosition10Pts(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SideCall,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}

		// PUT: exit if candle closes above EMA9
		if st.Side == SidePut && curr.Close > curr.EMA9 {
			reason := fmt.Sprintf("1m PUT exit: close=%d above EMA9(%.2f), EMA6=%.2f",
				candle.Close, curr.EMA9, curr.EMA6)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			s.resetPosition10Pts(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SidePut,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
	}


	// ── Exit Rule 3: EMA Crossover (original logic) ──
	// CALL Exit: EMA6 crosses below EMA9
	if st.Side == SideCall && ema6CrossedBelowEMA9 {
		reason := fmt.Sprintf("1m CALL exit: EMA6(%.2f) crossed below EMA9(%.2f), close=%d",
			curr.EMA6, curr.EMA9, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		s.resetPosition10Pts(st)
		return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SideCall,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	// PUT Exit: EMA6 crosses above EMA9
	if st.Side == SidePut && ema6CrossedAboveEMA9 {
		reason := fmt.Sprintf("1m PUT exit: EMA6(%.2f) crossed above EMA9(%.2f), close=%d",
			curr.EMA6, curr.EMA9, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		s.resetPosition10Pts(st)
		return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SidePut,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	return nil
}

// ── Entry Logic ──

func (s *Nifty5010Pts) evaluateEntry10Pts(
	candle model.TFCandle, st *nifty5010PtsState,
	curr nifty5010PtsBufferEntry,
	candleIST time.Time,
	ema6CrossedAboveEMA9, ema6CrossedBelowEMA9 bool,
	justExitedCooldown bool,
) *Signal {
	if st.Side != SideNone {
		return nil
	}

	// ── Big Move Filter & Pullback Detection ──
	// Disabled: Big move and pullback logic removed.

	// ── VWAP Distance Check ──
	// Ensure price is near VWAP (if configured)
	if s.cfg.VWAPMaxDistancePct > 0 {
		// Get VWAP from buffer (assuming it's tracked)
		// For now, we'll skip this check if VWAP is not available
		// TODO: Add VWAP tracking to buffer if needed
		vwap := curr.EMA9 // Placeholder - use EMA9 as proxy for now
		if !st.isNearVWAP(s.cfg, vwap) {
			log.Printf("[strategy] %s: %s Price too far from VWAP — skipping entry", s.Name(), candle.Key())
			return nil
		}
	}

	// ── Market regime detection (from external provider) ──
	// Trending → trade with default 1.5% target
	// Sideways → trade with reduced 1.0% target
	// Choppy   → skip entry entirely
	var entryFNOTarget float64 = s.cfg.FNOTargetProfitPct
	if s.marketStateProvider != nil {
		ms := s.marketStateProvider()
		switch ms {
		case EntryMarketStateChoppy:
			log.Printf("[strategy] %s: %s CHOPPY (external) — skipping entry", s.Name(), candle.Key())
			return nil
		case EntryMarketStateSideways:
			if s.cfg.SidewaysFNOTargetProfitPct > 0 {
				entryFNOTarget = s.cfg.SidewaysFNOTargetProfitPct
			}
			log.Printf("[strategy] %s: %s SIDEWAYS (external) — using reduced target %.2f%%",
				s.Name(), candle.Key(), entryFNOTarget)
		case EntryMarketStateTrending:
			// default: use full FNOTargetProfitPct
		}
	} else if s.cfg.SidewaysEnabled {
		// Fallback: internal detection if no external provider
		sw := st.isSideways(s.cfg, candleIST)
		if sw.Score >= 3 {
			log.Printf("[strategy] %s: %s CHOPPY — skipping entry (chop=%v flat=%v range=%v score=%d)",
				s.Name(), candle.Key(), sw.ChopFired, sw.FlatFired, sw.RangeFired, sw.Score)
			return nil
		}
		if sw.Score >= 2 {
			if s.cfg.SidewaysFNOTargetProfitPct > 0 {
				entryFNOTarget = s.cfg.SidewaysFNOTargetProfitPct
			}
			log.Printf("[strategy] %s: %s SIDEWAYS — using reduced target %.2f%% (chop=%v flat=%v range=%v)",
				s.Name(), candle.Key(), entryFNOTarget, sw.ChopFired, sw.FlatFired, sw.RangeFired)
		}
	}

	// Queue crossover triggers during cooldown
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

	// Execute pending entry from cooldown
	if justExitedCooldown && st.PendingEntry != SideNone {
		pending := st.PendingEntry
		st.PendingEntry = SideNone
		if pending == SidePut && curr.EMA9 > curr.EMA6 {
			// Check confirmation candle if enabled
			if s.cfg.ConfirmationCandleEnabled && !st.isConfirmationCandle(SidePut) {
				log.Printf("[strategy] %s: %s Pending PUT entry waiting for RED confirmation candle", s.Name(), candle.Key())
				st.PendingEntry = SidePut // Keep pending
				return nil
			}
			st.Side = SidePut
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.EntryPrice = candle.Close
			st.CrossConsumedAtBufIdx = -1
			st.ActiveFNOTargetProfitPct = entryFNOTarget
			st.Step1Activated = false
			st.Step2Activated = false
			st.TrailingSL = 0
			reason := fmt.Sprintf("1m PUT entry (pending): EMA9(%.2f) > EMA6(%.2f), close=%d",
				curr.EMA9, curr.EMA6, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
		if pending == SideCall && curr.EMA6 > curr.EMA9 {
			// Check confirmation candle if enabled
			if s.cfg.ConfirmationCandleEnabled && !st.isConfirmationCandle(SideCall) {
				log.Printf("[strategy] %s: %s Pending CALL entry waiting for GREEN confirmation candle", s.Name(), candle.Key())
				st.PendingEntry = SideCall // Keep pending
				return nil
			}
			st.Side = SideCall
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.EntryPrice = candle.Close
			st.CrossConsumedAtBufIdx = -1
			st.ActiveFNOTargetProfitPct = entryFNOTarget
			st.Step1Activated = false
			st.Step2Activated = false
			st.TrailingSL = 0
			reason := fmt.Sprintf("1m CALL entry (pending): EMA6(%.2f) > EMA9(%.2f), close=%d",
				curr.EMA6, curr.EMA9, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
		log.Printf("[strategy] %s: %s pending %s entry expired", s.Name(), candle.Key(), pending)
	}

	// ── Check for pending confirmation candle ──
	if st.ConfirmationPending == SideCall || st.ConfirmationPending == SidePut {
		pending := st.ConfirmationPending
		// Verify setup still valid
		if (pending == SideCall && curr.EMA6 <= curr.EMA9) ||
			(pending == SidePut && curr.EMA9 <= curr.EMA6) {
			// Setup broken, clear pending
			st.ConfirmationPending = SideNone
			log.Printf("[strategy] %s: %s %s confirmation expired (setup broken)", s.Name(), candle.Key(), pending)
			return nil
		}

		// Check if we have confirmation candle
		if st.isConfirmationCandle(pending) {
			st.ConfirmationPending = SideNone
			st.Side = pending
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.EntryPrice = candle.Close
			st.CrossConsumedAtBufIdx = -1
			st.ActiveFNOTargetProfitPct = entryFNOTarget
			st.Step1Activated = false
			st.Step2Activated = false
			st.TrailingSL = 0
			candleType := "GREEN"
			if pending == SidePut {
				candleType = "RED"
			}
			reason := fmt.Sprintf("1m %s entry (confirmed): %s candle, EMA6=%.2f, EMA9=%.2f, close=%d",
				pending, candleType, curr.EMA6, curr.EMA9, candle.Close)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: pending,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
		// Still waiting for confirmation
		candleType := "GREEN"
		if pending == SidePut {
			candleType = "RED"
		}
		log.Printf("[strategy] %s: %s Waiting for %s confirmation candle for %s entry",
			s.Name(), candle.Key(), candleType, pending)
		return nil
	} else if st.ConfirmationPending != SideNone {
		// Self-heal: clear invalid ConfirmationPending value (e.g., empty string from snapshot)
		log.Printf("[strategy] %s: %s clearing invalid ConfirmationPending=%q",
			s.Name(), candle.Key(), st.ConfirmationPending)
		st.ConfirmationPending = SideNone
	}

	// Re-check pending EMA diff entries
	if st.PendingEMADiffEntry != SideNone && st.PendingEMADiffEntry != "" {
		pd := st.PendingEMADiffEntry
		conditionOK := (pd == SideCall && curr.EMA6 > curr.EMA9) ||
			(pd == SidePut && curr.EMA9 > curr.EMA6)
		if !conditionOK {
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
				st.EntryPrice = candle.Close
				st.CrossConsumedAtBufIdx = -1
				st.ActiveFNOTargetProfitPct = entryFNOTarget
				reason := fmt.Sprintf("1m %s entry (deferred EMA-diff): EMA6(%.2f) EMA9(%.2f) diff=%.3f%%, close=%d",
					pd, curr.EMA6, curr.EMA9, diff, candle.Close)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: pd,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	// ── Momentum bypass (EMA9 as anchor) ──
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
				// Close below EMA9 → forced PUT
				st.Side = SidePut
				st.IndexEntryPrice = candle.Close
				st.IndexBestPrice = candle.Close
				st.EntryPrice = candle.Close
				st.CrossConsumedAtBufIdx = -1
				st.ActiveFNOTargetProfitPct = entryFNOTarget
				st.Step1Activated = false
				st.Step2Activated = false
				st.TrailingSL = 0
				reason := fmt.Sprintf("1m PUT entry (momentum): close=%d is %.2f%% below EMA9(%.2f), EMA6=%.2f",
					candle.Close, pct, curr.EMA9, curr.EMA6)
				log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
				return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
					Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
			}
			// Close above EMA9 → forced CALL
			st.Side = SideCall
			st.IndexEntryPrice = candle.Close
			st.IndexBestPrice = candle.Close
			st.EntryPrice = candle.Close
			st.CrossConsumedAtBufIdx = -1
			st.ActiveFNOTargetProfitPct = entryFNOTarget
			st.Step1Activated = false
			st.Step2Activated = false
			st.TrailingSL = 0
			reason := fmt.Sprintf("1m CALL entry (momentum): close=%d is %.2f%% above EMA9(%.2f), EMA6=%.2f",
				candle.Close, pct, curr.EMA9, curr.EMA6)
			log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
			return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
				Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	// Trend direction filter (disabled by default)
	trend := st.trendBias(s.cfg)

	// CALL Entry: EMA6 crosses above EMA9
	if curr.EMA6 > curr.EMA9 && ema6CrossedAboveEMA9 {
		if trend == TrendBearish {
			log.Printf("[strategy] %s: %s CALL blocked — bearish trend", s.Name(), candle.Key())
			return nil
		}

		// Skip crossover candle if configured
		if s.cfg.SkipCrossoverCandle {
			st.ConfirmationPending = SideCall
			log.Printf("[strategy] %s: %s CALL crossover detected — waiting for confirmation candle", s.Name(), candle.Key())
			return nil
		}

		// EMA diff guard
		if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.EMA9 > 0 {
			diff := (curr.EMA6 - curr.EMA9) / curr.EMA9 * 100
			if diff < 0 {
				diff = -diff
			}
			if diff < s.cfg.MinEMA6EMA9DiffPct {
				log.Printf("[strategy] %s: %s CALL entry DEFERRED — EMA diff %.3f%% < %.3f%%",
					s.Name(), candle.Key(), diff, s.cfg.MinEMA6EMA9DiffPct)
				st.PendingEMADiffEntry = SideCall
				return nil
			}
		}

		// Check confirmation candle if enabled (and not skipping crossover)
		if s.cfg.ConfirmationCandleEnabled && !st.isConfirmationCandle(SideCall) {
			st.ConfirmationPending = SideCall
			log.Printf("[strategy] %s: %s CALL entry waiting for GREEN confirmation candle", s.Name(), candle.Key())
			return nil
		}

		st.Side = SideCall
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		st.EntryPrice = candle.Close
		st.CrossConsumedAtBufIdx = -1
		st.ActiveFNOTargetProfitPct = entryFNOTarget
		st.Step1Activated = false
		st.Step2Activated = false
		st.TrailingSL = 0
		reason := fmt.Sprintf("1m CALL entry: EMA6(%.2f) crossed above EMA9(%.2f), close=%d",
			curr.EMA6, curr.EMA9, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	// PUT Entry: EMA9 crosses above EMA6
	if curr.EMA9 > curr.EMA6 && ema6CrossedBelowEMA9 {
		if trend == TrendBullish {
			log.Printf("[strategy] %s: %s PUT blocked — bullish trend", s.Name(), candle.Key())
			return nil
		}

		// Skip crossover candle if configured
		if s.cfg.SkipCrossoverCandle {
			st.ConfirmationPending = SidePut
			log.Printf("[strategy] %s: %s PUT crossover detected — waiting for confirmation candle", s.Name(), candle.Key())
			return nil
		}

		// EMA diff guard
		if s.cfg.MinEMA6EMA9DiffPct > 0 && curr.EMA9 > 0 {
			diff := (curr.EMA9 - curr.EMA6) / curr.EMA9 * 100
			if diff < 0 {
				diff = -diff
			}
			if diff < s.cfg.MinEMA6EMA9DiffPct {
				log.Printf("[strategy] %s: %s PUT entry DEFERRED — EMA diff %.3f%% < %.3f%%",
					s.Name(), candle.Key(), diff, s.cfg.MinEMA6EMA9DiffPct)
				st.PendingEMADiffEntry = SidePut
				return nil
			}
		}

		// Check confirmation candle if enabled (and not skipping crossover)
		if s.cfg.ConfirmationCandleEnabled && !st.isConfirmationCandle(SidePut) {
			st.ConfirmationPending = SidePut
			log.Printf("[strategy] %s: %s PUT entry waiting for RED confirmation candle", s.Name(), candle.Key())
			return nil
		}

		st.Side = SidePut
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		st.EntryPrice = candle.Close
		st.CrossConsumedAtBufIdx = -1
		st.ActiveFNOTargetProfitPct = entryFNOTarget
		st.Step1Activated = false
		st.Step2Activated = false
		st.TrailingSL = 0
		reason := fmt.Sprintf("1m PUT entry: EMA9(%.2f) crossed above EMA6(%.2f), close=%d",
			curr.EMA9, curr.EMA6, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	return nil
}

// ── Helpers ──

func (s *Nifty5010Pts) getOrCreate10Pts(key string) *nifty5010PtsState {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty5010PtsState(s.cfg)
		s.instruments[key] = st
	}
	return st
}

// resetPosition10Pts clears position state.
// NOTE: No cooldown on target-profit exits — position is immediately available
// for re-entry via next momentum signal.
func (s *Nifty5010Pts) resetPosition10Pts(st *nifty5010PtsState) {
	st.Side = SideNone
	st.EntryPrice = 0
	st.BestPrice = 0
	st.IndexEntryPrice = 0
	st.IndexBestPrice = 0
	st.FNOEntryPrice = 0
	st.FNOBestPrice = 0
	st.ActiveFNOTargetProfitPct = 0
	st.PendingEntry = SideNone
	st.ConfirmationPending = SideNone
	st.Step1Activated = false
	st.Step2Activated = false
	st.TrailingSL = 0
	// Consume crossover state on explicit SL/target exits to prevent stale
	// 10-candle crossover re-entries.
	if st.SLHitSide == SideCall || st.SLHitSide == SidePut {
		st.CrossConsumedAtBufIdx = (st.BufferIdx - 1 + len(st.Buffer)) % len(st.Buffer)
	}
	st.SLHitSide = SideNone // clear after use
	if s.cfg.CooldownCandles > 0 {
		st.InCooldown = true
		st.CandlesSinceExit = 0
	}
}

// ResetPositions resets position state while keeping indicators warm.
func (s *Nifty5010Pts) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		st.Side = SideNone
		st.EntryPrice = 0
		st.BestPrice = 0
		st.IndexEntryPrice = 0
		st.IndexBestPrice = 0
		st.FNOEntryPrice = 0
		st.FNOBestPrice = 0
		st.ActiveFNOTargetProfitPct = 0
		st.InCooldown = false
		st.CandlesSinceExit = 0
		st.CallSetupActive = false
		st.PutSetupActive = false
		st.CrossConsumedAtBufIdx = -1
		st.SLHitSide = SideNone
		st.ConfirmationPending = SideNone
		st.Step1Activated = false
		st.Step2Activated = false
		st.TrailingSL = 0
	}
}

// ForceExitAll fires an ActionExit signal for every open position.
func (s *Nifty5010Pts) ForceExitAll(reason string) []Signal {
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
		s.resetPosition10Pts(st)
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
func (s *Nifty5010Pts) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty5010PtsSnapshot(s.Name(), s.instruments)
}

// Restore recovers strategy state from a snapshot.
func (s *Nifty5010Pts) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := unmarshalNifty5010PtsSnapshot(data)
	if err != nil {
		return err
	}
	s.instruments = restoreNifty5010PtsInstruments(snap)
	return nil
}

// Nifty5010PtsIndicatorSnapshot holds current indicator values for Redis publishing.
type Nifty5010PtsIndicatorSnapshot struct {
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
func (s *Nifty5010Pts) IndicatorSnapshot() *Nifty5010PtsIndicatorSnapshot {
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

	snap := &Nifty5010PtsIndicatorSnapshot{
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
