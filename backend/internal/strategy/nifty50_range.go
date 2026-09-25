package strategy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 RANGE — Support/Resistance Range Trading Strategy
//
//  Strategy Logic:
//    1. Detect support and resistance levels from recent swing points
//    2. Calculate range size between support and resistance
//    3. ONLY TRADE if range is >= 10 points (or configured threshold)
//    4. BUY CALL near support, target = resistance
//    5. BUY PUT near resistance, target = support
//    6. Exit at 1.5% FNO profit or if range breaks
// ════════════════════════════════════════════════════════════════════

type Nifty50Range struct {
	mu          sync.Mutex
	instruments map[string]*nifty50RangeState
	qty         int64
	cfg         Nifty50RangeConfig
}

func NewNifty50Range(qty int64) *Nifty50Range {
	return NewNifty50RangeWithConfig(qty, DefaultNifty50RangeConfig())
}

func NewNifty50RangeWithConfig(qty int64, cfg Nifty50RangeConfig) *Nifty50Range {
	return &Nifty50Range{
		instruments: make(map[string]*nifty50RangeState, 64),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty50Range) Name() string {
	if s.cfg.NameOverride != "" {
		return s.cfg.NameOverride
	}
	return "NIFTY50_RANGE"
}

func (s *Nifty50Range) Config() Nifty50RangeConfig { return s.cfg }

func (s *Nifty50Range) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.FNOCallToken = callToken
	s.cfg.FNOPutToken = putToken
}

func (s *Nifty50Range) SetFNOEntryPrice(price int64) {
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

func (s *Nifty50Range) OnTFCandle(candle model.TFCandle) *Signal {
	if candle.TF != tf1m {
		return nil
	}

	if s.cfg.IndexToken != "" && candle.Key() != s.cfg.IndexToken {
		return nil
	}

	// Market hours: 9:16–15:29 IST
	ist, _ := time.LoadLocation("Asia/Kolkata")
	candleIST := candle.TS.In(ist)
	marketOpen := candleIST.Hour() > 9 || (candleIST.Hour() == 9 && candleIST.Minute() >= 16)
	marketClose := candleIST.Hour() < 15 || (candleIST.Hour() == 15 && candleIST.Minute() <= 29)
	inMarketHours := marketOpen && marketClose

	s.mu.Lock()
	defer s.mu.Unlock()

	key := candle.Key()
	st := s.getOrCreateRange(key)

	// Dedup
	closeTS := candle.TS
	if !st.LastCloseTS.IsZero() && !closeTS.After(st.LastCloseTS) {
		return nil
	}
	st.LastCloseTS = closeTS

	// Track cooldown
	if st.InCooldown {
		st.CandlesSinceExit++
		if s.cfg.CooldownCandles > 0 && st.CandlesSinceExit >= s.cfg.CooldownCandles {
			st.InCooldown = false
			st.CandlesSinceExit = 0
		}
	}

	// Update indicators
	entryCandle := toCandle1m(candle)
	st.updateIndicators(entryCandle)

	if !st.bufferReady() {
		return nil
	}

	// Update swing points
	s.updateSwingPoints(st)

	// Update support/resistance levels
	s.updateSRLevels(st, candleIST)

	// Detect active range
	s.detectActiveRange(st, float64(candle.Close))

	if !inMarketHours {
		return nil
	}

	// Try exit first
	if sig := s.evaluateExit(candle, st); sig != nil {
		return sig
	}

	// Try entry
	if !st.InCooldown {
		if sig := s.evaluateEntry(candle, st); sig != nil {
			return sig
		}
	}

	return nil
}

func (s *Nifty50Range) OnTick(tick model.Tick) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	tickKey := tick.Exchange + ":" + tick.Token
	isFNOCall := (s.cfg.FNOCallToken != "" && tickKey == s.cfg.FNOCallToken)
	isFNOPut := (s.cfg.FNOPutToken != "" && tickKey == s.cfg.FNOPutToken)

	if !isFNOCall && !isFNOPut {
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

	if isFNOCall && st.Side == SideCall && st.FNOEntryPrice != 0 {
		return s.checkFNOTargetAndSL(tick, st)
	}

	if isFNOPut && st.Side == SidePut && st.FNOEntryPrice != 0 {
		return s.checkFNOTargetAndSL(tick, st)
	}

	return nil
}

// ── Support/Resistance Detection ──

func (s *Nifty50Range) updateSwingPoints(st *nifty50RangeState) {
	if st.BufferCount < s.cfg.SwingLookback {
		return
	}

	// Check middle point for swing high/low
	midOffset := s.cfg.SwingLookback / 2
	mid, ok := st.getBufferEntry(midOffset)
	if !ok {
		return
	}

	// Check if it's a swing high
	isSwingHigh := true
	for i := 0; i < s.cfg.SwingLookback; i++ {
		if i == midOffset {
			continue
		}
		entry, ok := st.getBufferEntry(i)
		if !ok {
			isSwingHigh = false
			break
		}
		if entry.High >= mid.High {
			isSwingHigh = false
			break
		}
	}
	if isSwingHigh {
		st.RecentSwingHighs = append(st.RecentSwingHighs, SwingPoint{
			Price: mid.High,
			Time:  mid.Time,
			Index: st.BufferIdx - midOffset,
		})
		// Keep only recent swings
		if len(st.RecentSwingHighs) > 20 {
			st.RecentSwingHighs = st.RecentSwingHighs[1:]
		}
	}

	// Check if it's a swing low
	isSwingLow := true
	for i := 0; i < s.cfg.SwingLookback; i++ {
		if i == midOffset {
			continue
		}
		entry, ok := st.getBufferEntry(i)
		if !ok {
			isSwingLow = false
			break
		}
		if entry.Low <= mid.Low {
			isSwingLow = false
			break
		}
	}
	if isSwingLow {
		st.RecentSwingLows = append(st.RecentSwingLows, SwingPoint{
			Price: mid.Low,
			Time:  mid.Time,
			Index: st.BufferIdx - midOffset,
		})
		// Keep only recent swings
		if len(st.RecentSwingLows) > 20 {
			st.RecentSwingLows = st.RecentSwingLows[1:]
		}
	}
}

func (s *Nifty50Range) updateSRLevels(st *nifty50RangeState, currentTime time.Time) {
	// Cluster swing highs into resistance levels
	st.ResistanceLevels = s.clusterSwingPoints(st.RecentSwingHighs, "resistance", currentTime)

	// Cluster swing lows into support levels
	st.SupportLevels = s.clusterSwingPoints(st.RecentSwingLows, "support", currentTime)

	// Expire old levels
	st.ResistanceLevels = s.expireLevels(st.ResistanceLevels, currentTime)
	st.SupportLevels = s.expireLevels(st.SupportLevels, currentTime)
}

func (s *Nifty50Range) clusterSwingPoints(swings []SwingPoint, levelType string, currentTime time.Time) []SRLevel {
	if len(swings) == 0 {
		return nil
	}

	var levels []SRLevel

	for _, swing := range swings {
		// Check if this swing belongs to an existing level
		foundLevel := false
		for i := range levels {
			tolerance := levels[i].Price * s.cfg.TouchTolerancePct / 100
			if abs(swing.Price-levels[i].Price) <= tolerance {
				// Update existing level
				levels[i].Touches++
				levels[i].LastTouch = swing.Time
				levels[i].Price = (levels[i].Price*float64(levels[i].Touches-1) + swing.Price) / float64(levels[i].Touches)
				foundLevel = true
				break
			}
		}
		if !foundLevel {
			// Create new level
			levels = append(levels, SRLevel{
				Price:     swing.Price,
				Type:      levelType,
				Touches:   1,
				LastTouch: swing.Time,
				CreatedAt: swing.Time,
				Strength:  0.5,
			})
		}
	}

	// Calculate strength for each level
	for i := range levels {
		// Strength based on touches and recency
		touchScore := float64(levels[i].Touches) / 5.0
		if touchScore > 1.0 {
			touchScore = 1.0
		}
		ageMinutes := currentTime.Sub(levels[i].LastTouch).Minutes()
		recencyScore := 1.0 - (ageMinutes / 120.0) // Decay over 2 hours
		if recencyScore < 0 {
			recencyScore = 0
		}
		levels[i].Strength = (touchScore*0.6 + recencyScore*0.4)
	}

	// Filter by minimum touches
	var filtered []SRLevel
	for _, level := range levels {
		if level.Touches >= s.cfg.MinTouchesForLevel {
			filtered = append(filtered, level)
		}
	}

	return filtered
}

func (s *Nifty50Range) expireLevels(levels []SRLevel, currentTime time.Time) []SRLevel {
	var active []SRLevel
	for _, level := range levels {
		candlesSinceTouch := int(currentTime.Sub(level.LastTouch).Minutes())
		if candlesSinceTouch < s.cfg.LevelExpiryCandles {
			active = append(active, level)
		}
	}
	return active
}

func (s *Nifty50Range) detectActiveRange(st *nifty50RangeState, currentPrice float64) {
	// Find nearest support below current price
	var nearestSupport *SRLevel
	for i := range st.SupportLevels {
		if st.SupportLevels[i].Price < currentPrice {
			if nearestSupport == nil || st.SupportLevels[i].Price > nearestSupport.Price {
				nearestSupport = &st.SupportLevels[i]
			}
		}
	}

	// Find nearest resistance above current price
	var nearestResistance *SRLevel
	for i := range st.ResistanceLevels {
		if st.ResistanceLevels[i].Price > currentPrice {
			if nearestResistance == nil || st.ResistanceLevels[i].Price < nearestResistance.Price {
				nearestResistance = &st.ResistanceLevels[i]
			}
		}
	}

	// Create range if both levels exist
	if nearestSupport != nil && nearestResistance != nil {
		rangeSize := nearestResistance.Price - nearestSupport.Price
		rangeSizePct := (rangeSize / currentPrice) * 100
		rangeSizePts := int64(rangeSize * 100) // Convert to paise

		// ══════════════════════════════════════════════════════════════
		//  NEW LOGIC: Check points-based range FIRST, fallback to %
		// ══════════════════════════════════════════════════════════════
		isTradeableByPoints := false
		isTradeableByPct := false

		// Priority 1: Points-based check (PREFERRED)
		if s.cfg.MinRangeSizePts > 0 {
			if rangeSizePts >= s.cfg.MinRangeSizePts {
				isTradeableByPoints = true
				log.Printf("[strategy] %s: Range size %d paise (%.0f pts) >= min %d paise (%.0f pts) — ✅ TRADEABLE by points",
					s.Name(), rangeSizePts, rangeSize, s.cfg.MinRangeSizePts, float64(s.cfg.MinRangeSizePts)/100)
			} else {
				log.Printf("[strategy] %s: Range size %d paise (%.0f pts) < min %d paise (%.0f pts) — ❌ TOO SMALL",
					s.Name(), rangeSizePts, rangeSize, s.cfg.MinRangeSizePts, float64(s.cfg.MinRangeSizePts)/100)
			}
		}

		// Priority 2: Percentage-based fallback (if points check not configured)
		if !isTradeableByPoints && s.cfg.MinRangeSizePct > 0 {
			if rangeSizePct >= s.cfg.MinRangeSizePct {
				isTradeableByPct = true
				log.Printf("[strategy] %s: Range size %.2f%% >= min %.2f%% — ✅ TRADEABLE by percentage (fallback)",
					s.Name(), rangeSizePct, s.cfg.MinRangeSizePct)
			} else {
				log.Printf("[strategy] %s: Range size %.2f%% < min %.2f%% — ❌ TOO SMALL",
					s.Name(), rangeSizePct, s.cfg.MinRangeSizePct)
			}
		}

		// Create range if either check passes
		if isTradeableByPoints || isTradeableByPct {
			st.ActiveRange = &PriceRange{
				Support:    nearestSupport.Price,
				Resistance: nearestResistance.Price,
				MidPoint:   (nearestSupport.Price + nearestResistance.Price) / 2,
				Size:       rangeSize,
				SizePct:    rangeSizePct,
				Confirmed:  nearestSupport.Touches >= 2 && nearestResistance.Touches >= 2,
				CreatedAt:  time.Now(),
				TouchCount: nearestSupport.Touches + nearestResistance.Touches,
			}
			log.Printf("[strategy] %s: ✅ Active range CREATED: Support=%.2f, Resistance=%.2f, Size=%.0f pts (%.2f%%), Touches=%d",
				s.Name(), nearestSupport.Price, nearestResistance.Price, rangeSize, rangeSizePct, st.ActiveRange.TouchCount)
		} else {
			st.ActiveRange = nil
			if s.cfg.MinRangeSizePts > 0 {
				log.Printf("[strategy] %s: ❌ Range rejected: %.0f pts < %.0f pts required",
					s.Name(), rangeSize, float64(s.cfg.MinRangeSizePts)/100)
			} else {
				log.Printf("[strategy] %s: ❌ Range rejected: %.2f%% < %.2f%% required",
					s.Name(), rangeSizePct, s.cfg.MinRangeSizePct)
			}
		}
	} else {
		st.ActiveRange = nil
		if nearestSupport == nil {
			log.Printf("[strategy] %s: No support level found below current price %.2f", s.Name(), currentPrice)
		}
		if nearestResistance == nil {
			log.Printf("[strategy] %s: No resistance level found above current price %.2f", s.Name(), currentPrice)
		}
	}
}

// ── Entry Logic ──

func (s *Nifty50Range) evaluateEntry(candle model.TFCandle, st *nifty50RangeState) *Signal {
	if st.Side != SideNone {
		return nil
	}

	// Must have active range
	if st.ActiveRange == nil || !st.ActiveRange.Confirmed {
		return nil
	}

	currentPrice := float64(candle.Close)

	// Calculate entry zones
	supportZone := st.ActiveRange.Support * (1 + s.cfg.SupportEntryZonePct/100)
	resistanceZone := st.ActiveRange.Resistance * (1 - s.cfg.ResistanceEntryZonePct/100)

	// CALL Entry: Price near support
	if currentPrice <= supportZone && currentPrice >= st.ActiveRange.Support {
		st.Side = SideCall
		st.EntryPrice = candle.Close
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		st.EntryLevel = st.ActiveRange.Support
		st.TargetLevel = st.ActiveRange.Resistance
		st.StopLevel = st.ActiveRange.Support * (1 - s.cfg.StopBeyondLevelPct/100)

		reason := fmt.Sprintf("RANGE CALL entry: price=%d near support=%.2f, target=%.2f (range=%.2f%%)",
			candle.Close, st.ActiveRange.Support, st.ActiveRange.Resistance, st.ActiveRange.SizePct)
		log.Printf("[strategy] %s: %s", s.Name(), reason)

		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionBuy,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
			Side:         "CALL",
		}
	}

	// PUT Entry: Price near resistance
	if currentPrice >= resistanceZone && currentPrice <= st.ActiveRange.Resistance {
		st.Side = SidePut
		st.EntryPrice = candle.Close
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		st.EntryLevel = st.ActiveRange.Resistance
		st.TargetLevel = st.ActiveRange.Support
		st.StopLevel = st.ActiveRange.Resistance * (1 + s.cfg.StopBeyondLevelPct/100)

		reason := fmt.Sprintf("RANGE PUT entry: price=%d near resistance=%.2f, target=%.2f (range=%.2f%%)",
			candle.Close, st.ActiveRange.Resistance, st.ActiveRange.Support, st.ActiveRange.SizePct)
		log.Printf("[strategy] %s: %s", s.Name(), reason)

		return &Signal{
			StrategyName: s.Name(),
			Action:       ActionBuy,
			Token:        candle.Token,
			Exchange:     candle.Exchange,
			Qty:          s.qty,
			Reason:       reason,
			Side:         "PUT",
		}
	}

	return nil
}

// ── Exit Logic ──

func (s *Nifty50Range) evaluateExit(candle model.TFCandle, st *nifty50RangeState) *Signal {
	if st.Side == SideNone {
		return nil
	}

	currentPrice := float64(candle.Close)

	// Exit on breakout
	if st.Side == SideCall {
		breakoutLevel := st.EntryLevel * (1 - s.cfg.BreakoutConfirmPct/100)
		if currentPrice < breakoutLevel {
			reason := fmt.Sprintf("RANGE CALL exit: breakout below support (price=%d < %.2f)",
				candle.Close, breakoutLevel)
			log.Printf("[strategy] %s: %s", s.Name(), reason)
			s.resetPosition(st)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionExit,
				Token:        candle.Token,
				Exchange:     candle.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
	} else if st.Side == SidePut {
		breakoutLevel := st.EntryLevel * (1 + s.cfg.BreakoutConfirmPct/100)
		if currentPrice > breakoutLevel {
			reason := fmt.Sprintf("RANGE PUT exit: breakout above resistance (price=%d > %.2f)",
				candle.Close, breakoutLevel)
			log.Printf("[strategy] %s: %s", s.Name(), reason)
			s.resetPosition(st)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionExit,
				Token:        candle.Token,
				Exchange:     candle.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
	}

	return nil
}

func (s *Nifty50Range) checkFNOTargetAndSL(tick model.Tick, st *nifty50RangeState) *Signal {
	entryF := float64(st.FNOEntryPrice)
	priceF := float64(tick.Price)

	if tick.Price > st.FNOBestPrice {
		st.FNOBestPrice = tick.Price
	}

	// Target profit
	if s.cfg.FNOTargetProfitPct > 0 {
		gain := (priceF - entryF) / entryF * 100
		if gain >= s.cfg.FNOTargetProfitPct {
			reason := fmt.Sprintf("FNO TARGET PROFIT %s: price=%d gained %.2f%% (target=%.2f%%)",
				st.Side, tick.Price, gain, s.cfg.FNOTargetProfitPct)
			log.Printf("[strategy] %s: %s", s.Name(), reason)
			s.resetPosition(st)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionExit,
				Token:        tick.Token,
				Exchange:     tick.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
	}

	// Trailing SL
	if s.cfg.FNOTrailSLPct > 0 {
		bestF := float64(st.FNOBestPrice)
		gain := (bestF - entryF) / entryF * 100
		if gain >= s.cfg.FNOTrailStartPct {
			drop := (bestF - priceF) / bestF * 100
			if drop >= s.cfg.FNOTrailSLPct {
				reason := fmt.Sprintf("FNO TRAIL SL %s: price=%d fell %.2f%%", st.Side, tick.Price, drop)
				log.Printf("[strategy] %s: %s", s.Name(), reason)
				s.resetPosition(st)
				return &Signal{
					StrategyName: s.Name(),
					Action:       ActionExit,
					Token:        tick.Token,
					Exchange:     tick.Exchange,
					Qty:          s.qty,
					Reason:       reason,
				}
			}
		}
	}

	// Hard SL
	if s.cfg.FNOHardSLPct > 0 {
		drop := (entryF - priceF) / entryF * 100
		if drop >= s.cfg.FNOHardSLPct {
			reason := fmt.Sprintf("FNO HARD SL %s: price=%d fell %.2f%%", st.Side, tick.Price, drop)
			log.Printf("[strategy] %s: %s", s.Name(), reason)
			s.resetPosition(st)
			return &Signal{
				StrategyName: s.Name(),
				Action:       ActionExit,
				Token:        tick.Token,
				Exchange:     tick.Exchange,
				Qty:          s.qty,
				Reason:       reason,
			}
		}
	}

	return nil
}

// ── Helpers ──

func (s *Nifty50Range) getOrCreateRange(key string) *nifty50RangeState {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty50RangeState(s.cfg)
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50Range) resetPosition(st *nifty50RangeState) {
	st.Side = SideNone
	st.EntryPrice = 0
	st.EntryLevel = 0
	st.TargetLevel = 0
	st.StopLevel = 0
	st.IndexEntryPrice = 0
	st.IndexBestPrice = 0
	st.FNOEntryPrice = 0
	st.FNOBestPrice = 0
	if s.cfg.CooldownCandles > 0 {
		st.InCooldown = true
		st.CandlesSinceExit = 0
	}
}

func (s *Nifty50Range) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		st.Side = SideNone
		st.EntryPrice = 0
		st.FNOEntryPrice = 0
		st.FNOBestPrice = 0
		st.InCooldown = false
	}
}

func (s *Nifty50Range) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sigs []Signal
	for key, st := range s.instruments {
		if st.Side == SideNone {
			continue
		}
		exch, token := "", key
		if i := indexOf(key, ":"); i >= 0 {
			exch = key[:i]
			token = key[i+1:]
		}
		s.resetPosition(st)
		sigs = append(sigs, Signal{
			StrategyName: s.Name(),
			Action:       ActionExit,
			Token:        token,
			Exchange:     exch,
			Qty:          s.qty,
			Reason:       reason,
		})
	}
	return sigs
}

func (s *Nifty50Range) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty50RangeSnapshot(s.Name(), s.instruments)
}

func (s *Nifty50Range) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := unmarshalNifty50RangeSnapshot(data)
	if err != nil {
		return err
	}
	s.instruments = restoreNifty50RangeInstruments(snap)
	return nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func indexOf(s, substr string) int {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
