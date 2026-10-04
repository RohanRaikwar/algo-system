package exitwatch

import (
	"log"
	"time"

	"trading-systemv1/internal/model"
)

// View is one position's assessment as published on pub:analyst:reversal.
type View struct {
	Strategy      string             `json:"strategy"`
	Side          string             `json:"side"`
	FNOToken      string             `json:"fno_token"`
	IndexEntry    int64              `json:"index_entry"`
	TargetLevel   int64              `json:"target_level"`
	StopLevel     int64              `json:"stop_level"`
	FNOEntryPrice int64              `json:"fno_entry_price"`
	P             float64            `json:"p"`
	Decision      Decision           `json:"decision"`
	ExitReason    string             `json:"exit_reason,omitempty"`
	Latched       bool               `json:"latched"`
	Reasons       []string           `json:"reasons"`
	SuggestedStop int64              `json:"suggested_stop,omitempty"`
	IndexLTP      int64              `json:"index_ltp"`
	PremiumLTP    int64              `json:"premium_ltp"`
	Progress      float64            `json:"progress"`
	PeakProgress  float64            `json:"peak_progress"`
	Features      map[string]float64 `json:"features"`
	TS            time.Time          `json:"ts"`
}

// Payload is the pub:analyst:reversal message: every open position.
// Shadow is true while stratengine does not act on EXIT.
type Payload struct {
	Shadow    bool      `json:"shadow"`
	Positions []View    `json:"positions"`
	TS        time.Time `json:"ts"`
}

func newView(a Assessment) View {
	feats := make(map[string]float64, numFeatures)
	for f := Feature(0); f < numFeatures; f++ {
		if a.Snapshot.Present[f] {
			feats[featureNames[f]] = a.Snapshot.Raw[f]
		}
	}
	return View{
		Strategy: a.Strategy, Side: a.Side, FNOToken: a.FNOToken,
		IndexEntry: a.IndexEntry, TargetLevel: a.TargetLevel, StopLevel: a.StopLevel,
		FNOEntryPrice: a.FNOEntryPrice,
		P:             a.P, Decision: a.Decision, ExitReason: a.ExitReason, Latched: a.Latched,
		Reasons: a.Reasons, SuggestedStop: a.SuggestedStop,
		IndexLTP: a.IndexLTP, PremiumLTP: a.PremiumLTP,
		Progress: a.Snapshot.Progress, PeakProgress: a.Snapshot.PeakProgress,
		Features: feats, TS: a.TS,
	}
}

// Sink receives engine output. The service backs it with Redis and SQLite;
// tests back it with slices.
type Sink interface {
	Publish(p Payload)
	RecordDecision(v View)
	RecordFeatures(v View)
}

const publishEvery = 500 * time.Millisecond

type slot struct {
	tr       *Tracker
	last     Assessment
	lastDec  Decision
	lastFeat int64 // unix second of last features_1hz row
}

// Engine owns all trackers. It is driven from one goroutine: positions,
// ticks and timer calls must not run concurrently.
type Engine struct {
	model   Model
	sink    Sink
	shadow  bool
	slots   map[string]*slot // by PositionContext.Key()
	atr     map[string]int64 // by index token
	lastPub time.Time
	dirty   bool

	// Clock: ticks carry exchange time, the timer carries wall time. Between
	// ticks the engine advances the last tick time by wall time elapsed, so
	// one time base drives every feature and the exchange/local skew never
	// leaks into stall.
	evTS   time.Time // canonical time of the last routed tick
	evWall time.Time // wall time it arrived
	nowTS  time.Time // latest time evaluated; never moves back
}

// NewEngine returns an engine publishing to sink.
func NewEngine(m Model, sink Sink, shadow bool) *Engine {
	return &Engine{model: m, sink: sink, shadow: shadow, slots: map[string]*slot{}, atr: map[string]int64{}}
}

// SetModel applies reloaded tuning to every tracker.
func (e *Engine) SetModel(m Model) {
	e.model = m
	for _, s := range e.slots {
		s.tr.SetModel(m)
	}
}

// SetATR sets the 1m ATR (paise) for an index token.
func (e *Engine) SetATR(indexToken string, atr int64) {
	e.atr[indexToken] = atr
	for _, s := range e.slots {
		if s.tr.Position().IndexToken == indexToken {
			s.tr.SetATR(atr)
		}
	}
}

// Open reports the number of tracked positions.
func (e *Engine) Open() int { return len(e.slots) }

// SetPositions reconciles trackers with the open set from stratengine.
// A position keeps its tracker (and EXIT latch) while its key is unchanged.
func (e *Engine) SetPositions(ps []model.PositionContext, now time.Time) {
	seen := make(map[string]bool, len(ps))
	for _, p := range ps {
		k := p.Key()
		seen[k] = true
		if s, ok := e.slots[k]; ok {
			s.tr.pos = p // stop may have moved (breakeven)
			s.last.Position = p
			continue
		}
		tr, err := NewTracker(p, e.model)
		if err != nil {
			log.Printf("[exitwatch] skip position %s: %v", k, err)
			continue
		}
		tr.SetATR(e.atr[p.IndexToken])
		e.slots[k] = &slot{tr: tr}
		log.Printf("[exitwatch] tracking %s entry=%d target=%d stop=%d", k, p.IndexEntry, p.TargetLevel, p.StopLevel)
	}
	for k, s := range e.slots {
		if !seen[k] {
			log.Printf("[exitwatch] closed %s last=%s p=%.2f", k, s.last.Decision, s.last.P)
			delete(e.slots, k)
			e.dirty = true
		}
	}
	e.flush(now, true)
}

// OnTick routes a tick to every tracker that uses its token.
func (e *Engine) OnTick(t model.Tick) {
	ts := t.CanonicalTS()
	e.evTS, e.evWall = ts, time.Now()
	hit := false
	for _, s := range e.slots {
		p := s.tr.Position()
		switch t.Token {
		case p.IndexToken:
			s.tr.OnIndexTick(t.Price, ts)
			hit = true
		case p.FNOToken:
			s.tr.OnOptionTick(t.Price, t.BestBid, t.BestAsk, t.OI, ts)
			hit = true
		}
	}
	if hit {
		e.Evaluate(ts)
	}
}

// Wants reports whether any tracker uses the token; the service uses it to
// skip decoding unrelated ticks.
func (e *Engine) Wants(token string) bool {
	for _, s := range e.slots {
		p := s.tr.Position()
		if token == p.IndexToken || token == p.FNOToken {
			return true
		}
	}
	return false
}

// Tick evaluates on a timer: wall is mapped onto the tick clock so stall
// keeps growing when no ticks arrive.
func (e *Engine) Tick(wall time.Time) {
	if e.evTS.IsZero() {
		e.Evaluate(wall)
		return
	}
	e.Evaluate(e.evTS.Add(wall.Sub(e.evWall)))
}

// Evaluate scores every position at now, records decision changes and
// 1Hz features, and publishes at most every publishEvery unless a decision
// changed.
func (e *Engine) Evaluate(now time.Time) {
	if now.Before(e.nowTS) {
		now = e.nowTS
	}
	e.nowTS = now
	changed := false
	sec := now.Unix()
	for _, s := range e.slots {
		a := s.tr.Evaluate(now)
		s.last = a
		if a.IndexLTP == 0 {
			continue
		}
		if a.Decision != s.lastDec {
			v := newView(a)
			e.sink.RecordDecision(v)
			if a.Decision == DecisionExit {
				log.Printf("[exitwatch] %s EXIT (%s, shadow=%v) p=%.2f idx=%d prem=%d reasons=%v",
					a.Key(), a.ExitReason, e.shadow, a.P, a.IndexLTP, a.PremiumLTP, a.Reasons)
			}
			s.lastDec = a.Decision
			changed = true
		}
		if sec > s.lastFeat {
			s.lastFeat = sec
			e.sink.RecordFeatures(newView(a))
		}
	}
	e.flush(now, changed)
}

func (e *Engine) flush(now time.Time, force bool) {
	if !force && !e.dirty && (len(e.slots) == 0 || now.Sub(e.lastPub) < publishEvery) {
		return
	}
	views := make([]View, 0, len(e.slots))
	for _, s := range e.slots {
		if s.last.IndexLTP != 0 {
			views = append(views, newView(s.last))
		}
	}
	e.sink.Publish(Payload{Shadow: e.shadow, Positions: views, TS: now})
	e.lastPub, e.dirty = now, false
}
