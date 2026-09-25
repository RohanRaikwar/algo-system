package analyst

import (
	"math"
	"time"
)

// IndicatorSnapshot holds the current values from the indicator engine
// needed to compute market state. All values are raw floats from the engine.
type IndicatorSnapshot struct {
	ADX      float64 // ADX(14) value
	PlusDI   float64 // +DI value (for direction)
	MinusDI  float64 // -DI value (for direction)
	ATR      float64 // ATR(14) raw value in paise
	Close    int64   // current close price in paise (for ATR%)
	RSI      float64 // RSI(14) value
	PrevRSI  float64 // RSI 3 bars ago (for slope)
	EMA9     float64 // EMA(9) value
	EMA21    float64 // EMA(21) value
	VWAP     float64 // VWAP value
}

// MarketStateDetector computes market state from indicator confluence.
// Implements the 5-indicator system from market_state_detector.md.
type MarketStateDetector struct {
	prevState      MarketState
	stateChangedAt time.Time
}

// NewMarketStateDetector creates a new detector.
func NewMarketStateDetector() *MarketStateDetector {
	return &MarketStateDetector{
		prevState: MarketStateMixed,
	}
}

// Detect evaluates the 5 indicators and returns the market state event.
// Requires 3 of 5 indicators to agree before declaring a state.
func (d *MarketStateDetector) Detect(snap IndicatorSnapshot, token, exchange string, tf int, ts time.Time) MarketStateEvent {
	votes := make([]IndicatorVote, 0, 5)

	// 1. ADX/DI — Trend Strength
	adxVote := d.voteADX(snap.ADX)
	votes = append(votes, IndicatorVote{Name: "ADX", State: adxVote, Value: snap.ADX})

	// 2. ATR% — Volatility Filter
	atrPct := d.calcATRPct(snap.ATR, snap.Close)
	atrVote := d.voteATRPct(atrPct)
	votes = append(votes, IndicatorVote{Name: "ATR_PCT", State: atrVote, Value: atrPct})

	// 3. RSI Slope — Momentum
	rsiSlope := snap.RSI - snap.PrevRSI
	rsiVote := d.voteRSI(snap.RSI, rsiSlope)
	votes = append(votes, IndicatorVote{Name: "RSI_SLOPE", State: rsiVote, Value: rsiSlope})

	// 4. EMA 9/21 Spread
	emaSpread := d.calcEMASpread(snap.EMA9, snap.EMA21)
	emaVote := d.voteEMASpread(emaSpread)
	votes = append(votes, IndicatorVote{Name: "EMA_SPREAD", State: emaVote, Value: emaSpread})

	// 5. VWAP Deviation
	vwapDev := d.calcVWAPDev(float64(snap.Close), snap.VWAP)
	vwapVote := d.voteVWAPDev(vwapDev)
	votes = append(votes, IndicatorVote{Name: "VWAP_DEV", State: vwapVote, Value: vwapDev})

	// Count confluence
	bullish, bearish, sideways, choppy := 0, 0, 0, 0
	for _, v := range votes {
		switch v.State {
		case MarketStateTrendingUp:
			bullish++
		case MarketStateTrendingDown:
			bearish++
		case MarketStateSideways:
			sideways++
		case MarketStateChoppy:
			choppy++
		}
	}

	// Determine state: need 3 of 5 to agree
	state := MarketStateMixed
	confluence := 0
	direction := "NEUTRAL"

	trending := bullish + bearish
	if trending >= 3 {
		if bullish >= bearish {
			state = MarketStateTrendingUp
			direction = "BULLISH"
			confluence = bullish
		} else {
			state = MarketStateTrendingDown
			direction = "BEARISH"
			confluence = bearish
		}
	} else if sideways >= 3 {
		state = MarketStateSideways
		confluence = sideways
	} else if choppy >= 3 {
		state = MarketStateChoppy
		confluence = choppy
	} else {
		// Mixed — find the highest count
		maxCount := max4(bullish, bearish, sideways, choppy)
		confluence = maxCount
	}

	// Track state changes
	stateChangedAt := d.stateChangedAt
	if state != d.prevState {
		stateChangedAt = ts
		d.stateChangedAt = ts
	}
	prevState := d.prevState
	d.prevState = state

	return MarketStateEvent{
		State:          state,
		Confluence:     confluence,
		Direction:      direction,
		Votes:          votes,
		Token:          token,
		Exchange:       exchange,
		TF:             tf,
		TS:             ts,
		PreviousState:  prevState,
		StateChangedAt: stateChangedAt,
	}
}

// ── Indicator vote functions ──

// voteADX: > 25 = trending, 15-25 = sideways, < 15 = choppy
func (d *MarketStateDetector) voteADX(adx float64) MarketState {
	if adx > 25 {
		return MarketStateTrendingUp // direction resolved by DI later
	}
	if adx >= 15 {
		return MarketStateSideways
	}
	return MarketStateChoppy
}

// voteATRPct: > 0.80% = trending, 0.30-0.80% = sideways, < 0.30% = choppy
func (d *MarketStateDetector) voteATRPct(atrPct float64) MarketState {
	if atrPct > 0.80 {
		return MarketStateTrendingUp
	}
	if atrPct >= 0.30 {
		return MarketStateSideways
	}
	return MarketStateChoppy
}

// voteRSI: RSI 40-60 with flat slope = choppy, >55 rising = bullish, <45 falling = bearish
func (d *MarketStateDetector) voteRSI(rsi, slope float64) MarketState {
	if rsi >= 40 && rsi <= 60 && math.Abs(slope) < 0.5 {
		return MarketStateChoppy
	}
	if rsi > 55 && slope > 0.3 {
		return MarketStateTrendingUp
	}
	if rsi < 45 && slope < -0.3 {
		return MarketStateTrendingDown
	}
	return MarketStateSideways
}

// voteEMASpread: > +0.10% = uptrend, < -0.10% = downtrend, otherwise sideways
func (d *MarketStateDetector) voteEMASpread(spread float64) MarketState {
	if spread > 0.10 {
		return MarketStateTrendingUp
	}
	if spread < -0.10 {
		return MarketStateTrendingDown
	}
	return MarketStateSideways
}

// voteVWAPDev: > +0.15% = bullish, < -0.15% = bearish, otherwise ranging
func (d *MarketStateDetector) voteVWAPDev(dev float64) MarketState {
	if dev > 0.15 {
		return MarketStateTrendingUp
	}
	if dev < -0.15 {
		return MarketStateTrendingDown
	}
	return MarketStateSideways
}

// ── Calculation helpers ──

// calcATRPct returns ATR as percentage of close: ATR(14) / Close * 100
func (d *MarketStateDetector) calcATRPct(atr float64, closePaise int64) float64 {
	if closePaise <= 0 {
		return 0
	}
	return (atr / float64(closePaise)) * 100.0
}

// calcEMASpread returns (EMA9 - EMA21) / EMA21 * 100
func (d *MarketStateDetector) calcEMASpread(ema9, ema21 float64) float64 {
	if ema21 == 0 {
		return 0
	}
	return ((ema9 - ema21) / ema21) * 100.0
}

// calcVWAPDev returns (Close - VWAP) / VWAP * 100
func (d *MarketStateDetector) calcVWAPDev(close, vwap float64) float64 {
	if vwap == 0 {
		return 0
	}
	return ((close - vwap) / vwap) * 100.0
}

func max4(a, b, c, d int) int {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	if d > m {
		m = d
	}
	return m
}
