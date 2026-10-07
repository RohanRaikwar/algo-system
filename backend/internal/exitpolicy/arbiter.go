package exitpolicy

import (
	"sync"
	"time"
)

// Arbiter tracks open positions and evaluates their plans once a minute
// (minute evaluation keeps tick noise out of the decision). A position
// decides at most once; any exit of the position closes it here.
//
// Safe for concurrent use: ticks and minute evaluation come from different
// goroutines.
type Arbiter struct {
	mu  sync.Mutex
	pos map[string]*tracked
}

type tracked struct {
	pos      Position
	plan     Plan
	indexLTP int64
	premLTP  int64
	premBest int64
	streak   map[Reason]int
	latched  bool
}

func NewArbiter() *Arbiter {
	return &Arbiter{pos: make(map[string]*tracked)}
}

// Open starts tracking p under plan, replacing any position on the same
// key. An off plan, or one without rules, is not tracked.
func (a *Arbiter) Open(p Position, plan Plan) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pos, p.Key)
	if plan.Mode == ModeOff || plan.Mode == "" || len(plan.Rules) == 0 {
		return
	}
	a.pos[p.Key] = &tracked{pos: p, plan: plan, streak: make(map[Reason]int, len(plan.Rules))}
}

// Close stops tracking key.
func (a *Arbiter) Close(key string) {
	a.mu.Lock()
	delete(a.pos, key)
	a.mu.Unlock()
}

// Len is the number of tracked positions.
func (a *Arbiter) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pos)
}

// OnTick records an index or option price for every position that holds
// token (bare exchange token).
func (a *Arbiter) OnTick(token string, price int64) {
	if price <= 0 {
		return
	}
	a.mu.Lock()
	for _, t := range a.pos {
		switch token {
		case t.pos.IndexToken:
			t.indexLTP = price
		case t.pos.FNOToken:
			t.premLTP = price
			if price > t.premBest {
				t.premBest = price
			}
		}
	}
	a.mu.Unlock()
}

// OnMinute evaluates every unlatched position. armed reports whether the
// owning strategy has a protective stop in place. A rule fires once it has
// held for its ConfirmMinutes consecutive evaluations; the first rule (in
// plan order) to fire decides and latches the position.
func (a *Arbiter) OnMinute(now time.Time, armed func(Position) bool) []Decision {
	a.mu.Lock()
	snaps := make([]*tracked, 0, len(a.pos))
	for _, t := range a.pos {
		if !t.latched {
			snaps = append(snaps, t)
		}
	}
	a.mu.Unlock()

	// armed may take strategy locks: call it outside a.mu.
	arm := make([]bool, len(snaps))
	for i, t := range snaps {
		arm[i] = armed != nil && armed(t.pos)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Decision
	for i, t := range snaps {
		if a.pos[t.pos.Key] != t {
			continue // closed or reopened meanwhile
		}
		s := Snapshot{Now: now, IndexLTP: t.indexLTP, PremLTP: t.premLTP, PremBest: max(t.premBest, t.pos.PremEntry), ProtectArmed: arm[i]}
		for _, r := range t.plan.Rules {
			d, fire := r.Evaluate(t.pos, s)
			if !fire {
				t.streak[r.Name()] = 0
				continue
			}
			t.streak[r.Name()]++
			if t.streak[r.Name()] < max(1, r.ConfirmMinutes()) {
				continue
			}
			d.Shadow = t.plan.Mode != ModeAct
			t.latched = true
			out = append(out, d)
			break
		}
	}
	return out
}
