package strategy

import (
	"sort"
	"time"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  Range context — multi-timeframe "situation" for range strategies.
//
//  Trades trigger on 1m closes; the situation comes from higher TFs
//  built from the same 1m stream (NIFTY_RANGE_MARKET_GUIDE.md):
//    • 15m: ADX (trend strength), EMA20 slope (flatness), Bollinger
//      squeeze, swing support/resistance + previous-day high/low.
//    • 5m:  RSI (entry filter).
//    • 1m:  volume average and the last two bars for reversal patterns.
//
//  All prices are int64 paise. Nothing here reads the wall clock, so
//  replaying the same candles always yields the same state.
// ════════════════════════════════════════════════════════════════════

// ist is a fixed +05:30 zone: no tzdata dependency, deterministic.
var ist = time.FixedZone("IST", 5*3600+30*60)

// sessionOpenMin is 09:15 IST in minutes after midnight.
const sessionOpenMin = 9*60 + 15

// RangeClass buckets a range by width (guide: "Range Size and Trading Approach").
type RangeClass string

const (
	RangeNone   RangeClass = ""
	RangeNarrow RangeClass = "NARROW" // < 100 pts
	RangeMedium RangeClass = "MEDIUM" // 100-200 pts
	RangeWide   RangeClass = "WIDE"   // > 200 pts
)

// RangeContextConfig configures regime and level detection.
type RangeContextConfig struct {
	ADXPeriod     int     // 14 on 15m bars
	MaxADX        float64 // range regime only while ADX < this (guide: 25)
	EMAPeriod     int     // 20 on 15m bars
	FlatSlopeBars int     // compare EMA now vs this many 15m bars ago
	FlatSlopePts  int64   // |EMA change| over FlatSlopeBars, paise
	BBPeriod      int     // 20
	BBMult        float64 // 2.0
	BBSqueezePct  float64 // bandwidth % below which bands are "squeezing"
	RSIPeriod     int     // 14 on 5m bars
	VolAvgBars    int     // 1m bars in the volume average
	VolAvgBars5   int     // 5m bars in the 5m volume average
	ATRPeriod     int     // 14 on 15m bars (stop sizing)
	// RequireFlatEMA adds "EMA20 flat" to the range regime. Off by default:
	// ADX < MaxADX plus confirmed levels already define the range, and each
	// extra AND filter cuts the trade count (guide lists them as options).
	RequireFlatEMA bool

	// EntryMinutes (2 or 3) builds an extra N-minute series for entries;
	// 1 and 5 use the 1m and 5m series that always exist.
	EntryMinutes int
	// HTFEMAPeriod is the EMA on 1h bars for the higher-timeframe trend.
	HTFEMAPeriod int
	// HTFLevels adds 1h swing highs/lows to support/resistance.
	HTFLevels bool

	SwingWindow       int   // 15m bars each side for a swing high/low
	LevelLookbackBars int   // 15m bars of history used for levels
	LevelTolerancePts int64 // paise; swing points within this cluster together
	MinTouches        int   // touches for a level to count
	UsePrevDayLevels  bool  // add previous-day high/low as touches
	MinRangePts       int64 // paise
	MaxRangePts       int64 // paise
	NarrowMaxPts      int64 // paise; below → NARROW
	MediumMaxPts      int64 // paise; below → MEDIUM, else WIDE
}

// DefaultRangeContextConfig returns guide values for NIFTY (1 pt = 100 paise).
func DefaultRangeContextConfig() RangeContextConfig {
	return RangeContextConfig{
		ADXPeriod:     14,
		MaxADX:        25,
		EMAPeriod:     20,
		FlatSlopeBars: 4,
		FlatSlopePts:  2500, // 25 pts per hour
		BBPeriod:      20,
		BBMult:        2.0,
		BBSqueezePct:  0.8,
		RSIPeriod:     14,
		VolAvgBars:    20,
		VolAvgBars5:   12, // one hour
		HTFEMAPeriod:  20,
		ATRPeriod:     14,

		SwingWindow:       2,
		LevelLookbackBars: 50, // two sessions of 15m bars
		LevelTolerancePts: 1500,
		MinTouches:        2,
		UsePrevDayLevels:  true,
		MinRangePts:       5000,  // 50 pts
		MaxRangePts:       30000, // 300 pts
		NarrowMaxPts:      10000,
		MediumMaxPts:      20000,
	}
}

// tfBar is one aggregated bar; TS is the bucket start.
type tfBar struct {
	ohlcv
	TS time.Time `json:"ts"`
}

// tfAggregator builds N-minute bars from closed 1m candles, anchored at
// 09:15 IST so 15m buckets are 09:15, 09:30, … like exchange charts.
type tfAggregator struct {
	Minutes int    `json:"minutes"`
	Cur     tfBar  `json:"cur"`
	Has     bool   `json:"has"`
	Day     string `json:"day"`
	Bucket  int    `json:"bucket"`
}

// push adds a 1m bar. It returns the bar that closed, if any. A bar closes
// when its last minute arrives, or when a later bucket starts (gap).
func (a *tfAggregator) push(ts time.Time, b ohlcv) (tfBar, bool) {
	t := ts.In(ist)
	mins := t.Hour()*60 + t.Minute() - sessionOpenMin
	if mins < 0 {
		return tfBar{}, false
	}
	day := t.Format("2006-01-02")
	bucket := mins / a.Minutes

	var closed tfBar
	var didClose bool
	if a.Has && (day != a.Day || bucket != a.Bucket) {
		closed, didClose = a.Cur, true
		a.Has = false
	}
	if !a.Has {
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, ist).
			Add(time.Duration(sessionOpenMin+bucket*a.Minutes) * time.Minute)
		a.Cur = tfBar{ohlcv: b, TS: start}
		a.Has, a.Day, a.Bucket = true, day, bucket
	} else {
		a.Cur.High = maxInt64(a.Cur.High, b.High)
		a.Cur.Low = minInt64(a.Cur.Low, b.Low)
		a.Cur.Close = b.Close
		a.Cur.Volume += b.Volume
	}
	if mins%a.Minutes == a.Minutes-1 {
		if didClose {
			// Gap bar closed and this minute also completes its own bucket:
			// the caller gets the older one now; hold this one for the next push.
			return closed, true
		}
		out := a.Cur
		a.Has = false
		return out, true
	}
	return closed, didClose
}

// RangeState is the read-only view strategies use.
type RangeState struct {
	InRange    bool
	Support    int64
	Resistance int64
	SupTouches int
	ResTouches int
	Width      int64
	Class      RangeClass
	ADX        float64
	ADXReady   bool
	EMAFlat    bool
	Squeeze    bool
	RSI5       float64
	RSIReady   bool
	ATR15      int64 // 15m ATR, paise (0 until ready)
	HTFEMA     int64 // 1h EMA, paise (0 until ready)
	HTFTrend   int   // +1 price above the 1h EMA, −1 below, 0 not ready
}

// rangeContext holds the multi-TF state for one index.
type rangeContext struct {
	cfg RangeContextConfig

	agg5  tfAggregator
	agg15 tfAggregator
	aggE  tfAggregator // EntryMinutes series (2m/3m), unused otherwise
	agg60 tfAggregator

	adx15 *indicator.ADX
	ema15 *indicator.EMA
	bb15  *indicator.Bollinger
	atr15 *indicator.ATR
	rsi5  *indicator.RSI
	ema60 *indicator.EMA

	closedE bool    // the last update closed an EntryMinutes bar
	barsE   []tfBar // EntryMinutes bars
	bars60  []tfBar // 1h bars (trend + HTF levels), kept for replay

	// closed5 is true when the last update closed a 5m bar (entry trigger).
	closed5 bool
	// extVol is per-minute volume from another instrument (NIFTY futures),
	// keyed by minute start (unix). The index itself carries no volume.
	extVol map[int64]int64

	bars15  []tfBar // last LevelLookbackBars+ closed 15m bars
	bars5   []tfBar // last few 5m bars, kept for snapshot replay
	bars1   []ohlcv // last VolAvgBars+1 1m bars (newest last)
	emaHist []int64 // 15m EMA values, newest last

	curDay      string
	dayHigh     int64
	dayLow      int64
	prevDayHigh int64
	prevDayLow  int64

	lastTS time.Time
	state  RangeState
}

func newRangeContext(cfg RangeContextConfig) *rangeContext {
	return &rangeContext{
		cfg:   cfg,
		agg5:  tfAggregator{Minutes: 5},
		agg15: tfAggregator{Minutes: 15},
		aggE:  tfAggregator{Minutes: entryMinutes(cfg)},
		agg60: tfAggregator{Minutes: 60},
		ema60: indicator.NewEMA(htfPeriod(cfg)),
		adx15: indicator.NewADX(cfg.ADXPeriod),
		ema15: indicator.NewEMA(cfg.EMAPeriod),
		bb15:  indicator.NewBollinger(cfg.BBPeriod, cfg.BBMult),
		atr15: indicator.NewATR(atrPeriod(cfg)),
		rsi5:  indicator.NewRSI(cfg.RSIPeriod),
	}
}

func entryMinutes(cfg RangeContextConfig) int {
	if cfg.EntryMinutes == 2 || cfg.EntryMinutes == 3 {
		return cfg.EntryMinutes
	}
	return 0
}

func htfPeriod(cfg RangeContextConfig) int {
	if cfg.HTFEMAPeriod > 0 {
		return cfg.HTFEMAPeriod
	}
	return 20
}

func atrPeriod(cfg RangeContextConfig) int {
	if cfg.ATRPeriod > 0 {
		return cfg.ATRPeriod
	}
	return 14
}

// addVolume records external (futures) volume for the 1m bar starting at minute.
func (rc *rangeContext) addVolume(minute time.Time, vol int64) {
	if vol <= 0 {
		return
	}
	if rc.extVol == nil {
		rc.extVol = make(map[int64]int64)
	}
	rc.extVol[minute.Unix()] += vol
	// Keep the map bounded: drop minutes older than an hour.
	if len(rc.extVol) > 120 {
		cut := minute.Add(-time.Hour).Unix()
		for k := range rc.extVol {
			if k < cut {
				delete(rc.extVol, k)
			}
		}
	}
}

func toModelCandle(b ohlcv, ts time.Time) model.Candle {
	return model.Candle{TS: ts, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume}
}

// update feeds one closed 1m candle. It returns false for a duplicate or
// out-of-order candle, which is ignored.
func (rc *rangeContext) update(ts time.Time, b ohlcv) bool {
	if !rc.lastTS.IsZero() && !ts.After(rc.lastTS) {
		return false
	}
	rc.lastTS = ts
	rc.closed5 = false
	rc.closedE = false
	if b.Volume == 0 && rc.extVol != nil {
		if v, ok := rc.extVol[ts.Unix()]; ok {
			b.Volume = v
			delete(rc.extVol, ts.Unix())
		}
	}

	day := ts.In(ist).Format("2006-01-02")
	if day != rc.curDay {
		if rc.curDay != "" {
			rc.prevDayHigh, rc.prevDayLow = rc.dayHigh, rc.dayLow
		}
		rc.curDay, rc.dayHigh, rc.dayLow = day, b.High, b.Low
	} else {
		rc.dayHigh = maxInt64(rc.dayHigh, b.High)
		rc.dayLow = minInt64(rc.dayLow, b.Low)
	}

	rc.bars1 = append(rc.bars1, b)
	if keep := rc.cfg.VolAvgBars + 1; len(rc.bars1) > keep {
		rc.bars1 = rc.bars1[len(rc.bars1)-keep:]
	}

	if bar, ok := rc.agg5.push(ts, b); ok {
		rc.on5m(bar)
		rc.closed5 = true
	}
	if bar, ok := rc.agg15.push(ts, b); ok {
		rc.on15m(bar)
	}
	if rc.aggE.Minutes > 0 {
		if bar, ok := rc.aggE.push(ts, b); ok {
			rc.onEntry(bar)
			rc.closedE = true
		}
	}
	if bar, ok := rc.agg60.push(ts, b); ok {
		rc.on60m(bar)
	}
	rc.refresh(b.Close)
	return true
}

func (rc *rangeContext) on5m(bar tfBar) {
	rc.rsi5.Update(toModelCandle(bar.ohlcv, bar.TS))
	rc.bars5 = append(rc.bars5, bar)
	if keep := rc.cfg.RSIPeriod * 3; len(rc.bars5) > keep {
		rc.bars5 = rc.bars5[len(rc.bars5)-keep:]
	}
}

func (rc *rangeContext) onEntry(bar tfBar) {
	rc.barsE = append(rc.barsE, bar)
	if keep := 64; len(rc.barsE) > keep {
		rc.barsE = rc.barsE[len(rc.barsE)-keep:]
	}
}

func (rc *rangeContext) on60m(bar tfBar) {
	rc.ema60.Update(toModelCandle(bar.ohlcv, bar.TS))
	rc.bars60 = append(rc.bars60, bar)
	if keep := htfPeriod(rc.cfg) * 3; len(rc.bars60) > keep {
		rc.bars60 = rc.bars60[len(rc.bars60)-keep:]
	}
}

func (rc *rangeContext) on15m(bar tfBar) {
	c := toModelCandle(bar.ohlcv, bar.TS)
	rc.adx15.Update(c)
	rc.ema15.Update(c)
	rc.bb15.Update(c)
	rc.atr15.Update(c)
	if rc.ema15.Ready() {
		rc.emaHist = append(rc.emaHist, int64(rc.ema15.Value()))
		if keep := rc.cfg.FlatSlopeBars + 1; len(rc.emaHist) > keep {
			rc.emaHist = rc.emaHist[len(rc.emaHist)-keep:]
		}
	}
	rc.bars15 = append(rc.bars15, bar)
	if keep := rc.historyKeep15(); len(rc.bars15) > keep {
		rc.bars15 = rc.bars15[len(rc.bars15)-keep:]
	}
}

// historyKeep15 is enough 15m bars to rebuild every 15m indicator on replay.
func (rc *rangeContext) historyKeep15() int {
	n := rc.cfg.LevelLookbackBars
	if w := rc.cfg.ADXPeriod*4 + 2; w > n {
		n = w
	}
	if rc.cfg.EMAPeriod*3 > n {
		n = rc.cfg.EMAPeriod * 3
	}
	return n
}

// refresh recomputes the range view against the latest 1m close.
func (rc *rangeContext) refresh(price int64) {
	st := RangeState{
		ADX:      rc.adx15.Value(),
		ADXReady: rc.adx15.Ready(),
		Squeeze:  rc.bb15.IsSqueezing(rc.cfg.BBSqueezePct),
		RSI5:     rc.rsi5.Value(),
		RSIReady: rc.rsi5.Ready(),
	}
	if n := len(rc.emaHist); n > rc.cfg.FlatSlopeBars {
		st.EMAFlat = absInt64(rc.emaHist[n-1]-rc.emaHist[n-1-rc.cfg.FlatSlopeBars]) <= rc.cfg.FlatSlopePts
	}

	sup, supT, res, resT := rc.levels(price)
	st.Support, st.SupTouches, st.Resistance, st.ResTouches = sup, supT, res, resT
	if sup > 0 && res > sup {
		st.Width = res - sup
		switch {
		case st.Width < rc.cfg.NarrowMaxPts:
			st.Class = RangeNarrow
		case st.Width < rc.cfg.MediumMaxPts:
			st.Class = RangeMedium
		default:
			st.Class = RangeWide
		}
	}
	if rc.atr15.Ready() {
		st.ATR15 = int64(rc.atr15.Value())
	}
	if rc.ema60.Ready() {
		st.HTFEMA = int64(rc.ema60.Value())
		switch {
		case price > st.HTFEMA:
			st.HTFTrend = 1
		case price < st.HTFEMA:
			st.HTFTrend = -1
		}
	}
	st.InRange = st.ADXReady && st.ADX < rc.cfg.MaxADX && (st.EMAFlat || !rc.cfg.RequireFlatEMA) &&
		st.Width >= rc.cfg.MinRangePts && st.Width <= rc.cfg.MaxRangePts
	rc.state = st
}

type levelCluster struct {
	level   int64
	touches int
}

// cluster groups sorted prices within tolerance; level is the mean.
func clusterLevels(points []int64, tol int64) []levelCluster {
	if len(points) == 0 {
		return nil
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	var out []levelCluster
	start, sum := points[0], int64(0)
	count := 0
	for _, p := range points {
		if p-start > tol {
			out = append(out, levelCluster{level: sum / int64(count), touches: count})
			start, sum, count = p, 0, 0
		}
		sum += p
		count++
	}
	out = append(out, levelCluster{level: sum / int64(count), touches: count})
	return out
}

// swingPoints returns the swing highs and lows (15m, optional 1h) plus the
// previous-day high/low that support/resistance are clustered from.
func (rc *rangeContext) swingPoints() (highs, lows []int64) {
	highs, lows = rc.swingOnly()
	if rc.cfg.UsePrevDayLevels && rc.prevDayHigh > 0 {
		highs = append(highs, rc.prevDayHigh)
		lows = append(lows, rc.prevDayLow)
	}
	return highs, lows
}

// swingOnly returns the 15m (and optional 1h) swing highs and lows alone.
func (rc *rangeContext) swingOnly() (highs, lows []int64) {
	bars := rc.bars15
	if n := rc.cfg.LevelLookbackBars; len(bars) > n {
		bars = bars[len(bars)-n:]
	}
	w := rc.cfg.SwingWindow
	for i := w; i < len(bars)-w; i++ {
		isHigh, isLow := true, true
		for j := i - w; j <= i+w; j++ {
			if j == i {
				continue
			}
			if bars[j].High > bars[i].High {
				isHigh = false
			}
			if bars[j].Low < bars[i].Low {
				isLow = false
			}
		}
		if isHigh {
			highs = append(highs, bars[i].High)
		}
		if isLow {
			lows = append(lows, bars[i].Low)
		}
	}
	if rc.cfg.HTFLevels {
		h := rc.bars60
		for i := w; i < len(h)-w; i++ {
			isHigh, isLow := true, true
			for j := i - w; j <= i+w; j++ {
				if j == i {
					continue
				}
				if h[j].High > h[i].High {
					isHigh = false
				}
				if h[j].Low < h[i].Low {
					isLow = false
				}
			}
			if isHigh {
				highs = append(highs, h[i].High)
			}
			if isLow {
				lows = append(lows, h[i].Low)
			}
		}
	}
	return highs, lows
}

// levels returns the nearest confirmed support at/below price and the
// nearest confirmed resistance at/above price (each within tolerance).
func (rc *rangeContext) levels(price int64) (sup int64, supT int, res int64, resT int) {
	highs, lows := rc.swingPoints()
	tol := rc.cfg.LevelTolerancePts
	for _, c := range clusterLevels(lows, tol) {
		if c.touches >= rc.cfg.MinTouches && c.level <= price+tol && c.level > sup {
			sup, supT = c.level, c.touches
		}
	}
	for _, c := range clusterLevels(highs, tol) {
		if c.touches >= rc.cfg.MinTouches && c.level >= price-tol && (res == 0 || c.level < res) {
			res, resT = c.level, c.touches
		}
	}
	return sup, supT, res, resT
}

// State returns the latest range view.
func (rc *rangeContext) State() RangeState { return rc.state }

// lastBars returns the previous and current 1m bars.
func (rc *rangeContext) lastBars() (prev, cur ohlcv, ok bool) {
	n := len(rc.bars1)
	if n < 2 {
		return ohlcv{}, ohlcv{}, false
	}
	return rc.bars1[n-2], rc.bars1[n-1], true
}

// lastBars5 returns the previous and the just-closed 5m bars.
func (rc *rangeContext) lastBars5() (prev, cur ohlcv, ok bool) {
	n := len(rc.bars5)
	if n < 2 {
		return ohlcv{}, ohlcv{}, false
	}
	return rc.bars5[n-2].ohlcv, rc.bars5[n-1].ohlcv, true
}

// lastBarsE returns the previous and just-closed EntryMinutes bars.
func (rc *rangeContext) lastBarsE() (prev, cur ohlcv, ok bool) {
	n := len(rc.barsE)
	if n < 2 {
		return ohlcv{}, ohlcv{}, false
	}
	return rc.barsE[n-2].ohlcv, rc.barsE[n-1].ohlcv, true
}

// volumeAtLeast5 is volumeAtLeast on 5m bars: the just-closed 5m bar
// against the average of the VolAvgBars5 before it.
func (rc *rangeContext) volumeAtLeast5(pct int64, required bool) bool {
	return volumeAtLeastSeries(rc.bars5, rc.cfg.VolAvgBars5, pct, required)
}

// volumeAtLeastE is volumeAtLeast5 on the EntryMinutes series (same
// one-hour average window).
func (rc *rangeContext) volumeAtLeastE(pct int64, required bool) bool {
	k := 60
	if m := rc.aggE.Minutes; m > 0 {
		k = 60 / m
	}
	return volumeAtLeastSeries(rc.barsE, k, pct, required)
}

func volumeAtLeastSeries(bars []tfBar, k int, pct int64, required bool) bool {
	n := len(bars)
	if k <= 0 {
		k = 12
	}
	if n < 2 {
		return !required
	}
	start := n - 1 - k
	if start < 0 {
		start = 0
	}
	var sum int64
	for _, b := range bars[start : n-1] {
		sum += b.Volume
	}
	avg := sum / int64(n-1-start)
	if avg <= 0 {
		return !required
	}
	return bars[n-1].Volume*100 >= avg*pct
}

// avgVolume is the mean volume of the 1m bars before the current one.
// Zero means volume is unavailable (NSE index ticks carry no volume).
func (rc *rangeContext) avgVolume() int64 {
	n := len(rc.bars1)
	if n < 2 {
		return 0
	}
	var sum int64
	for _, b := range rc.bars1[:n-1] {
		sum += b.Volume
	}
	return sum / int64(n-1)
}

// volumeAtLeast reports cur volume >= pct% of the average. With no volume
// data it returns !required, so an index without volume isn't blocked
// unless the config demands volume.
func (rc *rangeContext) volumeAtLeast(pct int64, required bool) bool {
	avg := rc.avgVolume()
	if avg <= 0 {
		return !required
	}
	return rc.bars1[len(rc.bars1)-1].Volume*100 >= avg*pct
}

// ── Snapshot / restore ──
// Indicator snapshots are lossy (ADX drops its smoothing state), so the
// context is saved as bar history and rebuilt by replay.

type rangeContextSnapshot struct {
	Bars15      []tfBar      `json:"bars15"`
	Bars5       []tfBar      `json:"bars5"`
	Bars1       []ohlcv      `json:"bars1"`
	Agg5        tfAggregator `json:"agg5"`
	Agg15       tfAggregator `json:"agg15"`
	BarsE       []tfBar      `json:"bars_e,omitempty"`
	AggE        tfAggregator `json:"agg_e"`
	Bars60      []tfBar      `json:"bars60,omitempty"`
	Agg60       tfAggregator `json:"agg60"`
	CurDay      string       `json:"cur_day"`
	DayHigh     int64        `json:"day_high"`
	DayLow      int64        `json:"day_low"`
	PrevDayHigh int64        `json:"prev_day_high"`
	PrevDayLow  int64        `json:"prev_day_low"`
	LastTS      time.Time    `json:"last_ts"`
}

func (rc *rangeContext) snapshot() rangeContextSnapshot {
	return rangeContextSnapshot{
		Bars15: append([]tfBar(nil), rc.bars15...),
		Bars5:  append([]tfBar(nil), rc.bars5...),
		Bars1:  append([]ohlcv(nil), rc.bars1...),
		Agg5:   rc.agg5, Agg15: rc.agg15,
		BarsE: append([]tfBar(nil), rc.barsE...), AggE: rc.aggE,
		Bars60: append([]tfBar(nil), rc.bars60...), Agg60: rc.agg60,
		CurDay: rc.curDay, DayHigh: rc.dayHigh, DayLow: rc.dayLow,
		PrevDayHigh: rc.prevDayHigh, PrevDayLow: rc.prevDayLow,
		LastTS: rc.lastTS,
	}
}

func restoreRangeContext(cfg RangeContextConfig, s rangeContextSnapshot) *rangeContext {
	rc := newRangeContext(cfg)
	for _, b := range s.Bars15 {
		rc.on15m(b)
	}
	for _, b := range s.Bars5 {
		rc.on5m(b)
	}
	for _, b := range s.Bars60 {
		rc.on60m(b)
	}
	for _, b := range s.BarsE {
		rc.onEntry(b)
	}
	if s.Agg60.Minutes > 0 {
		rc.agg60 = s.Agg60
	}
	if s.AggE.Minutes > 0 && s.AggE.Minutes == rc.aggE.Minutes {
		rc.aggE = s.AggE
	}
	rc.bars1 = append([]ohlcv(nil), s.Bars1...)
	if s.Agg5.Minutes > 0 {
		rc.agg5 = s.Agg5
	}
	if s.Agg15.Minutes > 0 {
		rc.agg15 = s.Agg15
	}
	rc.curDay, rc.dayHigh, rc.dayLow = s.CurDay, s.DayHigh, s.DayLow
	rc.prevDayHigh, rc.prevDayLow = s.PrevDayHigh, s.PrevDayLow
	rc.lastTS = s.LastTS
	if n := len(rc.bars1); n > 0 {
		rc.refresh(rc.bars1[n-1].Close)
	}
	return rc
}

// minutesIST returns minutes after midnight IST.
func minutesIST(ts time.Time) int {
	t := ts.In(ist)
	return t.Hour()*60 + t.Minute()
}

// hhmm converts 9, 45 → minutes after midnight.
func hhmm(h, m int) int { return h*60 + m }
