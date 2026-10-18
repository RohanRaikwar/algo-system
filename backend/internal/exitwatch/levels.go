package exitwatch

import (
	"sort"
	"strings"
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

	// Higher-timeframe fractal swings, kept across days.
	LevelSwingHigh5m = "SWING_HIGH_5M"
	LevelSwingLow5m  = "SWING_LOW_5M"
	LevelSwingHigh1h = "SWING_HIGH_1H"
	LevelSwingLow1h  = "SWING_LOW_1H"

	ourSRPrefix   = "OUR_SR:"
	analystPrefix = "ANALYST:"
)

// Level is one support/resistance price. Side is not stored: a level above
// price is resistance and below is support, so a broken resistance becomes
// support without any bookkeeping.
type Level struct {
	Price int64     `json:"price"` // paise
	Type  string    `json:"type"`
	Since time.Time `json:"since"` // when the book first saw this level

	// Strong: a member of the merged cluster is a 5m/1h swing or one of
	// our SR strategy's levels. The pre-target rejection rule only uses
	// strong levels, so 1m fractal noise cannot end a trade early.
	Strong bool   `json:"strong,omitempty"`
	Tags   string `json:"tags,omitempty"` // member types, "+"-joined, label first

	// Touches: our SR strategy's touch count (highest in the cluster).
	Touches int `json:"touches,omitempty"`
}

// levelPriority ranks types when levels cluster: the label of the highest
// priority member names the cluster. Our strategy's levels win.
func levelPriority(t string) int {
	switch {
	case strings.HasPrefix(t, ourSRPrefix):
		return 50
	case t == LevelSwingHigh1h || t == LevelSwingLow1h:
		return 46
	case t == LevelSwingHigh5m || t == LevelSwingLow5m:
		return 43
	case t == LevelSwingHigh || t == LevelSwingLow:
		return 40
	case t == LevelORBHigh || t == LevelORBLow:
		return 30
	case strings.HasPrefix(t, analystPrefix):
		return 20
	default: // day high/low
		return 10
	}
}

// Strong level sources the pre-target rejection rule can use.
const (
	SourceOurSR = "sr"
	Source5m    = "5m"
	Source1h    = "1h"
)

// levelSource names the strong source of a level type, "" when not strong.
func levelSource(t string) string {
	switch t {
	case LevelSwingHigh5m, LevelSwingLow5m:
		return Source5m
	case LevelSwingHigh1h, LevelSwingLow1h:
		return Source1h
	}
	if strings.HasPrefix(t, ourSRPrefix) {
		return SourceOurSR
	}
	return ""
}

func strongType(t string) bool { return levelSource(t) != "" }

// qualifies reports whether a merged level counts for the pre-target rule:
// a member from one of srcs, and for our SR levels at least minTouches.
func (l Level) qualifies(srcs []string, minTouches int) bool {
	for _, t := range strings.Split(l.Tags, "+") {
		s := levelSource(t)
		if s == "" || (s == SourceOurSR && l.Touches < minTouches) {
			continue
		}
		for _, want := range srcs {
			if s == want {
				return true
			}
		}
	}
	return false
}

// htfSeries is one higher timeframe's closed bars and their swings.
type htfSeries struct {
	bars   []model.TFCandle
	swings []Level
}

var bookIST = time.FixedZone("IST", 5*3600+30*60)

// LevelBook is the merged S/R book for one index, rebuilt from today's data:
// live day high/low, opening range, 1m fractal swings, analyst levels and
// our SR strategy's levels, plus 5m/1h swings of the last few days.
// Not safe for concurrent use.
type LevelBook struct {
	fractalBars    int
	htfFractalBars int
	htfDays        int
	orbMin         int
	mergeTol       int64

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
	htf     map[int]*htfSeries // by TF seconds; not reset by the day roll
	adx     float64            // our SR strategy's 15m ADX; 0 = unknown

	clock   time.Time           // latest tick or candle-close time; dates new levels
	seen    map[int64]time.Time // first-seen time per raw level price
	merged  []Level
	dirty   bool
	version int
}

// NewLevelBook returns an empty book.
func NewLevelBook(m *Model) *LevelBook {
	b := &LevelBook{seen: map[int64]time.Time{}, htf: map[int]*htfSeries{}}
	b.configure(m)
	return b
}

func (b *LevelBook) configure(m *Model) {
	b.fractalBars, b.orbMin, b.mergeTol = m.FractalBars, m.ORBMin, m.LevelMergeTolPaise
	if b.fractalBars < 1 {
		b.fractalBars = 2
	}
	b.htfFractalBars, b.htfDays = m.HTFFractalBars, m.HTFLookbackDays
	if b.htfFractalBars < 1 {
		b.htfFractalBars = 2
	}
	if b.htfDays < 1 {
		b.htfDays = 5
	}
	for tf, s := range b.htf {
		b.trimHTF(tf, s)
	}
	b.dirty = true
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
		hi, lo := fractalAt(b.bars, i, k)
		if hi {
			b.swings = append(b.swings, Level{Price: b.bars[i].High, Type: LevelSwingHigh, Since: c.TS})
		}
		if lo {
			b.swings = append(b.swings, Level{Price: b.bars[i].Low, Type: LevelSwingLow, Since: c.TS})
		}
	}
	b.dirty = true
}

// fractalAt reports whether bar i's high (low) is strictly beyond the k
// bars on each side. The caller ensures k <= i < len(bars)-k.
func fractalAt(bars []model.TFCandle, i, k int) (hi, lo bool) {
	hi, lo = true, true
	for j := i - k; j <= i+k; j++ {
		if j == i {
			continue
		}
		if bars[j].High >= bars[i].High {
			hi = false
		}
		if bars[j].Low <= bars[i].Low {
			lo = false
		}
	}
	return hi, lo
}

// OnHTFCandle adds one closed 5m or 1h candle. Its swings survive the day
// roll and are kept for the last HTFLookbackDays sessions. Other TFs,
// forming bars and repeats are ignored.
func (b *LevelBook) OnHTFCandle(c model.TFCandle) {
	hiType, _ := htfTypes(c.TF)
	if c.Forming || hiType == "" {
		return
	}
	s := b.htf[c.TF]
	if s == nil {
		s = &htfSeries{}
		b.htf[c.TF] = s
	}
	if n := len(s.bars); n > 0 && !c.TS.After(s.bars[n-1].TS) {
		return
	}
	s.bars = append(s.bars, c)
	b.trimHTF(c.TF, s)
	b.dirty = true
}

// htfTypes names the swing types of a higher timeframe ("" when unused).
func htfTypes(tf int) (hi, lo string) {
	switch tf {
	case 300:
		return LevelSwingHigh5m, LevelSwingLow5m
	case 3600:
		return LevelSwingHigh1h, LevelSwingLow1h
	}
	return "", ""
}

// trimHTF keeps the bars of the last htfDays sessions and recomputes the
// series' swings. A swing is dated by the bar that confirms it.
func (b *LevelBook) trimHTF(tf int, s *htfSeries) {
	perDay := 375*60/tf + 1 // bars in a 09:15–15:30 session
	if max := b.htfDays * perDay; len(s.bars) > max {
		s.bars = append(s.bars[:0], s.bars[len(s.bars)-max:]...)
	}
	hiType, loType := htfTypes(tf)
	k := b.htfFractalBars
	s.swings = s.swings[:0]
	for i := k; i+k < len(s.bars); i++ {
		hi, lo := fractalAt(s.bars, i, k)
		since := s.bars[i+k].TS.Add(time.Duration(tf) * time.Second)
		if hi {
			s.swings = append(s.swings, Level{Price: s.bars[i].High, Type: hiType, Since: since})
		}
		if lo {
			s.swings = append(s.swings, Level{Price: s.bars[i].Low, Type: loType, Since: since})
		}
	}
}

// Bars returns today's closed 1m candles.
func (b *LevelBook) Bars() []model.TFCandle { return b.bars }

// SetAnalyst replaces the analyst levels.
func (b *LevelBook) SetAnalyst(ls []Level) { b.analyst, b.dirty = ls, true }

// SetOurSR replaces our SR strategy's levels.
func (b *LevelBook) SetOurSR(ls []Level) { b.ourSR, b.dirty = ls, true }

// SetTrend sets our SR strategy's 15m ADX (trend strength).
func (b *LevelBook) SetTrend(adx float64) { b.adx = adx }

// ADX is the latest 15m ADX, 0 when unknown.
func (b *LevelBook) ADX() float64 { return b.adx }

func (b *LevelBook) rebuild() {
	if !b.dirty {
		return
	}
	b.dirty = false
	now := b.clock
	if now.IsZero() {
		now = time.Now()
	}
	raw := make([]Level, 0, 8+len(b.swings)+len(b.analyst)+len(b.ourSR)+4*len(b.htf))
	add := func(p int64, t string, since time.Time, touches int) {
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
		raw = append(raw, Level{Price: p, Type: t, Since: since, Touches: touches})
	}
	add(b.dayHigh, LevelDayHigh, time.Time{}, 0)
	add(b.dayLow, LevelDayLow, time.Time{}, 0)
	if b.orbDone {
		add(b.orbHigh, LevelORBHigh, time.Time{}, 0)
		add(b.orbLow, LevelORBLow, time.Time{}, 0)
	}
	for _, l := range b.swings {
		add(l.Price, l.Type, l.Since, 0)
	}
	for _, l := range b.analyst {
		add(l.Price, l.Type, l.Since, 0)
	}
	for _, l := range b.ourSR {
		add(l.Price, l.Type, l.Since, l.Touches)
	}
	for _, tf := range []int{300, 3600} {
		if s := b.htf[tf]; s != nil {
			for _, l := range s.swings {
				add(l.Price, l.Type, l.Since, 0)
			}
		}
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Price < raw[j].Price })

	// Cluster within mergeTol of the cluster's first price; the highest
	// priority member names it, the oldest member dates it, and any strong
	// member makes it strong.
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
		best.Strong, best.Tags = clusterTags(raw[i:j], best.Type)
		best.Touches = 0
		for _, m := range raw[i:j] {
			if m.Touches > best.Touches {
				best.Touches = m.Touches
			}
		}
		out = append(out, best)
		i = j
	}
	if !sameLevels(out, b.merged) {
		b.merged = out
		b.version++
	}
}

// clusterTags reports whether any member is strong and lists the distinct
// member types, the label first.
func clusterTags(ms []Level, label string) (bool, string) {
	strong := false
	tags := []string{label}
	for _, m := range ms {
		strong = strong || strongType(m.Type)
		dup := false
		for _, t := range tags {
			dup = dup || t == m.Type
		}
		if !dup {
			tags = append(tags, m.Type)
		}
	}
	return strong, strings.Join(tags, "+")
}

func sameLevels(a, b []Level) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Price != b[i].Price || a[i].Type != b[i].Type || a[i].Tags != b[i].Tags || a[i].Touches != b[i].Touches {
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
