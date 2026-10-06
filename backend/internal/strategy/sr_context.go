package strategy

import (
	"sort"
	"time"

	"trading-systemv1/internal/indicator"
)

// ════════════════════════════════════════════════════════════════════
//  SR context — support/resistance, VWAP, 5m EMAs and the market regime
//  for NIFTY50_SR, layered on the range context (15m ADX/ATR/levels,
//  5m RSI, 1h EMA).
//
//  VWAP is kept in int64 paise here (typical price × volume); with no
//  volume data each 1m bar weighs 1, i.e. a session TWAP.
//  Nothing reads the wall clock: replaying candles gives the same state.
// ════════════════════════════════════════════════════════════════════

// SRRegime is the market regime the setups are chosen from.
type SRRegime string

const (
	SRRegimeNone      SRRegime = ""
	SRRegimeRange     SRRegime = "RANGE"      // ADX low, VWAP flat, price crossing VWAP
	SRRegimeTrendUp   SRRegime = "TREND_UP"   // ADX high, above VWAP, EMA9 > EMA21
	SRRegimeTrendDown SRRegime = "TREND_DOWN" // ADX high, below VWAP, EMA9 < EMA21
	SRRegimeMixed     SRRegime = "MIXED"      // signals disagree: breakout-retest only
	SRRegimeDead      SRRegime = "DEAD"       // 15m ATR below floor: no entries
)

// Level sources.
const (
	SRLevelSwing   = "SWING"    // clustered 15m swing highs/lows
	SRLevelPrevDay = "PREV_DAY" // previous session high/low
	SRLevelOpening = "OPENING"  // today's opening range high/low
)

// SRLevel is one support/resistance price.
type SRLevel struct {
	Price   int64  `json:"price"` // paise
	Touches int    `json:"touches"`
	Source  string `json:"source"`
}

// SRContextConfig configures regime and level detection.
type SRContextConfig struct {
	Range RangeContextConfig

	EMAFast int // on 5m bars
	EMASlow int

	RangeMaxADX        float64 // RANGE only while 15m ADX < this
	TrendMinADX        float64 // TREND only while 15m ADX > this
	VWAPSlopeBars      int     // 5m bars the VWAP slope is measured over
	FlatVWAPSlopePts   int64   // |VWAP change| over VWAPSlopeBars ≤ this → flat (paise)
	MinVWAPCrosses     int     // 5m closes switching VWAP side today, for RANGE
	MinATRPts          int64   // 15m ATR below this → DEAD (paise)
	OpeningRangeEndMin int     // opening range is 09:15 up to this minute (IST)
}

// DefaultSRContextConfig returns NIFTY values (1 pt = 100 paise).
func DefaultSRContextConfig() SRContextConfig {
	rc := DefaultRangeContextConfig()
	rc.EntryMinutes = 5
	return SRContextConfig{
		Range:              rc,
		EMAFast:            9,
		EMASlow:            21,
		RangeMaxADX:        22,
		TrendMinADX:        25,
		VWAPSlopeBars:      6,    // 30 minutes
		FlatVWAPSlopePts:   1500, // 15 pts per 30 min
		MinVWAPCrosses:     2,
		MinATRPts:          1200, // 12 pts on 15m
		OpeningRangeEndMin: hhmm(9, 30),
	}
}

// SRState is the read-only view the strategy uses.
type SRState struct {
	Range     RangeState
	Regime    SRRegime
	VWAP      int64 // paise, 0 until the first bar of the day
	VWAPSlope int64 // paise over VWAPSlopeBars 5m bars
	Crosses   int   // VWAP side changes today (5m closes)
	EMAFast   int64 // 5m, paise (0 until ready)
	EMASlow   int64
	EMAReady  bool
	ORHigh    int64 // opening range, 0 until complete
	ORLow     int64
	// PrevDayHigh/Low are the previous session's extremes, 0 until known.
	PrevDayHigh int64
	PrevDayLow  int64
	// DayOpen/High/Low are today's so far (DayOpen 0 if unknown).
	DayOpen int64
	DayHigh int64
	DayLow  int64
	Levels  []SRLevel // sorted by price
}

type srContext struct {
	cfg SRContextConfig
	rc  *rangeContext

	emaFast *indicator.EMA
	emaSlow *indicator.EMA

	day      string
	cumPV    int64 // Σ typical price × weight
	cumV     int64 // Σ weight
	vwapHist []int64
	crosses  int
	vwapSide int // +1 / −1 side of the last 5m close, 0 unknown

	orHigh, orLow int64
	orDone        bool

	state SRState
}

func newSRContext(cfg SRContextConfig) *srContext {
	return &srContext{
		cfg:     cfg,
		rc:      newRangeContext(cfg.Range),
		emaFast: indicator.NewEMA(cfg.EMAFast),
		emaSlow: indicator.NewEMA(cfg.EMASlow),
	}
}

// update feeds one closed 1m candle; false means duplicate/out of order.
func (sc *srContext) update(ts time.Time, b ohlcv) bool {
	if !sc.rc.update(ts, b) {
		return false
	}
	// The range context filled in futures volume, if any.
	bar := sc.rc.bars1[len(sc.rc.bars1)-1]

	day := ts.In(ist).Format("2006-01-02")
	if day != sc.day {
		sc.day = day
		sc.cumPV, sc.cumV = 0, 0
		sc.vwapHist = sc.vwapHist[:0]
		sc.crosses, sc.vwapSide = 0, 0
		sc.orHigh, sc.orLow, sc.orDone = 0, 0, false
	}
	w := bar.Volume
	if w <= 0 {
		w = 1
	}
	sc.cumPV += (bar.High + bar.Low + bar.Close) / 3 * w
	sc.cumV += w

	closeMin := minutesIST(ts) + 1 // TS is the bucket start
	if !sc.orDone {
		if sc.orHigh == 0 {
			sc.orHigh, sc.orLow = bar.High, bar.Low
		} else {
			sc.orHigh, sc.orLow = maxInt64(sc.orHigh, bar.High), minInt64(sc.orLow, bar.Low)
		}
		if closeMin >= sc.cfg.OpeningRangeEndMin {
			sc.orDone = true
		}
	}

	if sc.rc.closed5 {
		sc.on5m(sc.rc.bars5[len(sc.rc.bars5)-1])
	}
	sc.refresh(bar.Close)
	return true
}

func (sc *srContext) vwap() int64 {
	if sc.cumV == 0 {
		return 0
	}
	return sc.cumPV / sc.cumV
}

func (sc *srContext) on5m(bar tfBar) {
	c := toModelCandle(bar.ohlcv, bar.TS)
	sc.emaFast.Update(c)
	sc.emaSlow.Update(c)

	v := sc.vwap()
	sc.vwapHist = append(sc.vwapHist, v)
	if keep := sc.cfg.VWAPSlopeBars + 1; len(sc.vwapHist) > keep {
		sc.vwapHist = sc.vwapHist[len(sc.vwapHist)-keep:]
	}
	side := 0
	switch {
	case bar.Close > v:
		side = 1
	case bar.Close < v:
		side = -1
	}
	if side != 0 {
		if sc.vwapSide != 0 && side != sc.vwapSide {
			sc.crosses++
		}
		sc.vwapSide = side
	}
}

func (sc *srContext) refresh(price int64) {
	rs := sc.rc.State()
	st := SRState{Range: rs, VWAP: sc.vwap(), Crosses: sc.crosses}
	if n := len(sc.vwapHist); n > sc.cfg.VWAPSlopeBars {
		st.VWAPSlope = sc.vwapHist[n-1] - sc.vwapHist[n-1-sc.cfg.VWAPSlopeBars]
	}
	if sc.emaFast.Ready() && sc.emaSlow.Ready() {
		st.EMAFast, st.EMASlow, st.EMAReady = int64(sc.emaFast.Value()), int64(sc.emaSlow.Value()), true
	}
	if sc.orDone {
		st.ORHigh, st.ORLow = sc.orHigh, sc.orLow
	}
	st.PrevDayHigh, st.PrevDayLow = sc.rc.prevDayHigh, sc.rc.prevDayLow
	st.DayOpen, st.DayHigh, st.DayLow = sc.rc.dayOpen, sc.rc.dayHigh, sc.rc.dayLow
	st.Levels = sc.levels()
	st.Regime = sc.regime(st, price)
	sc.state = st
}

// regime classifies the market. Unready indicators give SRRegimeNone.
func (sc *srContext) regime(st SRState, price int64) SRRegime {
	rs := st.Range
	if !rs.ADXReady || !st.EMAReady || st.VWAP == 0 || rs.ATR15 == 0 {
		return SRRegimeNone
	}
	if rs.ATR15 < sc.cfg.MinATRPts {
		return SRRegimeDead
	}
	flat := absInt64(st.VWAPSlope) <= sc.cfg.FlatVWAPSlopePts
	if rs.ADX < sc.cfg.RangeMaxADX && flat && st.Crosses >= sc.cfg.MinVWAPCrosses {
		return SRRegimeRange
	}
	if rs.ADX > sc.cfg.TrendMinADX {
		// The 1h EMA must agree once it is ready.
		switch {
		case price > st.VWAP && st.EMAFast > st.EMASlow && st.VWAPSlope > 0 && rs.HTFTrend >= 0:
			return SRRegimeTrendUp
		case price < st.VWAP && st.EMAFast < st.EMASlow && st.VWAPSlope < 0 && rs.HTFTrend <= 0:
			return SRRegimeTrendDown
		}
	}
	return SRRegimeMixed
}

// levels merges swing clusters (highs and lows together: a broken
// resistance is the next support), previous-day high/low and the
// opening range. Levels closer than the cluster tolerance merge.
// Each point counts once: the previous-day high/low is not part of the
// swing clusters here (it used to be, and was then added a second time);
// with UsePrevDayLevels it still absorbs a lone swing touch at its price.
func (sc *srContext) levels() []SRLevel {
	cfg := sc.cfg.Range
	tol := cfg.LevelTolerancePts
	highs, lows := sc.rc.swingOnly()
	var out []SRLevel
	var weak []levelCluster // swing clusters below MinTouches
	for _, c := range clusterLevels(append(highs, lows...), tol) {
		if c.touches >= cfg.MinTouches {
			out = append(out, SRLevel{Price: c.level, Touches: c.touches, Source: SRLevelSwing})
		} else {
			weak = append(weak, c)
		}
	}
	add := func(p int64, src string, absorbWeak bool) {
		if p <= 0 {
			return
		}
		for i := range out {
			if absInt64(out[i].Price-p) <= tol {
				out[i].Touches++
				return
			}
		}
		touches := 1
		if absorbWeak {
			for i, c := range weak {
				if absInt64(c.level-p) <= tol {
					touches += c.touches
					weak = append(weak[:i], weak[i+1:]...)
					break
				}
			}
		}
		out = append(out, SRLevel{Price: p, Touches: touches, Source: src})
	}
	if sc.rc.prevDayHigh > 0 {
		add(sc.rc.prevDayHigh, SRLevelPrevDay, cfg.UsePrevDayLevels)
		add(sc.rc.prevDayLow, SRLevelPrevDay, cfg.UsePrevDayLevels)
	}
	if sc.orDone {
		add(sc.orHigh, SRLevelOpening, false)
		add(sc.orLow, SRLevelOpening, false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Price < out[j].Price })
	return out
}

// State returns the latest view.
func (sc *srContext) State() SRState { return sc.state }

// levelBelow is the highest level strictly below price, 0 if none.
func levelBelow(levels []SRLevel, price int64) int64 {
	var best int64
	for _, l := range levels {
		if l.Price < price && l.Price > best {
			best = l.Price
		}
	}
	return best
}

// levelAbove is the lowest level strictly above price, 0 if none.
func levelAbove(levels []SRLevel, price int64) int64 {
	var best int64
	for _, l := range levels {
		if l.Price > price && (best == 0 || l.Price < best) {
			best = l.Price
		}
	}
	return best
}

// ── Snapshot / restore ──
// The range context is saved as bar history; the 5m EMAs are rebuilt
// from its 5m bars, VWAP and the opening range are plain sums.

type srContextSnapshot struct {
	Range    rangeContextSnapshot `json:"range"`
	Day      string               `json:"day"`
	CumPV    int64                `json:"cum_pv"`
	CumV     int64                `json:"cum_v"`
	VWAPHist []int64              `json:"vwap_hist"`
	Crosses  int                  `json:"crosses"`
	VWAPSide int                  `json:"vwap_side"`
	ORHigh   int64                `json:"or_high"`
	ORLow    int64                `json:"or_low"`
	ORDone   bool                 `json:"or_done"`
}

func (sc *srContext) snapshot() srContextSnapshot {
	return srContextSnapshot{
		Range: sc.rc.snapshot(), Day: sc.day,
		CumPV: sc.cumPV, CumV: sc.cumV,
		VWAPHist: append([]int64(nil), sc.vwapHist...),
		Crosses:  sc.crosses, VWAPSide: sc.vwapSide,
		ORHigh: sc.orHigh, ORLow: sc.orLow, ORDone: sc.orDone,
	}
}

func restoreSRContext(cfg SRContextConfig, s srContextSnapshot) *srContext {
	sc := newSRContext(cfg)
	sc.rc = restoreRangeContext(cfg.Range, s.Range)
	for _, b := range sc.rc.bars5 {
		c := toModelCandle(b.ohlcv, b.TS)
		sc.emaFast.Update(c)
		sc.emaSlow.Update(c)
	}
	sc.day, sc.cumPV, sc.cumV = s.Day, s.CumPV, s.CumV
	sc.vwapHist = append([]int64(nil), s.VWAPHist...)
	sc.crosses, sc.vwapSide = s.Crosses, s.VWAPSide
	sc.orHigh, sc.orLow, sc.orDone = s.ORHigh, s.ORLow, s.ORDone
	if n := len(sc.rc.bars1); n > 0 {
		sc.refresh(sc.rc.bars1[n-1].Close)
	}
	return sc
}
