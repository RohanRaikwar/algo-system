package backtest

import (
	"trading-systemv1/internal/exitwatch"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// PreRejectConfig runs exitwatch's pre-target rejection rule (SR_REJECT_PRE)
// over the replay and closes the trade when it fires, as an auto-exit
// strategy would live. Params.PreRejectSources picks the level sources;
// the book always holds 5m/1h swings and our SR levels, as live.
type PreRejectConfig struct {
	Params exitwatch.Params
}

// btPreReject drives exitwatch.PreRejectReplay; nil when off.
type btPreReject struct {
	r    *exitwatch.PreRejectReplay
	key  string
	prev *model.TFCandle // last bar not yet added to the book
}

func (e *Engine) newBTPreReject() *btPreReject {
	c := e.cfg.PreReject
	if c == nil {
		return nil
	}
	return &btPreReject{r: exitwatch.NewPreRejectReplay(c.Params), key: e.cfg.Exchange + ":" + e.cfg.Token}
}

// srViewer is the strategy's dashboard view (live: pub:sr).
type srViewer interface {
	View(key string) (strategy.SRView, bool)
}

// advance adds the previous bar to the book; call once per market bar
// before step, then set c as the pending bar with pend.
func (b *btPreReject) advance() {
	if b == nil || b.prev == nil {
		return
	}
	b.r.OnBar(*b.prev)
	b.prev = nil
}

func (b *btPreReject) pend(c model.TFCandle) {
	if b != nil {
		b.prev = &c
	}
}

// open starts watching a trade entered at c's close.
func (b *btPreReject) open(t *Trade, c model.TFCandle, targetMove int64) {
	if b == nil || t == nil {
		return
	}
	b.r.Open(string(t.Side), c.Close, targetMove)
}

// step evaluates bar c for the open trade; true with the exit text when the
// rule fires.
func (b *btPreReject) step(strat backtestStrategy, c model.TFCandle) (bool, string) {
	if b == nil {
		return false, ""
	}
	if v, ok := strat.(srViewer); ok {
		if view, ok := v.View(b.key); ok {
			ls := make([]exitwatch.Level, 0, len(view.Levels))
			for _, l := range view.Levels {
				ls = append(ls, exitwatch.OurSRLevel(l.Price, l.Source, l.Touches))
			}
			b.r.Book().SetOurSR(ls)
			b.r.Book().SetTrend(view.ADX)
		}
	}
	return b.r.Step(c)
}
