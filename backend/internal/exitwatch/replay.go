package exitwatch

import (
	"time"

	"trading-systemv1/internal/model"
)

// OurSRLevel is one of our SR strategy's levels as the book labels it.
func OurSRLevel(price int64, source string, touches int) Level {
	return Level{Price: price, Type: ourSRPrefix + source, Touches: touches}
}

// PreRejectReplay runs the pre-target rejection rule over closed 1m bars,
// for the backtest. It keeps its own level book: 1m candles, 5m/1h candles
// aggregated from them on the live TF boundaries (model.TFBucket),
// and our SR levels set by the caller. Live, the same rule runs per tick
// inside the Tracker.
type PreRejectReplay struct {
	m    Model
	book *LevelBook
	agg  map[int]*model.TFCandle // forming 5m/1h bar by TF
	bars []model.TFCandle        // last atrPeriod+1 1m bars

	open       bool
	r          preReject
	entry      int64
	sign       int64
	targetMove int64
	prevClose  model.TFCandle // last closed 1m bar while open
}

// NewPreRejectReplay returns a replay with tuning p. The rule runs whether
// or not p.PreRejectEnabled is set.
func NewPreRejectReplay(p Params) *PreRejectReplay {
	m := p.Compile()
	return &PreRejectReplay{m: m, book: NewLevelBook(&m), agg: map[int]*model.TFCandle{}}
}

// Book is the replay's level book.
func (p *PreRejectReplay) Book() *LevelBook { return p.book }

// OnBar adds a closed 1m bar to the book and the HTF aggregation. Call it
// after Step for the same bar.
func (p *PreRejectReplay) OnBar(c model.TFCandle) {
	p.book.OnCandle(c)
	for _, tf := range htfStreamTFs {
		ts := c.TS.Unix()
		start := time.Unix(model.TFBucket(ts, int64(tf)), 0).In(c.TS.Location())
		f := p.agg[tf]
		if f != nil && !f.TS.Equal(start) {
			p.book.OnHTFCandle(*f)
			f = nil
		}
		if f == nil {
			f = &model.TFCandle{Token: c.Token, Exchange: c.Exchange, TF: tf, TS: start,
				Open: c.Open, High: c.High, Low: c.Low, Close: c.Close}
			p.agg[tf] = f
		}
		if c.High > f.High {
			f.High = c.High
		}
		if c.Low < f.Low {
			f.Low = c.Low
		}
		f.Close = c.Close
	}
	p.bars = append(p.bars, c)
	if len(p.bars) > atrPeriod+1 {
		p.bars = p.bars[len(p.bars)-atrPeriod-1:]
	}
	if p.open {
		p.prevClose = c
	}
}

// Open starts watching a trade entered at the close of entryBar.
func (p *PreRejectReplay) Open(side string, entry, targetMove int64) {
	p.open, p.r, p.prevClose = targetMove > 0, preReject{}, model.TFCandle{}
	p.entry, p.targetMove, p.sign = entry, targetMove, 1
	if side == "PUT" {
		p.sign = -1
	}
}

// Close stops watching the trade.
func (p *PreRejectReplay) Close() { p.open = false }

// Step walks bar c (not yet added with OnBar) as four prices: open, the
// nearer extreme, the farther extreme, close. It returns true with the
// detail text when the rule exits at the bar's close. A trade that reaches
// target inside the bar is no longer watched (the runner owns it live).
func (p *PreRejectReplay) Step(c model.TFCandle) (bool, string) {
	if !p.open {
		return false, ""
	}
	atr, _ := computeATR(p.bars)
	tol, rej := levelTolFor(&p.m, atr), rejectPtsFor(&p.m, atr)
	path := [4]int64{c.Open, c.High, c.Low, c.Close}
	if c.Close >= c.Open {
		path[1], path[2] = c.Low, c.High
	}
	offs := [4]time.Duration{0, 15 * time.Second, 30 * time.Second, 59 * time.Second}
	levels := p.book.Levels()
	for i, price := range path {
		mv := (price - p.entry) * p.sign
		if mv >= p.targetMove {
			p.open = false
			return false, ""
		}
		in := preRejectIn{
			levels: levels, price: price, move: mv, entry: p.entry, sign: p.sign,
			targetMove: p.targetMove, tol: tol, rej: rej, breakN: p.m.breakN,
			close: p.prevClose.Close, closeTS: p.prevClose.TS, now: c.TS.Add(offs[i]), m: &p.m,
			adx: p.book.ADX(),
		}
		if i == 3 { // the bar itself has closed
			in.close, in.closeTS = c.Close, c.TS
		}
		if exit, text := p.r.step(in); exit {
			p.open = false
			return true, text
		}
	}
	return false, ""
}
