package exitwatch

import (
	"fmt"
	"time"
)

// Phase is where a position is in its life relative to the target.
type Phase string

const (
	PhaseNormal Phase = "NORMAL" // before target
	PhaseRunner Phase = "RUNNER" // target hit, holding to the next S/R
	PhaseGhost  Phase = "GHOST"  // strategy closed it; exitwatch follows to measure the runner
)

// Signal actions exitwatch puts on pub:signal. They never place orders.
const (
	ActionWatchHold    = "WATCH_HOLD"
	ActionWatchTighten = "WATCH_TIGHTEN"
	ActionWatchExit    = "WATCH_EXIT"
)

// Exit reasons added by the runner.
const (
	ReasonTargetTake = "TARGET_TAKE"
	ReasonLock       = "LOCK"
	ReasonSRReject   = "SR_REJECT"
	ReasonRunTrail   = "RUN_TRAIL"
)

// Event is one edge-triggered exitwatch signal for the LOG tab.
type Event struct {
	Action string // WATCH_*
	Reason string // short code, e.g. TARGET_RUN, SR_BREAK
	Text   string // human detail
}

// runner holds the target-runner state of a Tracker.
type runner struct {
	phase       Phase
	decided     bool // target-hit decision made (hold or take)
	start       time.Time
	lockMove    int64 // favourable move that must hold, paise
	next        Level
	hasNext     bool
	runPeak     int64 // best move since runner start
	pbLow       int64 // lowest move since runPeak (pullback)
	touched     bool  // came within tol of next
	beyondSince int64 // unix ns price first held beyond next; 0 = not beyond
	bookVer     int
	micro       []Level // micro supports confirmed during the run
}

func pts(p int64) string { return fmt.Sprintf("%.2f", float64(p)/100) }

// aheadWord names a level ahead of price for this side.
func (t *Tracker) aheadWord() string {
	if t.sign > 0 {
		return "resistance"
	}
	return "support"
}

// behindWord names a level behind price for this side.
func (t *Tracker) behindWord() string {
	if t.sign > 0 {
		return "support"
	}
	return "resistance"
}

func (t *Tracker) levelTol() int64 {
	tol := int64(float64(t.atr) * t.m.LevelTolATR)
	if tol < t.m.LevelTolMinPaise {
		tol = t.m.LevelTolMinPaise
	}
	return tol
}

func (t *Tracker) rejectPts() int64 {
	r := int64(float64(t.atr) * t.m.RejectATR)
	if r < t.m.RejectMinPaise {
		r = t.m.RejectMinPaise
	}
	return r
}

func (t *Tracker) levels() []Level {
	if t.book == nil {
		return nil
	}
	return t.book.Levels()
}

// levelPrice converts a favourable move back to an index price.
func (t *Tracker) levelPrice(move int64) int64 { return t.pos.IndexEntry + t.sign*move }

func (t *Tracker) moveOf(price int64) int64 { return (price - t.pos.IndexEntry) * t.sign }

func (t *Tracker) emit(action, reason, text string) {
	t.events = append(t.events, Event{Action: action, Reason: reason, Text: text})
}

// latchExit fixes the EXIT decision with a reason and detail text.
func (t *Tracker) latchExit(reason, text string) {
	t.dec.latched, t.dec.reason = true, reason
	t.exitText = text
}

// runnerStep runs the target-runner rules. It is called before the score
// decider so a runner exit latches with its own reason.
func (t *Tracker) runnerStep(s *Snapshot, now time.Time) {
	if t.dec.latched {
		return
	}
	r := &t.run
	mv, price := t.lastMove, t.idxLTP
	tol := t.levelTol()
	ls := t.levels()

	if r.phase == PhaseNormal || r.phase == "" {
		r.phase = PhaseNormal
		if r.decided || mv < t.targetMove {
			return
		}
		r.decided = true
		next, ok := nextLevel(ls, price, t.sign, tol)
		roomOK := !ok || float64(t.moveOf(next.Price)-mv) >= t.m.RunnerMinRoomPct*float64(t.targetMove)
		strong := (!s.Present[FVelocity] || s.Z[FVelocity] < 0.5) &&
			(!s.Present[FImbalance] || s.Raw[FImbalance] >= 0) &&
			(!s.Present[FWick] || s.Z[FWick] < 0.5)
		if !strong || !roomOK {
			why := "momentum weak"
			if strong {
				why = fmt.Sprintf("next %s %s (%s) too close", t.aheadWord(), pts(next.Price), next.Type)
			}
			t.latchExit(ReasonTargetTake, "target hit, "+why+": take target")
			return
		}
		r.phase, r.start = PhaseRunner, now
		r.lockMove = int64(t.m.RunnerLockPct * float64(t.targetMove))
		r.next, r.hasNext = next, ok
		r.runPeak, r.pbLow = mv, mv
		if t.book != nil {
			r.bookVer = t.book.Version()
		}
		nextTxt := "no level ahead, trail"
		if ok {
			nextTxt = fmt.Sprintf("hold to next %s %s (%s)", t.aheadWord(), pts(next.Price), next.Type)
		}
		t.emit(ActionWatchHold, "TARGET_RUN", fmt.Sprintf("target hit, momentum strong: %s, lock=%s", nextTxt, pts(t.levelPrice(r.lockMove))))
		return
	}

	// ── RUNNER / GHOST ──
	rej := t.rejectPts()

	// Peak and micro support: a pullback of at least rej followed by a new
	// peak confirms the pullback low as fresh support.
	if mv > r.runPeak {
		if r.runPeak-r.pbLow >= rej && r.pbLow-tol > r.lockMove {
			r.lockMove = r.pbLow - tol
			lvl := Level{Price: t.levelPrice(r.pbLow), Type: LevelMicroLow, Since: now}
			if t.sign < 0 {
				lvl.Type = LevelMicroHigh
			}
			r.micro = append(r.micro, lvl)
			t.emit(ActionWatchHold, "NEW_LEVEL", fmt.Sprintf("new %s %s (%s) formed, lock raised to %s",
				t.behindWord(), pts(lvl.Price), lvl.Type, pts(t.levelPrice(r.lockMove))))
		}
		r.runPeak, r.pbLow = mv, mv
	} else if mv < r.pbLow {
		r.pbLow = mv
	}

	// New levels from the book: a closer level ahead becomes the next
	// target; a level formed during the run behind price raises the lock.
	if t.book != nil {
		if v := t.book.Version(); v != r.bookVer {
			r.bookVer = v
			t.onNewLevels(ls, price, tol)
		}
	}

	// Touch and ladder.
	if r.hasNext {
		dist := t.moveOf(r.next.Price) - mv // > 0: level still ahead
		if dist <= tol {
			r.touched = true
		}
		if dist < -tol {
			nowNs := now.UnixNano()
			if r.beyondSince == 0 {
				r.beyondSince = nowNs
			}
			if nowNs-r.beyondSince >= t.m.breakN {
				broken := r.next
				if l := t.moveOf(broken.Price) - tol; l > r.lockMove {
					r.lockMove = l
				}
				r.next, r.hasNext = nextLevel(ls, price, t.sign, tol)
				r.touched, r.beyondSince = false, 0
				nextTxt := "no level ahead, trail"
				if r.hasNext {
					nextTxt = fmt.Sprintf("next %s %s (%s)", t.aheadWord(), pts(r.next.Price), r.next.Type)
				}
				t.emit(ActionWatchHold, "SR_BREAK", fmt.Sprintf("broke %s %s (%s), %s, lock=%s",
					t.aheadWord(), pts(broken.Price), broken.Type, nextTxt, pts(t.levelPrice(r.lockMove))))
			}
		} else {
			r.beyondSince = 0
		}
	}

	// Runner exits.
	ext := r.runPeak - t.targetMove
	switch {
	case mv <= r.lockMove:
		t.latchExit(ReasonLock, fmt.Sprintf("gave back to lock %s", pts(t.levelPrice(r.lockMove))))
	case r.touched && r.runPeak-mv >= rej:
		t.latchExit(ReasonSRReject, fmt.Sprintf("rejected at %s %s (%s), pulled back %s pts",
			t.aheadWord(), pts(r.next.Price), r.next.Type, pts(r.runPeak-mv)))
	case ext >= rej && float64(r.runPeak-mv) >= t.m.RunnerGiveback*float64(ext):
		t.latchExit(ReasonRunTrail, fmt.Sprintf("gave back %s of %s pts run past target", pts(r.runPeak-mv), pts(ext)))
	}
}

func (t *Tracker) onNewLevels(ls []Level, price, tol int64) {
	r := &t.run
	mv := t.lastMove
	if cand, ok := nextLevel(ls, price, t.sign, tol); ok &&
		(!r.hasNext || t.moveOf(cand.Price) < t.moveOf(r.next.Price)) {
		r.next, r.hasNext, r.touched, r.beyondSince = cand, true, false, 0
		t.emit(ActionWatchHold, "NEW_LEVEL", fmt.Sprintf("new %s %s (%s) ahead", t.aheadWord(), pts(cand.Price), cand.Type))
	}
	var best Level
	bestMove := r.lockMove + tol
	found := false
	for _, l := range ls {
		if l.Type == LevelDayHigh || l.Type == LevelDayLow || l.Since.Before(r.start) {
			continue
		}
		lm := t.moveOf(l.Price)
		if lm > bestMove && lm < mv-tol {
			best, bestMove, found = l, lm, true
		}
	}
	if found {
		r.lockMove = bestMove - tol
		t.emit(ActionWatchHold, "NEW_LEVEL", fmt.Sprintf("new %s %s (%s) formed, lock raised to %s",
			t.behindWord(), pts(best.Price), best.Type, pts(t.levelPrice(r.lockMove))))
	}
}

// nearLevels returns up to 3 levels ahead and 2 behind price, nearest first.
func (t *Tracker) nearLevels() []Level {
	ls := t.levels()
	out := make([]Level, 0, 5)
	ahead, behind := 0, 0
	if t.sign > 0 {
		for _, l := range ls {
			if l.Price > t.idxLTP && ahead < 3 {
				out = append(out, l)
				ahead++
			}
		}
		for i := len(ls) - 1; i >= 0 && behind < 2; i-- {
			if ls[i].Price <= t.idxLTP {
				out = append(out, ls[i])
				behind++
			}
		}
		return out
	}
	for i := len(ls) - 1; i >= 0; i-- {
		if ls[i].Price < t.idxLTP && ahead < 3 {
			out = append(out, ls[i])
			ahead++
		}
	}
	for _, l := range ls {
		if l.Price >= t.idxLTP && behind < 2 {
			out = append(out, l)
			behind++
		}
	}
	return out
}
