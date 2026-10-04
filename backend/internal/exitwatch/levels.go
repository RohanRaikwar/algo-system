package exitwatch

import (
	"sort"
	"time"

	"trading-systemv1/internal/model"
)

// Level types. Sources outside exitwatch keep their own label after a prefix.
const (
	LevelDayHigh   = "DAY_HIGH"
	LevelDayLow    = "DAY_LOW"
	LevelORBHigh   = "ORB_HIGH"
	LevelORBLow    = "ORB_LOW"
	LevelSwingHigh = "SWING_HIGH"
	LevelSwingLow  = "SWING_LOW"
	LevelMicroHigh = "MICRO_HIGH"
	LevelMicroLow  = "MICRO_LOW"
	ourSRPrefix    = "OUR_SR:"
	analystPrefix  = "ANALYST:"
)

// Level is one support/resistance price. Side is not stored: a level above
// price is resistance and below is support, so a broken resistance becomes
// support without any bookkeeping.
type Level struct {
	Price int64     `json:"price"` // paise
	Type  string    `json:"type"`
	Since time.Time `json:"since"` // when the book first saw this level
}

// levelPriority ranks types when levels cluster: the label of the highest
// priority member names the cluster. Our strategy's levels win.
func levelPriority(t string) int {
	switch {
	case len(t) >= len(ourSRPrefix) && t[:len(ourSRPrefix)] == ourSRPrefix:
		return 5
	case t == LevelSwingHigh || t == LevelSwingLow:
		return 4
	case t == LevelORBHigh || t == LevelORBLow:
		return 3
	case len(t) >= len(analystPrefix) && t[:len(analystPrefix)] == analystPrefix:
		return 2
	default: // day high/low
		return 1
	}
}

var bookIST = time.FixedZone("IST", 5*3600+30*60)

// LevelBook is the merged S/R book for one index, rebuilt from today's data:
// live day high/low, opening range, 1m fractal swings, analyst levels and
// our SR strategy's levels. Not safe for concurrent use.
type LevelBook struct {
	fractalBars int
	orbMin      int
	mergeTol    int64

	day     string // IST date the day sources belong to
	bars    []model.TFCandle
	dayHigh int64
	dayLow  int64
	orbHigh int64
	orbLow  int64
	orbDone bool
	swings  []Level

	analyst []Level
	ourSR   []Level

	clock   time.Time           // latest tick or candle-close time; dates new levels
	seen    map[int64]time.Time // first-seen time per raw level price
	merged  []Level
	dirty   bool
	version int
}

// NewLevelBook returns an empty book.
func NewLevelBook(m *Model) *LevelBook {
	b := &LevelBook{seen: map[int64]time.Time{}}
	b.configure(m)
	return b
}

func (b *LevelBook) configure(m *Model) {
	b.fractalBars, b.orbMin, b.mergeTol = m.FractalBars, m.ORBMin, m.LevelMergeTolPaise
	if b.fractalBars < 1 {
		b.fractalBars = 2
	}
}

// Version changes whenever the merged level set changes.
func (b *LevelBook) Version() int {
	b.rebuild()
	return b.version
}

// Levels returns the merged, price-sorted levels.
func (b *LevelBook) Levels() []Level {
	b.rebuild()
	return b.merged
}

func (b *LevelBook) rollDay(ts time.Time) {
	d := ts.In(bookIST).Format("2006-01-02")
	if d == b.day {
		return
	}
	b.day = d
	b.bars = b.bars[:0]
	b.dayHigh, b.dayLow, b.orbHigh, b.orbLow, b.orbDone = 0, 0, 0, 0, false
	b.swings = b.swings[:0]
	b.seen = map[int64]time.Time{}
	b.dirty = true
}

// OnTick updates the live day high/low.
func (b *LevelBook) OnTick(price int64, ts time.Time) {
	b.rollDay(ts)
	if ts.After(b.clock) {
		b.clock = ts
	}
	if b.dayHigh == 0 || price > b.dayHigh {
		b.dayHigh, b.dirty = price, true
	}
	if b.dayLow == 0 || price < b.dayLow {
		b.dayLow, b.dirty = price, true
	}
}

// OnCandle adds one closed 1m candle: day range, opening range, fractals.
// Candles must arrive in time order; repeats and forming bars are ignored.
func (b *LevelBook) OnCandle(c model.TFCandle) {
	if c.Forming {
		return
	}
	b.rollDay(c.TS)
	if n := len(b.bars); n > 0 && !c.TS.After(b.bars[n-1].TS) {
		return
	}
	if end := c.TS.Add(time.Minute); end.After(b.clock) {
		b.clock = end
	}
	b.bars = append(b.bars, c)
	if b.dayHigh == 0 || c.High > b.dayHigh {
		b.dayHigh = c.High
	}
	if b.dayLow == 0 || c.Low < b.dayLow {
		b.dayLow = c.Low
	}

	ist := c.TS.In(bookIST)
	min := ist.Hour()*60 + ist.Minute()
	open := 9*60 + 15
	switch {
	case min >= open && min < open+b.orbMin:
		if b.orbHigh == 0 || c.High > b.orbHigh {
			b.orbHigh = c.High
		}
		if b.orbLow == 0 || c.Low < b.orbLow {
			b.orbLow = c.Low
		}
		if min == open+b.orbMin-1 {
			b.orbDone = true
		}
	case min >= open+b.orbMin && b.orbHigh > 0:
		b.orbDone = true
	}

	// Fractal on the bar fractalBars back: strictly beyond its neighbours.
	k := b.fractalBars
	if i := len(b.bars) - 1 - k; i >= k {
		hi, lo := true, true
		for j := i - k; j <= i+k; j++ {
			if j == i {
				continue
			}
			if b.bars[j].High >= b.bars[i].High {
				hi = false
			}
			if b.bars[j].Low <= b.bars[i].Low {
				lo = false
			}
		}
		if hi {
			b.swings = append(b.swings, Level{Price: b.bars[i].High, Type: LevelSwingHigh, Since: c.TS})
		}
		if lo {
			b.swings = append(b.swings, Level{Price: b.bars[i].Low, Type: LevelSwingLow, Since: c.TS})
		}
	}
	b.dirty = true
}

// Bars returns today's closed 1m candles.
func (b *LevelBook) Bars() []model.TFCandle { return b.bars }

// SetAnalyst replaces the analyst levels.
func (b *LevelBook) SetAnalyst(ls []Level) { b.analyst, b.dirty = ls, true }

// SetOurSR replaces our SR strategy's levels.
func (b *LevelBook) SetOurSR(ls []Level) { b.ourSR, b.dirty = ls, true }

func (b *LevelBook) rebuild() {
	if !b.dirty {
		return
	}
	b.dirty = false
	now := b.clock
	if now.IsZero() {
		now = time.Now()
	}
	raw := make([]Level, 0, 8+len(b.swings)+len(b.analyst)+len(b.ourSR))
	add := func(p int64, t string, since time.Time) {
		if p <= 0 {
			return
		}
		if first, ok := b.seen[p]; ok {
			since = first
		} else {
			if since.IsZero() {
				since = now
			}
			b.seen[p] = since
		}
		raw = append(raw, Level{Price: p, Type: t, Since: since})
	}
	add(b.dayHigh, LevelDayHigh, time.Time{})
	add(b.dayLow, LevelDayLow, time.Time{})
	if b.orbDone {
		add(b.orbHigh, LevelORBHigh, time.Time{})
		add(b.orbLow, LevelORBLow, time.Time{})
	}
	for _, l := range b.swings {
		add(l.Price, l.Type, l.Since)
	}
	for _, l := range b.analyst {
		add(l.Price, l.Type, l.Since)
	}
	for _, l := range b.ourSR {
		add(l.Price, l.Type, l.Since)
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Price < raw[j].Price })

	// Cluster within mergeTol of the cluster's first price; the highest
	// priority member names it, the oldest member dates it.
	out := make([]Level, 0, len(raw))
	for i := 0; i < len(raw); {
		best, oldest := raw[i], raw[i].Since
		j := i + 1
		for ; j < len(raw) && raw[j].Price-raw[i].Price <= b.mergeTol; j++ {
			if levelPriority(raw[j].Type) > levelPriority(best.Type) {
				best = raw[j]
			}
			if raw[j].Since.Before(oldest) {
				oldest = raw[j].Since
			}
		}
		best.Since = oldest
		out = append(out, best)
		i = j
	}
	if !sameLevels(out, b.merged) {
		b.merged = out
		b.version++
	}
}

func sameLevels(a, b []Level) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Price != b[i].Price || a[i].Type != b[i].Type {
			return false
		}
	}
	return true
}

// nextLevel returns the first level more than tol beyond price in the
// favourable direction (sign +1 up, -1 down), or ok=false when none.
func nextLevel(ls []Level, price, sign, tol int64) (Level, bool) {
	if sign > 0 {
		for _, l := range ls {
			if l.Price > price+tol {
				return l, true
			}
		}
		return Level{}, false
	}
	for i := len(ls) - 1; i >= 0; i-- {
		if ls[i].Price < price-tol {
			return ls[i], true
		}
	}
	return Level{}, false
}
