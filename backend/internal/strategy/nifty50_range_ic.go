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
//  NIFTY50 RANGE IC — iron condor in a strong range (guide Strategy 3)
//
//  Sell OTM CE + buy further OTM CE, sell OTM PE + buy further OTM PE,
//  outside the detected range. Paper-only: legs go through the
//  executor's multi-leg path, which never reaches the broker.
//
//  Entry (one condor per day): range regime with 15m ADX below a
//  stricter cap, Bollinger squeeze, entry window, and 1..5 days to expiry.
//  Exit: net debit falls to (1-ProfitTargetPct) of the credit, rises
//  to (1+StopLossPct) of it, spot closes beyond a short strike, or 15:00 (before NSE's closing auction session).
//  Intraday by design: the global EOD exit flattens everything anyway.
//  An intraday condor only earns the hours of time decay it holds, so it
//  trades the day before expiry (1 DTE) by default, when decay is fastest
//  (the picker never trades same-day expiry), and opens early (09:30-11:00)
//  to hold as many of those hours as possible.
// ════════════════════════════════════════════════════════════════════

// Leg ids of a condor.
const (
	LegLongCE  = "LONG_CE"
	LegShortCE = "SHORT_CE"
	LegShortPE = "SHORT_PE"
	LegLongPE  = "LONG_PE"
)

// Nifty50RangeICConfig configures the condor.
type Nifty50RangeICConfig struct {
	Range RangeContextConfig

	MaxADX          float64    // stricter than the range regime (guide: strong range)
	RequireSqueeze  bool       // Bollinger bands squeezing ("low volatility expected")
	EntryWindow     timeWindow // minutes after midnight IST
	MinDTE          int        // calendar days to expiry, inclusive
	MaxDTE          int
	StrikeStep      int64 // index points (50 for NIFTY)
	ShortOffsetPts  int64 // index points beyond the range edge for the short strike
	WingWidthPts    int64 // index points between short and long strike
	ProfitTargetPct int64 // close when debit ≤ credit × (100-this)/100
	StopLossPct     int64 // close when debit ≥ credit × (100+this)/100
	MinCreditPct    int64 // skip a condor whose net credit < this % of the wing width
	TimeExitMin     int

	IndexToken   string
	NameOverride string
}

// DefaultNifty50RangeICConfig returns the guide's example setup
// (range 23,900-24,100 → 24,200/24,300 CE, 23,800/23,700 PE).
func DefaultNifty50RangeICConfig() Nifty50RangeICConfig {
	rc := DefaultRangeContextConfig()
	rc.RequireFlatEMA = true // selling premium needs the stronger range test
	return Nifty50RangeICConfig{
		Range:           rc,
		MaxADX:          20,
		RequireSqueeze:  true,
		EntryWindow:     timeWindow{From: hhmm(9, 30), To: hhmm(11, 0)},
		MinDTE:          1,
		MaxDTE:          1,
		StrikeStep:      50,
		ShortOffsetPts:  100,
		WingWidthPts:    100,
		ProfitTargetPct: 50,
		StopLossPct:     100,
		MinCreditPct:    20,
		TimeExitMin:     hhmm(15, 0),
		IndexToken:      "NSE:99926000",
		NameOverride:    "NIFTY50_RANGE_IC",
	}
}

type icLeg struct {
	Leg        string `json:"leg"`
	Strike     int64  `json:"strike"`
	OptionType string `json:"option_type"`
	Short      bool   `json:"short"`
	Token      string `json:"token"` // "NFO:<token>" once resolved
	Entry      int64  `json:"entry"` // first premium seen after open (paise)
	LTP        int64  `json:"ltp"`
}

type nifty50RangeICState struct {
	ctx *rangeContext

	Open        bool
	Legs        []icLeg
	OpenedAt    time.Time
	TradeDay    string
	TradedToday bool
	LastCloseTS time.Time
}

// Nifty50RangeIC is the iron-condor strategy.
type Nifty50RangeIC struct {
	mu          sync.Mutex
	instruments map[string]*nifty50RangeICState
	qty         int64
	cfg         Nifty50RangeICConfig
	expiry      time.Time
}

func NewNifty50RangeIC(qty int64, cfg Nifty50RangeICConfig) *Nifty50RangeIC {
	return &Nifty50RangeIC{instruments: make(map[string]*nifty50RangeICState, 1), qty: qty, cfg: cfg}
}

func (s *Nifty50RangeIC) Name() string {
	if s.cfg.NameOverride != "" {
		return s.cfg.NameOverride
	}
	return "NIFTY50_RANGE_IC"
}

func (s *Nifty50RangeIC) Config() Nifty50RangeICConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SetExpiry sets the weekly expiry the legs trade, for days-to-expiry.
func (s *Nifty50RangeIC) SetExpiry(expiry time.Time) {
	s.mu.Lock()
	s.expiry = expiry
	s.mu.Unlock()
}

// SetLegTokens records the resolved option token ("NFO:<token>") per leg id.
func (s *Nifty50RangeIC) SetLegTokens(tokens map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if !st.Open {
			continue
		}
		for i := range st.Legs {
			if tok, ok := tokens[st.Legs[i].Leg]; ok {
				st.Legs[i].Token = tok
			}
		}
	}
}

// MinCredit is the smallest net credit per unit (paise) worth opening a
// condor for: MinCreditPct of the wing width. Below it the max loss is
// too large for what the trade can earn.
func (s *Nifty50RangeIC) MinCredit() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.WingWidthPts * 100 * s.cfg.MinCreditPct / 100
}

// CancelBasket drops an open condor whose legs could not be placed.
func (s *Nifty50RangeIC) CancelBasket(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Open {
			log.Printf("[strategy] %s: condor cancelled — %s", s.Name(), reason)
			st.Open, st.Legs = false, nil
		}
	}
}

func (s *Nifty50RangeIC) Warmup(candles []model.TFCandle) int {
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

func (s *Nifty50RangeIC) getOrCreate(key string) *nifty50RangeICState {
	st, ok := s.instruments[key]
	if !ok {
		st = &nifty50RangeICState{ctx: newRangeContext(s.cfg.Range)}
		s.instruments[key] = st
	}
	return st
}

// daysToExpiry counts calendar days from ts's IST date to the expiry date.
func daysToExpiry(ts, expiry time.Time) int {
	if expiry.IsZero() {
		return -1
	}
	a := ts.In(ist)
	e := expiry.In(ist)
	d0 := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, ist)
	d1 := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, ist)
	return int(d1.Sub(d0).Hours() / 24)
}

func roundUp(x, step int64) int64 {
	if r := x % step; r != 0 {
		return x + step - r
	}
	return x
}

func roundDown(x, step int64) int64 { return x - x%step }

// condorStrikes returns short/long CE and PE strikes (index points) for a
// range given in paise.
func (s *Nifty50RangeIC) condorStrikes(sup, res int64) (shortCE, longCE, shortPE, longPE int64) {
	step := s.cfg.StrikeStep
	shortCE = roundUp(roundUp((res+99)/100, step)+s.cfg.ShortOffsetPts, step)
	longCE = shortCE + s.cfg.WingWidthPts
	shortPE = roundDown(roundDown(sup/100, step)-s.cfg.ShortOffsetPts, step)
	longPE = shortPE - s.cfg.WingWidthPts
	return
}

func (s *Nifty50RangeIC) OnTFCandle(candle model.TFCandle) *Signal {
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
	rs := st.ctx.State()

	day := candle.TS.In(ist).Format("2006-01-02")
	if st.TradeDay != day {
		st.TradeDay, st.TradedToday = day, false
	}
	closeMin := minutesIST(candle.TS) + 1
	if closeMin <= sessionOpenMin || closeMin > hhmm(15, 30) {
		return nil
	}

	if st.Open {
		if closeMin >= s.cfg.TimeExitMin {
			return s.exitSignal(candle.Key(), st, fmt.Sprintf("IC TIME EXIT close=%d", candle.Close))
		}
		if sc, _ := st.leg(LegShortCE); sc != nil && candle.Close >= sc.Strike*100 {
			return s.exitSignal(candle.Key(), st, fmt.Sprintf("IC BREACH above short CE %d close=%d", sc.Strike, candle.Close))
		}
		if sp, _ := st.leg(LegShortPE); sp != nil && candle.Close <= sp.Strike*100 {
			return s.exitSignal(candle.Key(), st, fmt.Sprintf("IC BREACH below short PE %d close=%d", sp.Strike, candle.Close))
		}
		return nil
	}

	if st.TradedToday || closeMin < s.cfg.EntryWindow.From || closeMin >= s.cfg.EntryWindow.To {
		return nil
	}
	if !rs.InRange || rs.ADX >= s.cfg.MaxADX || (s.cfg.RequireSqueeze && !rs.Squeeze) {
		return nil
	}
	if dte := daysToExpiry(candle.TS, s.expiry); dte < s.cfg.MinDTE || dte > s.cfg.MaxDTE {
		return nil
	}
	if candle.Close <= rs.Support || candle.Close >= rs.Resistance {
		return nil
	}

	shortCE, longCE, shortPE, longPE := s.condorStrikes(rs.Support, rs.Resistance)
	st.Open, st.TradedToday, st.OpenedAt = true, true, candle.TS
	st.Legs = []icLeg{
		{Leg: LegLongCE, Strike: longCE, OptionType: "CE"},
		{Leg: LegShortCE, Strike: shortCE, OptionType: "CE", Short: true},
		{Leg: LegLongPE, Strike: longPE, OptionType: "PE"},
		{Leg: LegShortPE, Strike: shortPE, OptionType: "PE", Short: true},
	}
	reason := fmt.Sprintf("IC ENTRY range=%d-%d adx=%.1f dte=%d sell %dCE/%dPE buy %dCE/%dPE max_loss≈%d pts/unit close=%d",
		rs.Support, rs.Resistance, rs.ADX, daysToExpiry(candle.TS, s.expiry),
		shortCE, shortPE, longCE, longPE, s.cfg.WingWidthPts, candle.Close)
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	return &Signal{
		StrategyName: s.Name(), Action: ActionBuy, Side: SideCall,
		Token: candle.Token, Exchange: candle.Exchange, Qty: s.qty,
		MarketState: string(EntryMarketStateRange), Reason: reason,
		Legs: st.legSpecs(),
	}
}

func (st *nifty50RangeICState) leg(id string) (*icLeg, int) {
	for i := range st.Legs {
		if st.Legs[i].Leg == id {
			return &st.Legs[i], i
		}
	}
	return nil, -1
}

func (st *nifty50RangeICState) legSpecs() []LegSpec {
	out := make([]LegSpec, 0, len(st.Legs))
	for _, l := range st.Legs {
		out = append(out, LegSpec{Leg: l.Leg, Strike: l.Strike, OptionType: l.OptionType, Short: l.Short, Token: unqualify(l.Token)})
	}
	return out
}

func unqualify(token string) string {
	if i := indexOf(token, ":"); i >= 0 {
		return token[i+1:]
	}
	return token
}

// credit and debit per unit in paise; ok once every leg has a price.
func (st *nifty50RangeICState) creditDebit() (credit, debit int64, ok bool) {
	if len(st.Legs) == 0 {
		return 0, 0, false
	}
	for _, l := range st.Legs {
		if l.Entry <= 0 || l.LTP <= 0 {
			return 0, 0, false
		}
		if l.Short {
			credit += l.Entry
			debit += l.LTP
		} else {
			credit -= l.Entry
			debit -= l.LTP
		}
	}
	return credit, debit, true
}

func (s *Nifty50RangeIC) OnTick(tick model.Tick) *Signal {
	if tick.Price <= 0 {
		return nil
	}
	key := tick.Exchange + ":" + tick.Token
	s.mu.Lock()
	defer s.mu.Unlock()
	for ik, st := range s.instruments {
		if !st.Open {
			continue
		}
		hit := false
		for i := range st.Legs {
			if st.Legs[i].Token == key {
				st.Legs[i].LTP = tick.Price
				if st.Legs[i].Entry == 0 {
					st.Legs[i].Entry = tick.Price
				}
				hit = true
			}
		}
		if !hit {
			continue
		}
		credit, debit, ok := st.creditDebit()
		if !ok || credit <= 0 {
			continue
		}
		switch {
		case debit*100 <= credit*(100-s.cfg.ProfitTargetPct):
			return s.exitSignal(ik, st, fmt.Sprintf("IC PROFIT TARGET credit=%d debit=%d", credit, debit))
		case debit*100 >= credit*(100+s.cfg.StopLossPct):
			return s.exitSignal(ik, st, fmt.Sprintf("IC STOP LOSS credit=%d debit=%d", credit, debit))
		}
	}
	return nil
}

// exitSignal closes every leg; stratengine orders the fan-out (shorts first).
func (s *Nifty50RangeIC) exitSignal(key string, st *nifty50RangeICState, reason string) *Signal {
	log.Printf("[strategy] %s: %s", s.Name(), reason)
	legs := st.legSpecs()
	st.Open, st.Legs = false, nil
	exch, token := splitKey(key)
	return &Signal{
		StrategyName: s.Name(), Action: ActionExit, Side: SideCall,
		Token: token, Exchange: exch, Qty: s.qty, Reason: reason, Legs: legs,
	}
}

func (s *Nifty50RangeIC) ForceExitAll(reason string) []Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Signal
	for key, st := range s.instruments {
		if st.Open {
			out = append(out, *s.exitSignal(key, st, reason))
		}
	}
	return out
}

func (s *Nifty50RangeIC) ResetPositions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		st.Open, st.Legs = false, nil
	}
}

// OpenLegs returns a copy of the open condor legs (for the dashboard).
func (s *Nifty50RangeIC) OpenLegs() []LegSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.instruments {
		if st.Open {
			return st.legSpecs()
		}
	}
	return nil
}

// ── Snapshot / Restore ──

type nifty50RangeICSnapshot struct {
	Version     int                         `json:"version"`
	Strategy    string                      `json:"strategy"`
	Instruments []nifty50RangeICInstrumentS `json:"instruments"`
}

type nifty50RangeICInstrumentS struct {
	Key         string               `json:"key"`
	Context     rangeContextSnapshot `json:"context"`
	Open        bool                 `json:"open"`
	Legs        []icLeg              `json:"legs"`
	OpenedAt    time.Time            `json:"opened_at"`
	TradeDay    string               `json:"trade_day"`
	TradedToday bool                 `json:"traded_today"`
	LastCloseTS time.Time            `json:"last_close_ts"`
}

func (s *Nifty50RangeIC) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := nifty50RangeICSnapshot{Version: 1, Strategy: s.Name()}
	for k, st := range s.instruments {
		snap.Instruments = append(snap.Instruments, nifty50RangeICInstrumentS{
			Key: k, Context: st.ctx.snapshot(), Open: st.Open, Legs: append([]icLeg(nil), st.Legs...),
			OpenedAt: st.OpenedAt, TradeDay: st.TradeDay, TradedToday: st.TradedToday, LastCloseTS: st.LastCloseTS,
		})
	}
	return json.Marshal(snap)
}

func (s *Nifty50RangeIC) Restore(data []byte) error {
	var snap nifty50RangeICSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := make(map[string]*nifty50RangeICState, len(snap.Instruments))
	for _, is := range snap.Instruments {
		m[is.Key] = &nifty50RangeICState{
			ctx: restoreRangeContext(s.cfg.Range, is.Context), Open: is.Open, Legs: is.Legs,
			OpenedAt: is.OpenedAt, TradeDay: is.TradeDay, TradedToday: is.TradedToday, LastCloseTS: is.LastCloseTS,
		}
	}
	s.instruments = m
	return nil
}
