package strategy

import (
	"strings"
	"time"
)

// SRView is the dashboard's read-only picture of NIFTY50_SR for one index:
// the regime and levels it sees, what it is waiting for, why the last entry
// bar was refused, and any position. Prices are int64 paise; strikes are
// index points.
type SRView struct {
	Strategy string    `json:"strategy"`
	Key      string    `json:"key"`
	TS       time.Time `json:"ts"` // last 1m candle processed (bucket start)

	Regime  string  `json:"regime"` // RANGE | TREND_UP | TREND_DOWN | MIXED | DEAD | WARMING
	ADX     float64 `json:"adx"`
	RSI5    float64 `json:"rsi5"`
	ATR15   int64   `json:"atr15,omitempty"`
	VWAP    int64   `json:"vwap,omitempty"`
	EMAFast int64   `json:"ema_fast,omitempty"`
	EMASlow int64   `json:"ema_slow,omitempty"`
	ORHigh  int64   `json:"or_high,omitempty"`
	ORLow   int64   `json:"or_low,omitempty"`
	// Today's open/high/low and the previous session's high/low.
	DayOpen     int64     `json:"day_open,omitempty"`
	DayHigh     int64     `json:"day_high,omitempty"`
	DayLow      int64     `json:"day_low,omitempty"`
	PrevDayHigh int64     `json:"prev_day_high,omitempty"`
	PrevDayLow  int64     `json:"prev_day_low,omitempty"`
	Close       int64     `json:"close,omitempty"`
	Levels      []SRLevel `json:"levels"`

	PendingSide  string `json:"pending_side,omitempty"` // CALL | PUT while a break awaits its retest
	PendingLevel int64  `json:"pending_level,omitempty"`
	PendingLeft  int    `json:"pending_left,omitempty"` // entry bars left

	Side   string `json:"side"` // NONE | CALL | PUT
	Kind   string `json:"kind,omitempty"`
	Entry  int64  `json:"entry,omitempty"`
	Stop   int64  `json:"stop,omitempty"`
	Target int64  `json:"target,omitempty"`
	Strike int64  `json:"strike,omitempty"`

	// Block is the day/time cap refusing entries right now, "" if none.
	Block        string         `json:"block,omitempty"`
	LastReject   string         `json:"last_reject,omitempty"`
	LastRejectTS time.Time      `json:"last_reject_ts,omitempty"`
	Rejects      map[string]int `json:"rejects"` // today, per filter

	TradesToday  int   `json:"trades_today"`
	MaxTrades    int   `json:"max_trades"`
	ConsecLosses int   `json:"consec_losses"`
	DayPnLPts    int64 `json:"day_pnl_pts"` // index paise
	CooldownLeft int   `json:"cooldown_left"`

	EntryTF      int  `json:"entry_tf"`
	EntryFromMin int  `json:"entry_from_min"`
	EntryToMin   int  `json:"entry_to_min"`
	MinConfirms  int  `json:"min_confirmations"`
	Fade         bool `json:"fade"`
	Retest       bool `json:"retest"`
	Pullback     bool `json:"pullback"`

	// Day-extreme gate (0 = off): no CALL at ≥ DayExtremePct % of the day's
	// range after DayRunPts up from the low; PUT mirror with DayExtremePuts.
	DayExtremePct  int64 `json:"day_extreme_pct"`
	DayRunPts      int64 `json:"day_run_pts"`
	DayExtremePuts bool  `json:"day_extreme_puts"`

	// Sideways box: span of the last BoxBars 5m bars and the most it may
	// span to count as sideways (index paise); 0 = off or not enough bars.
	BoxPts   int64     `json:"box_pts,omitempty"`
	BoxLimit int64     `json:"box_limit,omitempty"`
	BoxBars  int       `json:"box_bars,omitempty"`
	BoxFrom  time.Time `json:"box_from,omitempty"` // first box bar start
	BoxTo    time.Time `json:"box_to,omitempty"`   // last box bar end
	BoxHigh  int64     `json:"box_high,omitempty"`
	BoxLow   int64     `json:"box_low,omitempty"`
	Boxed    bool      `json:"boxed"`

	// Warn is what a CALL / PUT entered now would be flagged with
	// ("call:day_extreme", "put:sideways"); Warns counts today's entries
	// taken with each warning. GateMode: warn | block.
	Warn     []string       `json:"warn,omitempty"`
	Warns    map[string]int `json:"warns,omitempty"`
	GateMode string         `json:"gate_mode"`
}

// View returns the dashboard view for key ("NSE:99926000"); false before
// the first candle.
func (s *Nifty50SR) View(key string) (SRView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.instruments[key]
	if !ok || st.LastCloseTS.IsZero() {
		return SRView{}, false
	}
	ss := st.ctx.State()
	v := SRView{
		Strategy: s.Name(), Key: key, TS: st.LastCloseTS,
		Regime: string(ss.Regime), ADX: ss.Range.ADX, RSI5: ss.Range.RSI5, ATR15: ss.Range.ATR15,
		VWAP: ss.VWAP, EMAFast: ss.EMAFast, EMASlow: ss.EMASlow, ORHigh: ss.ORHigh, ORLow: ss.ORLow,
		DayOpen: ss.DayOpen, DayHigh: ss.DayHigh, DayLow: ss.DayLow, PrevDayHigh: ss.PrevDayHigh, PrevDayLow: ss.PrevDayLow,
		DayExtremePct: s.cfg.DayExtremePct, DayRunPts: s.cfg.DayRunPts, DayExtremePuts: s.cfg.DayExtremePuts,
		Close: st.LastClose, Levels: append([]SRLevel{}, ss.Levels...),
		Side: string(st.Side), Kind: st.Kind, Entry: st.IndexEntry, Stop: st.StopLevel, Target: st.TargetLevel, Strike: st.Strike,
		LastReject: s.lastReject, LastRejectTS: s.lastRejectTS, Rejects: make(map[string]int, len(s.dayRejects)),
		TradesToday: st.TradesToday, MaxTrades: s.cfg.MaxTradesPerDay, ConsecLosses: st.ConsecLosses,
		DayPnLPts: st.DayPnLPts, CooldownLeft: st.CooldownLeft,
		EntryTF: s.entryTF(), EntryFromMin: s.cfg.EntryFromMin, EntryToMin: s.cfg.EntryToMin,
		MinConfirms: s.cfg.MinConfirmations,
		Fade:        s.cfg.FadeEnabled, Retest: s.cfg.RetestEnabled, Pullback: s.cfg.PullbackEnabled,
	}
	if box, boxed := s.sideways(st, ss); box > 0 {
		v.BoxPts, v.BoxLimit, v.BoxBars, v.Boxed = box, ss.Range.ATR15*s.cfg.BoxATRPct/100, s.cfg.BoxBars, boxed
		bars := st.ctx.rc.bars5[len(st.ctx.rc.bars5)-s.cfg.BoxBars:]
		v.BoxFrom, v.BoxTo = bars[0].TS, bars[len(bars)-1].TS.Add(5*time.Minute)
		v.BoxHigh, v.BoxLow = bars[0].High, bars[0].Low
		for _, b := range bars[1:] {
			v.BoxHigh, v.BoxLow = maxInt64(v.BoxHigh, b.High), minInt64(v.BoxLow, b.Low)
		}
	}
	v.GateMode = s.cfg.GateMode
	if st.LastClose > 0 {
		day, closeMin := dayPosition(ss, st.LastClose), minutesIST(st.LastCloseTS)+1
		for _, side := range []PositionSide{SideCall, SidePut} {
			for _, w := range s.situationWarnings(st, ss, side, SRKindPullback, day, closeMin) {
				v.Warn = append(v.Warn, strings.ToLower(string(side))+":"+w)
			}
		}
	}
	if len(s.dayWarns) > 0 {
		v.Warns = make(map[string]int, len(s.dayWarns))
		for k, n := range s.dayWarns {
			v.Warns[k] = n
		}
	}
	if ss.Regime == SRRegimeNone {
		v.Regime = "WARMING"
	}
	for k, n := range s.dayRejects {
		v.Rejects[k] = n
	}
	if st.PendingSide != SideNone && st.PendingSide != "" {
		v.PendingSide, v.PendingLevel, v.PendingLeft = string(st.PendingSide), st.PendingLevel, st.PendingLeft
	}
	if st.Side == SideNone || st.Side == "" {
		v.Side = string(SideNone)
		v.Block = s.entryBlock(st, minutesIST(st.LastCloseTS)+1)
	}
	return v, true
}
