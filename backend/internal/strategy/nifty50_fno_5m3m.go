package strategy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 FnO 5M3M — Multi-Timeframe Strategy
//
//  Entry:  5-minute TF — SMA21 × EMA6 crossover
//          CALL when SMA21 > EMA6, PUT when SMA21 < EMA6
//  Exit:   3-minute TF — SMA21 × EMA6 crossover (opposite direction)
//  SL:     Trail SL + Hard SL on BOTH index price AND FNO option price
//  Filter: Resistance/support proximity blocking
//
//  Indicator mapping:
//    SMA21 (period 21) — baseline SMA entry signal
//    EMA6  (period 6)  — medium EMA confirmation
//
//  Multi-TF: Entry signals on 5m, Exit signals on 3m
// ════════════════════════════════════════════════════════════════════

// Nifty50FnO5M3M implements the NIFTY50 FNO 5M3M strategy.
type Nifty50FnO5M3M struct {
	mu          sync.Mutex
	instruments map[string]*nifty50FnO5M3MState
	qty         int64
	cfg         Nifty50FnO5M3MConfig
}

// NewNifty50FnO5M3M creates a new strategy with default config.
func NewNifty50FnO5M3M(qty int64) *Nifty50FnO5M3M {
	return NewNifty50FnO5M3MWithConfig(qty, DefaultNifty50FnO5M3MConfig())
}

// NewNifty50FnO5M3MWithConfig creates a strategy with custom configuration.
func NewNifty50FnO5M3MWithConfig(qty int64, cfg Nifty50FnO5M3MConfig) *Nifty50FnO5M3M {
	return &Nifty50FnO5M3M{
		instruments: make(map[string]*nifty50FnO5M3MState, 64),
		qty:         qty,
		cfg:         cfg,
	}
}

func (s *Nifty50FnO5M3M) Name() string                { return "NIFTY50_FNO_5M3M" }
func (s *Nifty50FnO5M3M) Config() Nifty50FnO5M3MConfig { return s.cfg }

// SetFNOTokens updates the FNO option tokens at runtime.
func (s *Nifty50FnO5M3M) SetFNOTokens(callToken, putToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.FNOCallToken = callToken
	s.cfg.FNOPutToken = putToken
}

// SetResistanceLevels updates resistance levels at runtime.
func (s *Nifty50FnO5M3M) SetResistanceLevels(levels []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.ResistanceLevels = levels
}

// SetSupportLevels updates support levels at runtime.
func (s *Nifty50FnO5M3M) SetSupportLevels(levels []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.SupportLevels = levels
}

// SetFNOEntryPrice records the live FNO option premium at entry.
func (s *Nifty50FnO5M3M) SetFNOEntryPrice(price int64) {
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
func (s *Nifty50FnO5M3M) CurrentFNOPosition() *LiveFNOPosition {
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

// Timeframe constants
const (
	tf5m5M3M = 300 // 5-minute timeframe in seconds (entry)
	tf3m5M3M = 180 // 3-minute timeframe in seconds (exit)
)

func (s *Nifty50FnO5M3M) OnTFCandle(candle model.TFCandle) *Signal {
	// Only process 5m (entry) and 3m (exit) candles
	if candle.TF != tf5m5M3M && candle.TF != tf3m5M3M {
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

	// Dedup: enforce monotonic candle close timestamp per timeframe
	closeTS := candle.TS
	if candle.TF == tf5m5M3M {
		if !st.Last5mCloseTS.IsZero() && !closeTS.After(st.Last5mCloseTS) {
			return nil
		}
		st.Last5mCloseTS = closeTS
	} else if candle.TF == tf3m5M3M {
		if !st.Last3mCloseTS.IsZero() && !closeTS.After(st.Last3mCloseTS) {
			return nil
		}
		st.Last3mCloseTS = closeTS
	}

	// Track cooldown
	if st.InCooldown {
		st.CandlesSinceExit++
		if s.cfg.CooldownCandles > 0 && st.CandlesSinceExit >= s.cfg.CooldownCandles {
			st.InCooldown = false
			st.CandlesSinceExit = 0
		}
	}

	// Feed indicators based on timeframe
	if candle.TF == tf5m5M3M {
		st.updateIndicators5m(toCandle1m(candle))
	} else if candle.TF == tf3m5M3M {
		st.updateIndicators3m(toCandle1m(candle))
	}

	// Try exit first (3-minute TF)
	if candle.TF == tf3m5M3M && inMarketHours {
		if !st.bufferReady3m() {
			return nil
		}
		_, curr3m := st.prevAndCurr3m()
		sma21CrossedAboveEMA6_3m := st.sma21CrossedAboveEMA6_3m()
		sma21CrossedBelowEMA6_3m := st.sma21CrossedBelowEMA6_3m()

		if sig := s.evaluateExit(candle, st, curr3m, sma21CrossedAboveEMA6_3m, sma21CrossedBelowEMA6_3m); sig != nil {
			return sig
		}
	}

	// Try entry (5-minute TF)
	if candle.TF == tf5m5M3M && inMarketHours && inEntryWindow {
		if !st.bufferReady5m() {
			return nil
		}
		_, curr5m := st.prevAndCurr5m()
		sma21CrossedAboveEMA6_5m := st.sma21CrossedAboveEMA6_5m()
		sma21CrossedBelowEMA6_5m := st.sma21CrossedBelowEMA6_5m()

		if sig := s.evaluateEntry(candle, st, curr5m, candleIST,
			sma21CrossedAboveEMA6_5m, sma21CrossedBelowEMA6_5m); sig != nil {
			return sig
		}
	}

	return nil
}

// OnTick implements dual-layer stop loss checking on every tick.
func (s *Nifty50FnO5M3M) OnTick(tick model.Tick) *Signal {
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
func (s *Nifty50FnO5M3M) checkIndexSL(tick model.Tick, st *nifty50FnO5M3MState) *Signal {
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
				s.resetPosition(st)
				return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
					Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
			}
		}
	}

	return nil
}

// checkFNOSL checks Trail SL and Hard SL on the FNO option premium.
func (s *Nifty50FnO5M3M) checkFNOSL(tick model.Tick, st *nifty50FnO5M3MState) *Signal {
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
			s.resetPosition(st)
			return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: slSide,
				Token: tick.Token, Exchange: tick.Exchange, Qty: s.qty, Reason: reason}
		}
	}

	return nil
}

// ── Exit Logic (3-minute TF) ──

func (s *Nifty50FnO5M3M) evaluateExit(
	candle model.TFCandle, st *nifty50FnO5M3MState,
	curr3m nifty50FnO5M3MBufferEntry,
	sma21CrossedAboveEMA6, sma21CrossedBelowEMA6 bool,
) *Signal {
	if st.Side == SideNone {
		return nil
	}

	// PUT Exit: SMA21 crosses above EMA6 on 3m
	if st.Side == SidePut && sma21CrossedAboveEMA6 {
		reason := fmt.Sprintf("3m PUT exit: SMA21(%.0f) crossed above EMA6(%.0f), close=%d",
			curr3m.SMA21, curr3m.EMA6, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		s.resetPosition(st)
		return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SidePut,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	// CALL Exit: SMA21 crosses below EMA6 on 3m
	if st.Side == SideCall && sma21CrossedBelowEMA6 {
		reason := fmt.Sprintf("3m CALL exit: SMA21(%.0f) crossed below EMA6(%.0f), close=%d",
			curr3m.SMA21, curr3m.EMA6, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		s.resetPosition(st)
		return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: SideCall,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	return nil
}

// ── Entry Logic (5-minute TF) ──

func (s *Nifty50FnO5M3M) evaluateEntry(
	candle model.TFCandle, st *nifty50FnO5M3MState,
	curr5m nifty50FnO5M3MBufferEntry,
	candleIST time.Time,
	sma21CrossedAboveEMA6, sma21CrossedBelowEMA6 bool,
) *Signal {
	if st.Side != SideNone {
		return nil
	}

	if st.InCooldown {
		return nil
	}

	// CALL Entry: SMA21 crosses above EMA6 on 5m
	if sma21CrossedAboveEMA6 {
		if isNearResistance(float64(candle.Close), s.cfg.ResistanceLevels, s.cfg.ResistanceDistancePct) {
			log.Printf("[strategy] %s: %s CALL entry BLOCKED — near resistance, close=%d",
				s.Name(), candle.Key(), candle.Close)
			return nil
		}
		st.Side = SideCall
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		reason := fmt.Sprintf("5m CALL entry: SMA21(%.0f) crossed above EMA6(%.0f), close=%d",
			curr5m.SMA21, curr5m.EMA6, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	// PUT Entry: SMA21 crosses below EMA6 on 5m
	if sma21CrossedBelowEMA6 {
		if isNearSupport(float64(candle.Close), s.cfg.SupportLevels, s.cfg.SupportDistancePct) {
			log.Printf("[strategy] %s: %s PUT entry BLOCKED — near support, close=%d",
				s.Name(), candle.Key(), candle.Close)
			return nil
		}
		st.Side = SidePut
		st.IndexEntryPrice = candle.Close
		st.IndexBestPrice = candle.Close
		reason := fmt.Sprintf("5m PUT entry: SMA21(%.0f) crossed below EMA6(%.0f), close=%d",
			curr5m.SMA21, curr5m.EMA6, candle.Close)
		log.Printf("[strategy] %s: %s %s", s.Name(), candle.Key(), reason)
		return &Signal{StrategyName: s.Name(), Action: ActionBuy, Side: SidePut,
			Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty, Reason: reason}
	}

	return nil
}

// ── Helpers ──

func (s *Nifty50FnO5M3M) getOrCreate(key string) *nifty50FnO5M3MState {
	st, ok := s.instruments[key]
	if !ok {
		st = newNifty50FnO5M3MState(s.cfg)
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50FnO5M3M) resetPosition(st *nifty50FnO5M3MState) {
	st.Side = SideNone
	st.IndexEntryPrice = 0
	st.IndexBestPrice = 0
	st.FNOEntryPrice = 0
	st.FNOBestPrice = 0
	if s.cfg.CooldownCandles > 0 {
		st.InCooldown = true
		st.CandlesSinceExit = 0
	}
}

// ResetPositions resets position state while keeping indicators warm.
func (s *Nifty50FnO5M3M) ResetPositions() {
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
	}
}

// ForceExitAll fires an ActionExit signal for every open position.
func (s *Nifty50FnO5M3M) ForceExitAll(reason string) []Signal {
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
func (s *Nifty50FnO5M3M) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalNifty50FnO5M3MSnapshot(s.Name(), s.instruments)
}

// Restore recovers strategy state from a snapshot.
func (s *Nifty50FnO5M3M) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := unmarshalNifty50FnO5M3MSnapshot(data)
	if err != nil {
		return err
	}
	s.instruments = restoreNifty50FnO5M3MInstruments(snap)
	return nil
}
