package analyst

import (
	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

// Engine is the core analyst computation engine.
// It owns its own indicator engine, market state detector, S/R detector,
// and breakout detector. Fed TFCandles, it produces analysis events.
type Engine struct {
	indEngine *indicator.Engine
	msd       *MarketStateDetector
	srd       *SRDetector
	bod       *BreakoutDetector

	// RSI history for slope calculation (ring buffer of last 4 values)
	rsiHistory [4]float64
	rsiCount   int

	// last is the most recent result, returned again for a replayed candle.
	last AnalysisResult
}

// NewEngine creates a new analyst engine with its own indicator instances.
// Indicators: ADX(14), ATR(14), RSI(14), EMA(9), EMA(21), VWAP, BB(20)
func NewEngine() *Engine {
	indConfigs := []indicator.TFIndicatorConfig{
		{
			TF: 60, // 1-minute candles
			Indicators: []indicator.IndicatorConfig{
				{Type: "ADX", Period: 14},
				{Type: "ATR", Period: 14},
				{Type: "RSI", Period: 14},
				{Type: "EMA", Period: 9},
				{Type: "EMA", Period: 21},
				{Type: "VWAP", Period: 0},
				{Type: "BOLLINGER", Period: 20},
			},
		},
	}

	return &Engine{
		indEngine: indicator.NewEngine(indConfigs),
		msd:       NewMarketStateDetector(),
		srd:       NewSRDetector(),
		bod:       NewBreakoutDetector(),
	}
}

// AnalysisResult holds the combined output of one analysis cycle.
type AnalysisResult struct {
	State    MarketStateEvent
	Levels   LevelUpdateEvent
	Breakout *BreakoutEvent // nil if no breakout event this cycle
}

// Process takes a finalized 1m TFCandle and runs the full analysis pipeline.
// Returns the combined result with market state, S/R levels, and breakout events.
func (e *Engine) Process(tfc model.TFCandle) AnalysisResult {
	// A candle already analysed (pending recovery, warm-up overlap) must not
	// run the detectors again: its indicators would be skipped and the
	// detectors would see an empty snapshot, and S/R would count it twice.
	if e.indEngine.IsReplay(tfc) {
		res := e.last
		res.Breakout = nil // a breakout is an event: never report it twice
		return res
	}

	// Step 1: Compute indicators
	results := e.indEngine.Process(tfc)

	// Step 2: Extract indicator values
	snap := e.extractSnapshot(results, tfc)

	// Step 3: Detect market state
	stateEvent := e.msd.Detect(snap, tfc.Token, tfc.Exchange, tfc.TF, tfc.TS)

	// Step 4: Update S/R levels
	e.srd.Update(tfc)
	levels := e.srd.ActiveLevels()
	levelEvent := LevelUpdateEvent{
		Levels:   levels,
		Token:    tfc.Token,
		Exchange: tfc.Exchange,
		TF:       tfc.TF,
		TS:       tfc.TS,
	}

	// Step 5: Check for breakouts
	atrPct := e.msd.calcATRPct(snap.ATR, snap.Close)
	avgVol := e.srd.avgVolume()
	bbw := e.extractBBW(results, tfc)
	breakoutEvent := e.bod.Update(tfc, levels, snap.ADX, atrPct, bbw, avgVol)

	e.last = AnalysisResult{
		State:    stateEvent,
		Levels:   levelEvent,
		Breakout: breakoutEvent,
	}
	return e.last
}

// extractSnapshot builds an IndicatorSnapshot from raw indicator results.
func (e *Engine) extractSnapshot(results []model.IndicatorResult, tfc model.TFCandle) IndicatorSnapshot {
	snap := IndicatorSnapshot{
		Close: tfc.Close,
	}

	for _, r := range results {
		if !r.Ready {
			continue
		}
		switch r.Name {
		case "ADX_14":
			snap.ADX = r.Value
		case "ATR_14":
			snap.ATR = r.Value
		case "RSI_14":
			snap.RSI = r.Value
			// Track RSI history for slope (RSI now - RSI 3 bars ago)
			e.rsiHistory[e.rsiCount%4] = r.Value
			e.rsiCount++
			if e.rsiCount >= 4 {
				snap.PrevRSI = e.rsiHistory[(e.rsiCount-3)%4]
			} else {
				snap.PrevRSI = r.Value
			}
		case "EMA_9":
			snap.EMA9 = r.Value
		case "EMA_21":
			snap.EMA21 = r.Value
		case "VWAP_0":
			snap.VWAP = r.Value
		}
	}

	return snap
}

// extractBBW extracts Bollinger Band Width from indicator results.
// BBW = (Upper - Lower) / Middle × 100
func (e *Engine) extractBBW(results []model.IndicatorResult, tfc model.TFCandle) float64 {
	// The Bollinger indicator returns the middle band value.
	// For BBW we need the full band, which requires the Bollinger indicator
	// to expose upper/lower. For now, use a proxy: ATR% as volatility measure.
	// TODO: Extend Bollinger indicator to expose bandwidth.
	for _, r := range results {
		if r.Name == "BB_20" && r.Ready {
			// Bollinger.Value() returns the middle band (SMA).
			// Use ATR as a proxy for bandwidth until Bollinger is extended.
			middle := r.Value
			if middle > 0 {
				// Approximate BBW using ATR: BBW ≈ 2 × ATR / middle × 100
				for _, r2 := range results {
					if r2.Name == "ATR_14" && r2.Ready {
						return (2.0 * r2.Value / middle) * 100.0
					}
				}
			}
		}
	}
	return 0
}
