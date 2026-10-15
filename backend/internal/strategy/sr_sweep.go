package strategy

import "time"

// ════════════════════════════════════════════════════════════════════
//  Liquidity sweep — a 1m bar that opens inside a key level, wicks
//  through it by at least SweepMinWickPts and closes back inside: the
//  stops beyond the level were taken and price came back (stop hunt).
//  Warning only: a CALL just after a high sweep (or a PUT after a low
//  sweep) is flagged, never refused. Prices are int64 paise.
// ════════════════════════════════════════════════════════════════════

// Sweep sides.
const (
	SRSweepHigh = "HIGH" // resistance swept, bearish warning
	SRSweepLow  = "LOW"  // support swept, bullish warning
)

// Sweep level sources besides the SRLevel ones.
const (
	SRSweepDayHigh = "DAY_HIGH"
	SRSweepDayLow  = "DAY_LOW"
)

// maxSweepsKept is how many of today's sweeps the view shows.
const maxSweepsKept = 5

// SRSweep is one detected liquidity sweep.
type SRSweep struct {
	Side    string    `json:"side"`   // HIGH | LOW
	Level   int64     `json:"level"`  // paise
	Source  string    `json:"source"` // PREV_DAY | OPENING | DAY_HIGH | DAY_LOW | SWING
	Wick    int64     `json:"wick"`   // paise beyond the level
	Extreme int64     `json:"extreme"`
	Close   int64     `json:"close"`
	Touches int       `json:"touches,omitempty"`
	TS      time.Time `json:"ts"` // bucket start of the sweep bar
}

type sweepLevel struct {
	price   int64
	source  string
	touches int
}

// sweepRank orders sources when one bar sweeps several levels.
func sweepRank(source string) int {
	switch source {
	case SRLevelPrevDay:
		return 4
	case SRLevelOpening:
		return 3
	case SRSweepDayHigh, SRSweepDayLow:
		return 2
	}
	return 1
}

// sweepLevels lists the levels a bar can sweep, from the state as it
// stood before that bar (so the day high/low is the one the bar broke).
func sweepLevels(ss SRState) []sweepLevel {
	out := make([]sweepLevel, 0, len(ss.Levels)+6)
	add := func(p int64, src string, touches int) {
		if p > 0 {
			out = append(out, sweepLevel{price: p, source: src, touches: touches})
		}
	}
	add(ss.PrevDayHigh, SRLevelPrevDay, 1)
	add(ss.PrevDayLow, SRLevelPrevDay, 1)
	add(ss.ORHigh, SRLevelOpening, 1)
	add(ss.ORLow, SRLevelOpening, 1)
	if ss.DayHigh > ss.DayLow && ss.DayLow > 0 {
		add(ss.DayHigh, SRSweepDayHigh, 1)
		add(ss.DayLow, SRSweepDayLow, 1)
	}
	for _, l := range ss.Levels {
		add(l.Price, l.Source, l.Touches)
	}
	return out
}

// detectSweep returns the sweep bar b makes of levels, false if none.
// High sweep: open and close at or below the level, high at least
// minWick above it. Low sweep mirrors it. When several levels qualify
// the strongest source wins (then more touches, then the bigger wick);
// a bar sweeping both sides keeps the bigger wick.
func detectSweep(b ohlcv, ts time.Time, levels []sweepLevel, minWick int64) (SRSweep, bool) {
	if minWick <= 0 {
		return SRSweep{}, false
	}
	var best SRSweep
	found := false
	better := func(c SRSweep) bool {
		if !found {
			return true
		}
		if c.Side != best.Side {
			return c.Wick > best.Wick
		}
		if rc, rb := sweepRank(c.Source), sweepRank(best.Source); rc != rb {
			return rc > rb
		}
		if c.Touches != best.Touches {
			return c.Touches > best.Touches
		}
		return c.Wick > best.Wick
	}
	for _, l := range levels {
		var c SRSweep
		switch {
		case b.Open <= l.price && b.Close <= l.price && b.High-l.price >= minWick:
			c = SRSweep{Side: SRSweepHigh, Wick: b.High - l.price, Extreme: b.High}
		case b.Open >= l.price && b.Close >= l.price && l.price-b.Low >= minWick:
			c = SRSweep{Side: SRSweepLow, Wick: l.price - b.Low, Extreme: b.Low}
		default:
			continue
		}
		c.Level, c.Source, c.Touches, c.Close, c.TS = l.price, l.source, l.touches, b.Close, ts
		if better(c) {
			best, found = c, true
		}
	}
	return best, found
}

// recordSweep keeps sw unless the same level was swept within the
// cooldown; true when kept.
func (st *nifty50SRState) recordSweep(sw SRSweep, cooldown time.Duration) bool {
	if last, ok := st.sweepAt[sw.Level]; ok && sw.TS.Sub(last) < cooldown {
		return false
	}
	if st.sweepAt == nil {
		st.sweepAt = make(map[int64]time.Time)
	}
	st.sweepAt[sw.Level] = sw.TS
	st.Sweeps = append(st.Sweeps, sw)
	if len(st.Sweeps) > maxSweepsKept {
		st.Sweeps = st.Sweeps[len(st.Sweeps)-maxSweepsKept:]
	}
	return true
}

// sweepWarning is the warning an entry on side gets from a sweep against
// it in the last SweepWarnMin minutes before the bar at ts closed: a CALL
// after a high sweep, a PUT after a low sweep. "" when none.
func (s *Nifty50SR) sweepWarning(st *nifty50SRState, side PositionSide, ts time.Time) string {
	if s.cfg.SweepWarnMin <= 0 {
		return ""
	}
	window := time.Duration(s.cfg.SweepWarnMin) * time.Minute
	for i := len(st.Sweeps) - 1; i >= 0; i-- {
		sw := st.Sweeps[i]
		if ts.Sub(sw.TS) >= window {
			break
		}
		if ts.Before(sw.TS) {
			continue
		}
		if side == SideCall && sw.Side == SRSweepHigh {
			return "sweep_high"
		}
		if side == SidePut && sw.Side == SRSweepLow {
			return "sweep_low"
		}
	}
	return ""
}
