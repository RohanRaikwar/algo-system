package strategy

// ════════════════════════════════════════════════════════════════════
//  Candle reversal patterns used by the range strategies.
//
//  All comparisons are integer paise; no float intermediates.
//  Proportions are expressed as "a*100 >= b*pct" to stay in int64.
// ════════════════════════════════════════════════════════════════════

// ohlcv is one bar in paise.
type ohlcv struct {
	Open   int64 `json:"o"`
	High   int64 `json:"h"`
	Low    int64 `json:"l"`
	Close  int64 `json:"c"`
	Volume int64 `json:"v"`
}

func (b ohlcv) rng() int64 { return b.High - b.Low }

func (b ohlcv) body() int64 {
	if b.Close >= b.Open {
		return b.Close - b.Open
	}
	return b.Open - b.Close
}

func (b ohlcv) upperWick() int64 { return b.High - maxInt64(b.Open, b.Close) }
func (b ohlcv) lowerWick() int64 { return minInt64(b.Open, b.Close) - b.Low }
func (b ohlcv) bullish() bool    { return b.Close > b.Open }
func (b ohlcv) bearish() bool    { return b.Close < b.Open }

// isHammer: small body in the upper third, lower wick at least twice the
// body and at least 60% of the bar range, small upper wick.
func isHammer(b ohlcv) bool {
	r := b.rng()
	if r <= 0 {
		return false
	}
	body := b.body()
	lw := b.lowerWick()
	uw := b.upperWick()
	return body*100 <= r*35 && lw >= 2*body && lw*100 >= r*60 && uw*100 <= r*15
}

// isShootingStar is the mirror of isHammer.
func isShootingStar(b ohlcv) bool {
	r := b.rng()
	if r <= 0 {
		return false
	}
	body := b.body()
	lw := b.lowerWick()
	uw := b.upperWick()
	return body*100 <= r*35 && uw >= 2*body && uw*100 >= r*60 && lw*100 <= r*15
}

// isBullishEngulfing: bearish prev, bullish cur whose body covers prev's body.
func isBullishEngulfing(prev, cur ohlcv) bool {
	return prev.bearish() && cur.bullish() &&
		cur.Open <= prev.Close && cur.Close >= prev.Open && cur.body() > prev.body()
}

// isBearishEngulfing: bullish prev, bearish cur whose body covers prev's body.
func isBearishEngulfing(prev, cur ohlcv) bool {
	return prev.bullish() && cur.bearish() &&
		cur.Open >= prev.Close && cur.Close <= prev.Open && cur.body() > prev.body()
}

// bullishReversal reports a hammer or a bullish engulfing on cur.
func bullishReversal(prev, cur ohlcv) bool {
	return isHammer(cur) || isBullishEngulfing(prev, cur)
}

// bearishReversal reports a shooting star or a bearish engulfing on cur.
func bearishReversal(prev, cur ohlcv) bool {
	return isShootingStar(cur) || isBearishEngulfing(prev, cur)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func absInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
