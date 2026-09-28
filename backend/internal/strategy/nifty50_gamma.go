package strategy

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"trading-systemv1/internal/model"
)

// ════════════════════════════════════════════════════════════════════
//  NIFTY50 GAMMA — expiry-day "gamma blast" (paper only)
//
//  On weekly expiry day, ATM options carry very high gamma: after a quiet
//  morning a breakout can multiply their premium. Rules (minimal core of
//  the gamma-blast literature; the indicator kitchen sink is left out):
//    • Expiry day only; the option bought is today's expiry.
//    • Setup at 13:45: day range since 09:15 ≤ MaxRangeBps of price,
//      15m ADX < MaxADX, optionally a Bollinger squeeze.
//    • Trigger 13:45–14:30: a strong 5m close beyond the day range (volume
//      spike when futures volume is available), confirmed by the next 5m
//      close still beyond it. One trade per day.
//    • Exits: premium −30% stop, +150% target, trail 30% off the best once
//      +100%; index back inside the range; flat by 15:00.
// ════════════════════════════════════════════════════════════════════

// Nifty50GammaConfig configures the gamma-blast strategy.
type Nifty50GammaConfig struct {
	Range RangeContextConfig // ADX / squeeze / 5m bars / volume

	SetupMin       int // minutes after midnight IST when the setup is judged (13:45)
	TriggerWindow  timeWindow
	MaxRangeBps    int64   // day range 09:15→setup ≤ this bps of price (100 = 1%)
	MaxADX         float64 // 15m ADX below this at setup
	RequireSqueeze bool    // Bollinger squeeze at setup
	ConfirmPts     int64   // paise beyond the day range for a breakout close
	VolPct         int64   // breakout bar volume ≥ this % of average (when volume exists)
	RequireVolume  bool
	FailBackPts    int64 // exit when the index closes this far back inside the range
	OTMSteps       int   // 0 = ATM
	StrikeStep     int64

	PremiumSLPct     int64 // exit at −this % of entry premium
	PremiumTargetPct int64 // exit at +this % (150 = 2.5×)
	TrailStartPct    int64 // arm the premium trail at +this %
	TrailGiveBackPct int64 // exit when premium falls this % from its best once armed
	TimeExitMin      int

	IndexToken   string
	NameOverride string
}

// DefaultNifty50GammaConfig returns the documented core rules.
func DefaultNifty50GammaConfig() Nifty50GammaConfig {
	return Nifty50GammaConfig{
		Range:            DefaultRangeContextConfig(),
		SetupMin:         hhmm(13, 45),
		TriggerWindow:    timeWindow{From: hhmm(13, 45), To: hhmm(14, 30)},
		MaxRangeBps:      100,
		MaxADX:           20,
		RequireSqueeze:   false,
		ConfirmPts:       1000,
		VolPct:           150,
		FailBackPts:      2500,
		OTMSteps:         0,
		StrikeStep:       50,
		PremiumSLPct:     30,
		PremiumTargetPct: 150,
		TrailStartPct:    100,
		TrailGiveBackPct: 30,
		TimeExitMin:      hhmm(15, 0), // before NSE's closing auction session
		IndexToken:       "NSE:99926000",
		NameOverride:     "NIFTY50_GAMMA",
	}
}

type nifty50GammaState struct {
	ctx *rangeContext

	Day       string
	DayHigh   int64
	DayLow    int64
	SetupDone bool // setup judged today
	SetupOK   bool
	RangeHi   int64 // day range frozen at setup
	RangeLo   int64
	Traded    bool

	Pending     PositionSide
	PendingEdge int64

	Side        PositionSide
	Strike      int64
	IndexEntry  int64
	FNOToken    string
	FNOEntry    int64
	FNOBest     int64
	LastCloseTS time.Time
}

// Nifty50Gamma is the expiry-day gamma-blast strategy.
type Nifty50Gamma struct {
	mu          sync.Mutex
	instruments map[string]*nifty50GammaState
	qty         int64
	cfg         Nifty50GammaConfig
	// isExpiryDay reports whether a date is a NIFTY weekly expiry. Default:
	// the last trading day on or before Tuesday, trading days = weekdays.
	isExpiryDay func(time.Time) bool
	vol         minuteVolume
	volToken    string
}

func NewNifty50Gamma(qty int64, cfg Nifty50GammaConfig) *Nifty50Gamma {
	return &Nifty50Gamma{instruments: make(map[string]*nifty50GammaState, 1), qty: qty, cfg: cfg,
		isExpiryDay: func(t time.Time) bool { return NiftyExpiryDay(t, nil) }}
}

func (s *Nifty50Gamma) Name() string {
	if s.cfg.NameOverride != "" {
		return s.cfg.NameOverride
	}
	return "NIFTY50_GAMMA"
}

func (s *Nifty50Gamma) Config() Nifty50GammaConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// NiftyExpiryDay reports whether t's IST date is a NIFTY weekly expiry: the
// last trading day on or before that week's Tuesday (holidays move it
// earlier). isTradingDay nil = weekdays.
func NiftyExpiryDay(t time.Time, isTradingDay func(time.Time) bool) bool {
	if isTradingDay == nil {
		isTradingDay = func(d time.Time) bool { return d.Weekday() != time.Saturday && d.Weekday() != time.Sunday }
	}
	d := t.In(ist)
	day := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, ist)
	if !isTradingDay(day) {
		return false
	}
	toTue := (int(time.Tuesday) - int(day.Weekday()) + 7) % 7
	if toTue > 3 { // Wed..Fri belong to next week's Tuesday; only Sat..Tue can shift
		return false
	}
	for i := 1; i <= toTue; i++ {
		if isTradingDay(day.AddDate(0, 0, i)) {
			return false // a later trading day on/before Tuesday is the expiry
		}
	}
	return true
}

// SetTradingDayFunc wires the exchange holiday calendar into expiry detection.
func (s *Nifty50Gamma) SetTradingDayFunc(isTradingDay func(time.Time) bool) {
	s.mu.Lock()
	s.isExpiryDay = func(t time.Time) bool { return NiftyExpiryDay(t, isTradingDay) }
	s.mu.Unlock()
}

// SetVolumeToken names the instrument whose ticks supply volume (NIFTY future).
func (s *Nifty50Gamma) SetVolumeToken(token string) {
	s.mu.Lock()
	s.volToken, s.vol = token, minuteVolume{}
	s.mu.Unlock()
}

// SetPositionToken records the contract ("NFO:<token>") the position holds.
func (s *Nifty50Gamma) SetPositionToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != SideNone {
			st.FNOToken = token
		}
	}
}

// SetFNOEntryPrice records the entry premium so premium exits can run.
func (s *Nifty50Gamma) SetFNOEntryPrice(price int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != SideNone && price > 0 {
			st.FNOEntry, st.FNOBest = price, price
		}
	}
}

// CancelEntry drops an entry the engine could not place; the day's one
// trade is not used up.
func (s *Nifty50Gamma) CancelEntry(side PositionSide, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side == side && side != SideNone {
			log.Printf("[strategy] %s: %s entry cancelled — %s", s.Name(), side, reason)
			s.flatten(st)
			st.Traded = false
		}
	}
}

func (s *Nifty50Gamma) Warmup(candles []model.TFCandle) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range candles {
		if c.TF != tf1m || c.Forming || (s.cfg.IndexToken != "" && c.Key() != s.cfg.IndexToken) {
			continue
		}
		st := s.getOrCreate(c.Key())
		if st.ctx.update(c.TS, barOf(c)) {
			n++
			if c.TS.After(st.LastCloseTS) {
				st.LastCloseTS = c.TS
			}
		}
	}
	return n
}

func (s *Nifty50Gamma) getOrCreate(key string) *nifty50GammaState {
	st, ok := s.instruments[key]
	if !ok {
		st = &nifty50GammaState{ctx: newRangeContext(s.cfg.Range), Side: SideNone, Pending: SideNone}
		s.instruments[key] = st
	}
	return st
}

func (s *Nifty50Gamma) OnTFCandle(candle model.TFCandle) *Signal {
	if candle.TF != tf1m || (s.cfg.IndexToken != "" && candle.Key() != s.cfg.IndexToken) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.getOrCreate(candle.Key())
	if !st.LastCloseTS.IsZero() && !candle.TS.After(st.LastCloseTS) {
		return nil
	}
	st.LastCloseTS = candle.TS
	st.ctx.update(candle.TS, barOf(candle))

	day := candle.TS.In(ist).Format("2006-01-02")
	if st.Day != day {
		*st = nifty50GammaState{ctx: st.ctx, Day: day, DayHigh: candle.High, DayLow: candle.Low,
			Side: SideNone, Pending: SideNone, LastCloseTS: st.LastCloseTS}
	}
	closeMin := minutesIST(candle.TS) + 1
	if closeMin <= sessionOpenMin || closeMin > hhmm(15, 30) {
		return nil
	}
	if !st.SetupDone {
		st.DayHigh = maxInt64(st.DayHigh, candle.High)
		st.DayLow = minInt64(st.DayLow, candle.Low)
	}

	if st.Side != SideNone {
		return s.indexExit(candle, st, closeMin)
	}
	if !s.isExpiryDay(candle.TS) {
		return nil
	}
	if !st.SetupDone && closeMin >= s.cfg.SetupMin {
		s.judgeSetup(candle, st)
	}
	if !st.SetupOK || st.Traded || !st.ctx.closed5 || closeMin < s.cfg.TriggerWindow.From || closeMin >= s.cfg.TriggerWindow.To {
		return nil
	}
	return s.trigger(candle, st)
}

func (s *Nifty50Gamma) judgeSetup(candle model.TFCandle, st *nifty50GammaState) {
	st.SetupDone = true
	st.RangeHi, st.RangeLo = st.DayHigh, st.DayLow
	rs := st.ctx.State()
	quiet := (st.RangeHi-st.RangeLo)*10000 <= candle.Close*s.cfg.MaxRangeBps
	adxOK := rs.ADXReady && rs.ADX < s.cfg.MaxADX
	squeezeOK := !s.cfg.RequireSqueeze || rs.Squeeze
	st.SetupOK = quiet && adxOK && squeezeOK
	log.Printf("[strategy] %s: expiry setup %v range=%d-%d (%.2f%%) adx=%.1f squeeze=%v", s.Name(), st.SetupOK,
		st.RangeLo, st.RangeHi, float64(st.RangeHi-st.RangeLo)*100/float64(candle.Close), rs.ADX, rs.Squeeze)
}

func (s *Nifty50Gamma) trigger(candle model.TFCandle, st *nifty50GammaState) *Signal {
	_, cur, ok := st.ctx.lastBars5()
	if !ok {
		return nil
	}
	confirm := s.cfg.ConfirmPts
	if st.Pending != SideNone {
		dir, edge := st.Pending, st.PendingEdge
		st.Pending = SideNone
		held := (dir == SideCall && candle.Close >= edge+confirm) || (dir == SidePut && candle.Close <= edge-confirm)
		if !held {
			log.Printf("[strategy] %s: false gamma break %s edge=%d close=%d", s.Name(), dir, edge, candle.Close)
			return nil
		}
		return s.enter(candle, st, dir)
	}
	if !st.ctx.volumeAtLeast5(s.cfg.VolPct, s.cfg.RequireVolume) {
		return nil
	}
	strong := cur.body()*2 >= cur.rng()
	switch {
	case strong && cur.bullish() && candle.Close >= st.RangeHi+confirm:
		st.Pending, st.PendingEdge = SideCall, st.RangeHi
	case strong && cur.bearish() && candle.Close <= st.RangeLo-confirm:
		st.Pending, st.PendingEdge = SidePut, st.RangeLo
	}
	return nil
}

func (s *Nifty50Gamma) enter(candle model.TFCandle, st *nifty50GammaState, dir PositionSide) *Signal {
	st.Side, st.Traded, st.IndexEntry = dir, true, candle.Close
	st.Strike = entryStrike(dir, candle.Close, s.cfg.StrikeStep, s.cfg.OTMSteps)
	st.FNOToken, st.FNOEntry, st.FNOBest = "", 0, 0
	width := st.RangeHi - st.RangeLo
	reason := fmt.Sprintf("GAMMA BLAST %s expiry-day break of %d-%d strike=%d close=%d",
		dir, st.RangeLo, st.RangeHi, st.Strike, candle.Close)
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	return &Signal{
		StrategyName: s.Name(), Action: ActionBuy, Side: dir,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty,
		MarketState: "gamma", Reason: reason, Strike: st.Strike,
		SameDayExpiry: true, TargetMove: maxInt64(width, 5000),
	}
}

// indexExit handles the time exit and a failed break (index back inside).
func (s *Nifty50Gamma) indexExit(candle model.TFCandle, st *nifty50GammaState, closeMin int) *Signal {
	if closeMin >= s.cfg.TimeExitMin {
		return s.exit(candle.Key(), st, fmt.Sprintf("GAMMA %s TIME EXIT close=%d", st.Side, candle.Close))
	}
	back := s.cfg.FailBackPts
	if (st.Side == SideCall && candle.Close <= st.RangeHi-back) || (st.Side == SidePut && candle.Close >= st.RangeLo+back) {
		return s.exit(candle.Key(), st, fmt.Sprintf("GAMMA %s FAILED BREAK back inside range close=%d", st.Side, candle.Close))
	}
	return nil
}

func (s *Nifty50Gamma) OnTick(tick model.Tick) *Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tick.Exchange + ":" + tick.Token
	if s.volToken != "" && key == s.volToken {
		if minute, v, ok := s.vol.observe(tick.CanonicalTS(), tick.DayVolume); ok {
			for _, st := range s.instruments {
				st.ctx.addVolume(minute, v)
			}
		}
		return nil
	}
	for ik, st := range s.instruments {
		if st.Side == SideNone || st.FNOEntry <= 0 || st.FNOToken != key || tick.Price <= 0 {
			continue
		}
		p, e := tick.Price, st.FNOEntry
		if p > st.FNOBest {
			st.FNOBest = p
		}
		best := st.FNOBest
		switch {
		case (e-p)*100 >= e*s.cfg.PremiumSLPct:
			return s.exit(ik, st, fmt.Sprintf("GAMMA %s PREMIUM SL premium=%d entry=%d", st.Side, p, e))
		case (p-e)*100 >= e*s.cfg.PremiumTargetPct:
			return s.exit(ik, st, fmt.Sprintf("GAMMA %s PREMIUM TARGET premium=%d entry=%d", st.Side, p, e))
		case (best-e)*100 >= e*s.cfg.TrailStartPct && (best-p)*100 >= best*s.cfg.TrailGiveBackPct:
			return s.exit(ik, st, fmt.Sprintf("GAMMA %s PREMIUM TRAIL premium=%d best=%d", st.Side, p, best))
		}
	}
	return nil
}

func (s *Nifty50Gamma) exit(key string, st *nifty50GammaState, reason string) *Signal {
	side := st.Side
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	s.flatten(st)
	exch, token := splitKey(key)
	return &Signal{StrategyName: s.Name(), Action: ActionExit, Side: side, Token: token, Exchange: exch, Qty: s.qty, Reason: reason}
}

func (s *Nifty50Gamma) flatten(st *nifty50GammaState) {
	st.Side, st.Strike, st.IndexEntry = SideNone, 0, 0
	st.FNOToken, st.FNOEntry, st.FNOBest = "", 0, 0
}

// PremiumExitsOnTicks marks that exits depend on premium ticks (backtests
// feed modeled premiums).
func (s *Nifty50Gamma) PremiumExitsOnTicks() bool { return true }

// CurrentFNOPosition reports the open option position, if priced.
func (s *Nifty50Gamma) CurrentFNOPosition() *LiveFNOPosition {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Side != SideNone && st.FNOEntry > 0 && st.FNOToken != "" {
			return &LiveFNOPosition{Side: st.Side, Token: st.FNOToken, EntryPrice: st.FNOEntry, BestPrice: st.FNOBest}
		}
	}
	return nil
}

func (s *Nifty50Gamma) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Signal
	for key, st := range s.instruments {
		if st.Side != SideNone {
			out = append(out, *s.exit(key, st, reason))
		}
	}
	return out
}

func (s *Nifty50Gamma) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		s.flatten(st)
		st.Pending = SideNone
	}
}

// ── Snapshot / Restore ──

type nifty50GammaSnap struct {
	Key         string               `json:"key"`
	Context     rangeContextSnapshot `json:"context"`
	Day         string               `json:"day"`
	DayHigh     int64                `json:"day_high"`
	DayLow      int64                `json:"day_low"`
	SetupDone   bool                 `json:"setup_done"`
	SetupOK     bool                 `json:"setup_ok"`
	RangeHi     int64                `json:"range_hi"`
	RangeLo     int64                `json:"range_lo"`
	Traded      bool                 `json:"traded"`
	Pending     PositionSide         `json:"pending"`
	PendingEdge int64                `json:"pending_edge"`
	Side        PositionSide         `json:"side"`
	Strike      int64                `json:"strike"`
	IndexEntry  int64                `json:"index_entry"`
	FNOToken    string               `json:"fno_token"`
	FNOEntry    int64                `json:"fno_entry"`
	FNOBest     int64                `json:"fno_best"`
	LastCloseTS time.Time            `json:"last_close_ts"`
}

func (s *Nifty50Gamma) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []nifty50GammaSnap
	for k, st := range s.instruments {
		out = append(out, nifty50GammaSnap{Key: k, Context: st.ctx.snapshot(), Day: st.Day, DayHigh: st.DayHigh, DayLow: st.DayLow,
			SetupDone: st.SetupDone, SetupOK: st.SetupOK, RangeHi: st.RangeHi, RangeLo: st.RangeLo, Traded: st.Traded,
			Pending: st.Pending, PendingEdge: st.PendingEdge, Side: st.Side, Strike: st.Strike, IndexEntry: st.IndexEntry,
			FNOToken: st.FNOToken, FNOEntry: st.FNOEntry, FNOBest: st.FNOBest, LastCloseTS: st.LastCloseTS})
	}
	return json.Marshal(map[string]any{"version": 1, "strategy": s.Name(), "instruments": out})
}

func (s *Nifty50Gamma) Restore(data []byte) error {
	var snap struct {
		Instruments []nifty50GammaSnap `json:"instruments"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := make(map[string]*nifty50GammaState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		st := &nifty50GammaState{ctx: restoreRangeContext(s.cfg.Range, is.Context), Day: is.Day, DayHigh: is.DayHigh, DayLow: is.DayLow,
			SetupDone: is.SetupDone, SetupOK: is.SetupOK, RangeHi: is.RangeHi, RangeLo: is.RangeLo, Traded: is.Traded,
			Pending: is.Pending, PendingEdge: is.PendingEdge, Side: is.Side, Strike: is.Strike, IndexEntry: is.IndexEntry,
			FNOToken: is.FNOToken, FNOEntry: is.FNOEntry, FNOBest: is.FNOBest, LastCloseTS: is.LastCloseTS}
		if st.Side == "" {
			st.Side = SideNone
		}
		if st.Pending == "" {
			st.Pending = SideNone
		}
		m[is.Key] = st
	}
	s.instruments = m
	return nil
}
