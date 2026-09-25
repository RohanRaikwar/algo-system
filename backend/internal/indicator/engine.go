package indicator

import (
	"context"
	"time"

	"trading-systemv1/internal/model"
)

// IndicatorConfig specifies a single indicator to compute.
type IndicatorConfig struct {
	Type   string // "SMA", "EMA", "SMMA", "RSI"
	Period int
}

// TFIndicatorConfig groups indicator configs for a specific timeframe.
type TFIndicatorConfig struct {
	TF         int // timeframe in seconds
	Indicators []IndicatorConfig
}

// tokenIndicators holds live indicator instances for one token within a TF.
type tokenIndicators struct {
	indicators []Indicator
	configs    []IndicatorConfig

	// lastTS[i] is the bucket time of the last candle applied to
	// indicators[i]. A candle at or before it is a replay (SQLite warm-up,
	// Redis backfill or delta replay, redelivery) and is not applied to that
	// indicator again. Kept per indicator so one added by a reload, or
	// missing from a restored snapshot, can be warmed from history without
	// double-counting into the others.
	lastTS []time.Time
}

// Engine computes multiple indicators across multiple TFs for multiple tokens.
// Not safe for concurrent use: exactly one goroutine may own it (indengine's
// processLoop); every other caller must go through that owner.
type Engine struct {
	configs []TFIndicatorConfig

	// state[tfIdx][tokenKey] → *tokenIndicators
	state []map[string]*tokenIndicators

	// tfIndex maps TF seconds → index in configs/state for O(1) lookup
	tfIndex map[int]int
}

// NewEngine creates an indicator engine with the given per-TF indicator configs.
func NewEngine(configs []TFIndicatorConfig) *Engine {
	state := make([]map[string]*tokenIndicators, len(configs))
	tfIndex := make(map[int]int, len(configs))
	for i := range state {
		state[i] = make(map[string]*tokenIndicators, 64)
		tfIndex[configs[i].TF] = i
	}
	return &Engine{
		configs: configs,
		state:   state,
		tfIndex: tfIndex,
	}
}

// Process takes a finalized TF candle and computes all indicators for that TF + token.
// Returns indicator results (may include not-ready indicators with Ready=false).
func (e *Engine) Process(tfc model.TFCandle) []model.IndicatorResult {
	// O(1) TF lookup via index map
	tfIdx, ok := e.tfIndex[tfc.TF]
	if !ok {
		return nil // TF not configured for indicators
	}

	key := tfc.Key()
	ti, exists := e.state[tfIdx][key]
	if !exists {
		// First candle for this token + TF — create indicator instances
		ti = e.createTokenIndicators(tfIdx)
		e.state[tfIdx][key] = ti
	}

	// Create a model.Candle from the TFCandle for indicator Update()
	candle := model.Candle{
		Token:    tfc.Token,
		Exchange: tfc.Exchange,
		TS:       tfc.TS,
		Open:     tfc.Open,
		High:     tfc.High,
		Low:      tfc.Low,
		Close:    tfc.Close,
		Volume:   tfc.Volume,
	}

	// Update all indicators that have not seen this candle yet and collect
	// their results (one pass).
	results := make([]model.IndicatorResult, 0, len(ti.indicators))
	for i, ind := range ti.indicators {
		if last := ti.lastTS[i]; !last.IsZero() && !tfc.TS.After(last) {
			continue // replayed candle
		}
		ind.Update(candle)
		ti.lastTS[i] = tfc.TS
		cfg := ti.configs[i]
		results = append(results, model.IndicatorResult{
			Name:     ind.Name() + "_" + model.Itoa(cfg.Period),
			Token:    tfc.Token,
			Exchange: tfc.Exchange,
			TF:       tfc.TF,
			Value:    ind.Value(),
			TS:       tfc.TS,
			Ready:    ind.Ready(),
		})
	}
	if len(results) == 0 {
		return nil
	}

	return results
}

// IsReplay reports whether every indicator for tfc's token and TF has
// already applied a candle at or after tfc.TS, so Process would skip it.
func (e *Engine) IsReplay(tfc model.TFCandle) bool {
	tfIdx, ok := e.tfIndex[tfc.TF]
	if !ok {
		return false
	}
	ti, ok := e.state[tfIdx][tfc.Key()]
	if !ok || len(ti.lastTS) == 0 {
		return false
	}
	for _, last := range ti.lastTS {
		if last.IsZero() || tfc.TS.After(last) {
			return false
		}
	}
	return true
}

// ProcessPeek computes live indicator values for a forming TF candle using Peek().
// Does NOT mutate indicator state — safe for streaming updates every second.
// Returns nil if token hasn't been seen before (need at least one Process first).
func (e *Engine) ProcessPeek(tfc model.TFCandle) []model.IndicatorResult {
	// O(1) TF lookup via index map
	tfIdx, ok := e.tfIndex[tfc.TF]
	if !ok {
		return nil
	}

	key := tfc.Key()
	ti, exists := e.state[tfIdx][key]
	if !exists {
		// Token hasn't been seeded by a completed candle yet — skip peek.
		// indengine calls Process() on completed candles first, so this is safe.
		return nil
	}

	results := make([]model.IndicatorResult, 0, len(ti.indicators))
	for i, ind := range ti.indicators {
		cfg := ti.configs[i]
		results = append(results, model.IndicatorResult{
			Name:     ind.Name() + "_" + model.Itoa(cfg.Period),
			Token:    tfc.Token,
			Exchange: tfc.Exchange,
			TF:       tfc.TF,
			Value:    ind.Peek(tfc.Close),
			TS:       tfc.TS,
			Ready:    ind.Ready(),
			Live:     true,
		})
	}
	return results
}

// Run consumes TF candles and emits indicator results. Blocks until ctx done.
func (e *Engine) Run(ctx context.Context, tfCandleCh <-chan model.TFCandle, resultCh chan<- model.IndicatorResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-tfCandleCh:
			if !ok {
				return
			}
			if tfc.Forming {
				continue // skip forming candles
			}
			results := e.Process(tfc)
			for _, r := range results {
				select {
				case resultCh <- r:
				default:
					// drop if channel full
				}
			}
		}
	}
}

// createTokenIndicators creates fresh indicator instances for a TF config.
func (e *Engine) createTokenIndicators(tfIdx int) *tokenIndicators {
	cfg := e.configs[tfIdx]
	inds := make([]Indicator, len(cfg.Indicators))
	for i, ic := range cfg.Indicators {
		switch ic.Type {
		case "SMA":
			inds[i] = NewSMA(ic.Period)
		case "EMA":
			inds[i] = NewEMA(ic.Period)
		case "SMMA":
			inds[i] = NewSMMA(ic.Period)
		case "RSI":
			inds[i] = NewRSI(ic.Period)
		case "ADX":
			inds[i] = NewADX(ic.Period)
		case "ATR":
			inds[i] = NewATR(ic.Period)
		case "VWAP":
			inds[i] = NewVWAP()
		case "BOLLINGER":
			inds[i] = NewBollinger(ic.Period, 2.0)
		default:
			inds[i] = NewSMA(ic.Period) // fallback
		}
	}
	return &tokenIndicators{
		indicators: inds,
		configs:    cfg.Indicators,
		lastTS:     make([]time.Time, len(inds)),
	}
}
