package strategy

import "time"

// RangeView is the dashboard's read-only picture of NIFTY50_RANGE for one
// index: the situation it sees, what it is waiting for, and any position.
// Prices are int64 paise; strikes are index points.
type RangeView struct {
	Strategy string    `json:"strategy"`
	Key      string    `json:"key"`
	TS       time.Time `json:"ts"` // last 1m candle processed (bucket start)

	Regime     string  `json:"regime"` // RANGE | TRENDING | WARMING
	ADX        float64 `json:"adx"`
	MaxADX     float64 `json:"max_adx"`
	Support    int64   `json:"support,omitempty"`
	Resistance int64   `json:"resistance,omitempty"`
	Width      int64   `json:"width,omitempty"`
	Class      string  `json:"class,omitempty"`
	RSI5       float64 `json:"rsi5"`
	ATR15      int64   `json:"atr15,omitempty"`
	DayOpen    int64   `json:"day_open,omitempty"`

	FlagBoxLo    int64 `json:"flag_box_lo,omitempty"`
	FlagBoxHi    int64 `json:"flag_box_hi,omitempty"`
	FlagTight    bool  `json:"flag_tight"`
	FlagTrendDay bool  `json:"flag_trend_day"`
	DayTrend     int64 `json:"day_trend"` // latest close − day open

	PendingSide string `json:"pending_side,omitempty"` // CALL | PUT while a break awaits follow-through
	PendingKind string `json:"pending_kind,omitempty"`
	PendingEdge int64  `json:"pending_edge,omitempty"`

	Side   string `json:"side"` // NONE | CALL | PUT
	Kind   string `json:"kind,omitempty"`
	Entry  int64  `json:"entry,omitempty"`
	Stop   int64  `json:"stop,omitempty"`
	Target int64  `json:"target,omitempty"`
	Strike int64  `json:"strike,omitempty"`

	TradesToday   int  `json:"trades_today"`
	MaxTrades     int  `json:"max_trades"`
	EntryTF       int  `json:"entry_tf"`
	MeanReversion bool `json:"mean_reversion"`
	Breakout      bool `json:"breakout"`
	Flag          bool `json:"flag"`
	TimeExitMin   int  `json:"time_exit_min"`
}

// View returns the dashboard view for key ("NSE:99926000"); false before
// the first candle.
func (s *Nifty50Range) View(key string) (RangeView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.instruments[key]
	if !ok {
		return RangeView{}, false
	}
	rs := st.ctx.State()
	v := RangeView{
		Strategy: s.Name(), Key: key, TS: st.LastCloseTS,
		ADX: rs.ADX, MaxADX: s.cfg.Range.MaxADX, RSI5: rs.RSI5, ATR15: rs.ATR15, DayOpen: st.DayOpen,
		Side: string(st.Side), Kind: st.Kind, Entry: st.IndexEntry, Stop: st.StopLevel, Target: st.TargetLevel, Strike: st.Strike,
		TradesToday: st.TradesToday, MaxTrades: s.cfg.MaxTradesPerDay, EntryTF: s.cfg.EntryTFMinutes,
		MeanReversion: s.cfg.MeanReversionEnabled, Breakout: s.cfg.BreakoutEnabled, Flag: s.cfg.FlagEnabled,
		TimeExitMin: s.cfg.TimeExitMin,
	}
	switch {
	case !rs.ADXReady:
		v.Regime = "WARMING"
	case rs.InRange:
		v.Regime = "RANGE"
	default:
		v.Regime = "TRENDING"
	}
	if rs.Width > 0 {
		v.Support, v.Resistance, v.Width, v.Class = rs.Support, rs.Resistance, rs.Width, string(rs.Class)
	}
	if box, ok := s.flagBox(st); ok {
		v.FlagBoxLo, v.FlagBoxHi, v.FlagTight, v.FlagTrendDay, v.DayTrend = box.Lo, box.Hi, box.Tight, box.TrendDay, box.Trend
	}
	if st.PendingBreak != SideNone && st.PendingBreak != "" {
		v.PendingSide, v.PendingKind, v.PendingEdge = string(st.PendingBreak), st.PendingKind, st.PendingEdge
	}
	return v, true
}
