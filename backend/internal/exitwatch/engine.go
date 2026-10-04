package exitwatch

import (
	"fmt"
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
	ExitText      string             `json:"exit_text,omitempty"`
	Latched       bool               `json:"latched"`
	Reasons       []string           `json:"reasons"`
	SuggestedStop int64              `json:"suggested_stop,omitempty"`
	IndexLTP      int64              `json:"index_ltp"`
	PremiumLTP    int64              `json:"premium_ltp"`
	Progress      float64            `json:"progress"`
	PeakProgress  float64            `json:"peak_progress"`
	Phase         Phase              `json:"phase"`
	NextLevel     *Level             `json:"next_level,omitempty"`
	LockLevel     int64              `json:"lock_level,omitempty"`
	Levels        []Level            `json:"levels"`
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
	levels := a.Levels
	if levels == nil {
		levels = []Level{}
	}
	return View{
		Strategy: a.Strategy, Side: a.Side, FNOToken: a.FNOToken,
		IndexEntry: a.IndexEntry, TargetLevel: a.TargetLevel, StopLevel: a.StopLevel,
		FNOEntryPrice: a.FNOEntryPrice,
		P:             a.P, Decision: a.Decision, ExitReason: a.ExitReason, ExitText: a.ExitText, Latched: a.Latched,
		Reasons: a.Reasons, SuggestedStop: a.SuggestedStop,
		IndexLTP: a.IndexLTP, PremiumLTP: a.PremiumLTP,
		Progress: a.Snapshot.Progress, PeakProgress: a.Snapshot.PeakProgress,
		Phase: a.Phase, NextLevel: a.NextLevel, LockLevel: a.LockLevel, Levels: levels,
		Features: feats, TS: a.TS,
	}
}

// SignalEvent is an exitwatch signal in the pub:signal payload shape that
// stratengine uses (stratengine/service.go signalLoop), so the Signals LOG
// tab shows it next to strategy BUY/EXIT. Actions are WATCH_*; they never
// place orders.
type SignalEvent struct {
	StrategyName    string `json:"strategy_name"`
	Action          string `json:"action"`
	Side            string `json:"side"`
	Token           string `json:"token"`
	Exchange        string `json:"exchange"`
	Qty             int64  `json:"qty"`
	Price           int64  `json:"price"` // index LTP, paise
	EntryFNOPrice   int64  `json:"entry_fno_price"`
	CurrentFNOPrice int64  `json:"current_fno_price"`
	Reason          string `json:"reason"`
	FNOToken        string `json:"fno_token"`
	OrderMode       string `json:"order_mode"` // SHADOW
	TS              string `json:"ts"`

	at time.Time
}

// Sink receives engine output. The service backs it with Redis and SQLite;
// tests back it with slices.
type Sink interface {
	Publish(p Payload)
	RecordDecision(v View)
	RecordFeatures(v View)
	Signal(ev SignalEvent)
}

const (
	publishEvery  = 500 * time.Millisecond
	tightenRepeat = 60 * time.Second
)

type slot struct {
	tr          *Tracker
	last        Assessment
	lastDec     Decision
	lastFeat    int64 // unix second of last features_1hz row
	lastTighten time.Time

	ghostSince time.Time // zero unless the strategy closed it while in runner
	closeMove  int64     // favourable move when the strategy closed it
}

// Engine owns all trackers and the per-index level books. It is driven from
// one goroutine: positions, ticks, levels and timer calls must not run
// concurrently.
type Engine struct {
	model   Model
	sink    Sink
	shadow  bool
	slots   map[string]*slot      // by PositionContext.Key()
	atr     map[string]int64      // by index token
	books   map[string]*LevelBook // by index token
	exch    map[string]string     // index token → exchange
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
	return &Engine{
		model: m, sink: sink, shadow: shadow,
		slots: map[string]*slot{}, atr: map[string]int64{},
		books: map[string]*LevelBook{}, exch: map[string]string{},
	}
}

// SetModel applies reloaded tuning to every tracker and book.
func (e *Engine) SetModel(m Model) {
	e.model = m
	for _, s := range e.slots {
		s.tr.SetModel(m)
	}
	for _, b := range e.books {
		b.configure(&e.model)
	}
}

// SetExchange records the exchange of an index token for signal payloads.
func (e *Engine) SetExchange(indexToken, exchange string) { e.exch[indexToken] = exchange }

// Book returns (creating) the level book of an index token.
func (e *Engine) Book(indexToken string) *LevelBook {
	b, ok := e.books[indexToken]
	if !ok {
		b = NewLevelBook(&e.model)
		e.books[indexToken] = b
	}
	return b
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

// OnCandle feeds a closed 1m index candle to the level book and refreshes ATR.
func (e *Engine) OnCandle(c model.TFCandle) {
	b := e.Book(c.Token)
	b.OnCandle(c)
	bars := b.Bars()
	if n := len(bars); n > atrPeriod {
		bars = bars[n-atrPeriod-1:]
	}
	if atr, ok := computeATR(bars); ok {
		e.SetATR(c.Token, atr)
	}
}

// SetAnalystLevels replaces the analyst's levels for an index.
func (e *Engine) SetAnalystLevels(indexToken string, ls []Level) { e.Book(indexToken).SetAnalyst(ls) }

// SetOurSRLevels replaces our SR strategy's levels for an index.
func (e *Engine) SetOurSRLevels(indexToken string, ls []Level) { e.Book(indexToken).SetOurSR(ls) }

// Open reports the number of tracked positions, ghosts included.
func (e *Engine) Open() int { return len(e.slots) }

// SetPositions reconciles trackers with the open set from stratengine.
// A position keeps its tracker (and EXIT latch) while its key is unchanged.
// A position closed while holding past target becomes a ghost: exitwatch
// keeps following it to measure what the runner would have made.
func (e *Engine) SetPositions(ps []model.PositionContext, now time.Time) {
	seen := make(map[string]bool, len(ps))
	for _, p := range ps {
		k := p.Key()
		seen[k] = true
		if s, ok := e.slots[k]; ok && s.ghostSince.IsZero() {
			s.tr.pos = p // stop may have moved (breakeven)
			s.last.Position = p
			continue
		} else if ok {
			log.Printf("[exitwatch] ghost %s replaced by a new position", k)
		}
		tr, err := NewTracker(p, e.model)
		if err != nil {
			log.Printf("[exitwatch] skip position %s: %v", k, err)
			continue
		}
		tr.SetATR(e.atr[p.IndexToken])
		tr.SetBook(e.Book(p.IndexToken))
		e.slots[k] = &slot{tr: tr}
		log.Printf("[exitwatch] tracking %s entry=%d target=%d stop=%d", k, p.IndexEntry, p.TargetLevel, p.StopLevel)
	}
	for k, s := range e.slots {
		if seen[k] || !s.ghostSince.IsZero() {
			continue
		}
		if s.tr.InRunner() && !s.last.Latched {
			s.ghostSince, s.closeMove = now, s.tr.LastMove()
			s.tr.ghost = true
			log.Printf("[exitwatch] %s closed by strategy in runner at move %s; following as ghost", k, pts(s.closeMove))
			e.signal(s, Event{Action: ActionWatchHold, Reason: "GHOST",
				Text: fmt.Sprintf("strategy closed at %s; following runner to measure the hold", pts(s.tr.idxLTP))}, now)
			continue
		}
		log.Printf("[exitwatch] closed %s last=%s p=%.2f", k, s.last.Decision, s.last.P)
		delete(e.slots, k)
		e.dirty = true
	}
	e.flush(now, true)
}

// OnTick routes a tick to the level books and every tracker that uses it.
func (e *Engine) OnTick(t model.Tick) {
	ts := t.CanonicalTS()
	e.evTS, e.evWall = ts, time.Now()
	if b, ok := e.books[t.Token]; ok {
		b.OnTick(t.Price, ts)
	}
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

// Tokens lists every token the trackers need, ghosts included.
func (e *Engine) Tokens() map[string]bool {
	w := make(map[string]bool, 2*len(e.slots))
	for _, s := range e.slots {
		p := s.tr.Position()
		w[p.IndexToken] = true
		if p.FNOToken != "" {
			w[p.FNOToken] = true
		}
	}
	return w
}

// Tick evaluates on a timer: wall is mapped onto the tick clock so stall
// keeps growing when no ticks arrive.
func (e *Engine) Tick(wall time.Time) { e.Evaluate(e.ClockAt(wall)) }

// ClockAt maps a wall time onto the engine's tick clock.
func (e *Engine) ClockAt(wall time.Time) time.Time {
	if e.evTS.IsZero() {
		return wall
	}
	return e.evTS.Add(wall.Sub(e.evWall))
}

// Evaluate scores every position at now, records decision changes and
// 1Hz features, emits edge-triggered signals, and publishes at most every
// publishEvery unless a decision changed.
func (e *Engine) Evaluate(now time.Time) {
	if now.Before(e.nowTS) {
		now = e.nowTS
	}
	e.nowTS = now
	changed := false
	sec := now.Unix()
	for k, s := range e.slots {
		a := s.tr.Evaluate(now)
		s.last = a
		for _, ev := range s.tr.DrainEvents() {
			e.signal(s, ev, now)
		}
		if a.IndexLTP == 0 {
			continue
		}
		if a.Decision != s.lastDec {
			v := newView(a)
			e.sink.RecordDecision(v)
			switch a.Decision {
			case DecisionExit:
				log.Printf("[exitwatch] %s EXIT (%s, shadow=%v) p=%.2f idx=%d prem=%d %s reasons=%v",
					a.Key(), a.ExitReason, e.shadow, a.P, a.IndexLTP, a.PremiumLTP, a.ExitText, a.Reasons)
				e.signal(s, Event{Action: ActionWatchExit, Reason: a.ExitReason, Text: e.exitText(s, a)}, now)
			case DecisionTighten:
				if s.lastTighten.IsZero() || now.Sub(s.lastTighten) >= tightenRepeat {
					s.lastTighten = now
					txt := "reversal risk rising"
					if a.SuggestedStop != 0 {
						txt += ", suggested stop " + pts(a.SuggestedStop)
					}
					e.signal(s, Event{Action: ActionWatchTighten, Reason: "TIGHTEN", Text: txt}, now)
				}
			}
			s.lastDec = a.Decision
			changed = true
		}
		if sec > s.lastFeat {
			s.lastFeat = sec
			e.sink.RecordFeatures(newView(a))
		}
		if !s.ghostSince.IsZero() {
			if a.Latched {
				delete(e.slots, k)
				changed = true
			} else if e.ghostExpired(s, now) {
				e.signal(s, Event{Action: ActionWatchExit, Reason: "GHOST_END", Text: "ghost window over; " + e.extraText(s)}, now)
				delete(e.slots, k)
				changed = true
			}
		}
	}
	e.flush(now, changed)
}

func (e *Engine) ghostExpired(s *slot, now time.Time) bool {
	if e.model.GhostMaxMin > 0 && now.Sub(s.ghostSince) >= time.Duration(e.model.GhostMaxMin)*time.Minute {
		return true
	}
	ist := now.In(bookIST)
	return e.model.GhostEndMin > 0 && ist.Hour()*60+ist.Minute() >= e.model.GhostEndMin
}

// extraText reports what holding the ghost made versus the strategy's exit.
func (e *Engine) extraText(s *slot) string {
	d := s.tr.LastMove() - s.closeMove
	sgn := "+"
	if d < 0 {
		sgn, d = "-", -d
	}
	return fmt.Sprintf("extra=%s%s pts vs strategy exit", sgn, pts(d))
}

func (e *Engine) exitText(s *slot, a Assessment) string {
	txt := a.ExitText
	if txt == "" {
		txt = fmt.Sprintf("p=%.2f", a.P)
		if len(a.Reasons) > 0 {
			txt += fmt.Sprintf(" %v", a.Reasons)
		}
	}
	if !s.ghostSince.IsZero() {
		txt += "; " + e.extraText(s)
	}
	return txt
}

func (e *Engine) signal(s *slot, ev Event, now time.Time) {
	p := s.tr.Position()
	exch := e.exch[p.IndexToken]
	if exch == "" {
		exch = "NSE"
	}
	mode := "SHADOW"
	if !e.shadow {
		mode = "AUTO"
	}
	phase := ""
	if ph := s.tr.Phase(); ph != PhaseNormal {
		phase = " " + string(ph)
	}
	e.sink.Signal(SignalEvent{
		StrategyName: p.Strategy, Action: ev.Action, Side: p.Side,
		Token: p.IndexToken, Exchange: exch, Price: s.tr.idxLTP,
		EntryFNOPrice: p.FNOEntryPrice, CurrentFNOPrice: s.tr.prem,
		Reason:   fmt.Sprintf("EXITWATCH %s%s %s: %s", mode, phase, ev.Reason, ev.Text),
		FNOToken: p.FNOToken, OrderMode: mode,
		TS: now.UTC().Format(time.RFC3339Nano), at: now,
	})
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
