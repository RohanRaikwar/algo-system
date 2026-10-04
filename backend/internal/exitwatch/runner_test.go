package exitwatch

import (
	"strings"
	"testing"
	"time"
)

// Target is +40pt. ATR 15pt gives tol 3pt and rejection 5pt.
const pt = 100 // paise per point

type runResult struct {
	as     []Assessment
	events []Event
	tr     *Tracker
}

// runPath drives a tracker every 250ms; path returns the favourable move in
// points ×100 (paise). hook, when set, runs before each tick.
func runPath(t *testing.T, pos Position, book *LevelBook, dur time.Duration, path pathFn, hook func(el time.Duration, tr *Tracker)) runResult {
	t.Helper()
	tr, err := NewTracker(pos, DefaultParams().Compile())
	if err != nil {
		t.Fatal(err)
	}
	tr.SetATR(15 * pt)
	tr.SetBook(book)
	sign := int64(1)
	if pos.Side == "PUT" {
		sign = -1
	}
	var res runResult
	res.tr = tr
	for el := time.Duration(0); el <= dur; el += 250 * time.Millisecond {
		if hook != nil {
			hook(el, tr)
		}
		ts := t0.Add(el)
		mv := path(el)
		tr.OnIndexTick(pos.IndexEntry+sign*mv, ts)
		tr.OnOptionTick(pos.FNOEntryPrice+mv/2, 0, 0, 0, ts)
		a := tr.Evaluate(ts)
		res.as = append(res.as, a)
		res.events = append(res.events, tr.DrainEvents()...)
		if a.Latched {
			break
		}
	}
	return res
}

func bookWith(prices ...int64) *LevelBook {
	b := newBook()
	ls := make([]Level, len(prices))
	for i, p := range prices {
		ls[i] = Level{Price: p, Type: "OUR_SR:swing", Since: t0.Add(-time.Hour)}
	}
	b.SetOurSR(ls)
	return b
}

// lin moves linearly from a to b points between s0 and s1 seconds.
func lin(s, s0, s1 float64, a, b float64) int64 {
	if s <= s0 {
		return int64(a * pt)
	}
	if s >= s1 {
		return int64(b * pt)
	}
	return int64((a + (b-a)*(s-s0)/(s1-s0)) * pt)
}

func last(r runResult) Assessment { return r.as[len(r.as)-1] }

func hasEvent(r runResult, reason string) (Event, bool) {
	for _, e := range r.events {
		if e.Reason == reason {
			return e, true
		}
	}
	return Event{}, false
}

func countEvents(r runResult, reason string) int {
	n := 0
	for _, e := range r.events {
		if e.Reason == reason {
			n++
		}
	}
	return n
}

// strong run through target to R2 (+70), touch, pull back 8pt.
func runToR2Reject(el time.Duration) int64 {
	s := el.Seconds()
	switch {
	case s <= 140:
		return lin(s, 0, 140, 0, 70)
	default:
		return lin(s, 140, 150, 70, 62)
	}
}

func TestRunnerHoldsPastTargetAndExitsAtNextSR(t *testing.T) {
	for _, pos := range []Position{callPos(), putPos()} {
		t.Run(pos.Side, func(t *testing.T) {
			sign := int64(1)
			if pos.Side == "PUT" {
				sign = -1
			}
			book := bookWith(entryIdx+sign*70*pt, entryIdx+sign*100*pt)
			r := runPath(t, pos, book, 160*time.Second, runToR2Reject, nil)
			hold, ok := hasEvent(r, "TARGET_RUN")
			if !ok {
				t.Fatalf("no TARGET_RUN hold; events=%+v last=%+v", r.events, last(r))
			}
			if !strings.Contains(hold.Text, pts(entryIdx+sign*70*pt)) {
				t.Fatalf("hold text should name next level: %q", hold.Text)
			}
			a := last(r)
			if a.Decision != DecisionExit || a.ExitReason != ReasonSRReject {
				t.Fatalf("want EXIT SR_REJECT, got %s %s (%s)", a.Decision, a.ExitReason, a.ExitText)
			}
			if mv := (a.IndexLTP - entryIdx) * sign; mv <= 40*pt {
				t.Fatalf("exit at move %d: should be above target", mv)
			}
			if countEvents(r, "TARGET_RUN") != 1 {
				t.Fatal("TARGET_RUN must fire once")
			}
		})
	}
}

func TestRunnerLaddersThroughBrokenLevel(t *testing.T) {
	book := bookWith(entryIdx+70*pt, entryIdx+100*pt)
	path := func(el time.Duration) int64 {
		s := el.Seconds()
		if s <= 150 {
			return lin(s, 0, 150, 0, 75)
		}
		return lin(s, 150, 200, 75, 80)
	}
	r := runPath(t, callPos(), book, 200*time.Second, path, nil)
	ev, ok := hasEvent(r, "SR_BREAK")
	if !ok {
		t.Fatalf("no SR_BREAK; events=%+v last=%s %s", r.events, last(r).Decision, last(r).ExitText)
	}
	a := last(r)
	if a.Latched {
		t.Fatalf("should still hold, got EXIT %s (%s)", a.ExitReason, a.ExitText)
	}
	if a.NextLevel == nil || a.NextLevel.Price != entryIdx+100*pt {
		t.Fatalf("next level should be R3, got %+v (%s)", a.NextLevel, ev.Text)
	}
	if a.LockLevel != entryIdx+67*pt {
		t.Fatalf("lock should move to R2 - tol = %d, got %d", entryIdx+67*pt, a.LockLevel)
	}
}

func TestRunnerLockOnFastGiveback(t *testing.T) {
	book := bookWith(entryIdx+70*pt, entryIdx+100*pt)
	path := func(el time.Duration) int64 {
		s := el.Seconds()
		if s <= 84 {
			return lin(s, 0, 84, 0, 42)
		}
		return 27 * pt // gap down through the 28pt lock
	}
	r := runPath(t, callPos(), book, 90*time.Second, path, nil)
	a := last(r)
	if a.ExitReason != ReasonLock {
		t.Fatalf("want LOCK, got %s %s (%s)", a.Decision, a.ExitReason, a.ExitText)
	}
}

func TestTargetTakeWhenNextLevelTooClose(t *testing.T) {
	book := bookWith(entryIdx + 44*pt) // 4pt past target < 30% of 40pt
	r := runPath(t, callPos(), book, 100*time.Second, func(el time.Duration) int64 { return lin(el.Seconds(), 0, 90, 0, 45) }, nil)
	a := last(r)
	if a.ExitReason != ReasonTargetTake || !strings.Contains(a.ExitText, "too close") {
		t.Fatalf("want TARGET_TAKE too close, got %s (%s)", a.ExitReason, a.ExitText)
	}
	if _, ok := hasEvent(r, "TARGET_RUN"); ok {
		t.Fatal("must not start runner")
	}
}

func TestTargetTakeWhenMomentumWeak(t *testing.T) {
	path := func(el time.Duration) int64 {
		s := el.Seconds()
		switch {
		case s <= 60:
			return lin(s, 0, 60, 0, 39)
		case s <= 85:
			return lin(s, 60, 85, 39, 35)
		default:
			return 40 * pt // one jump onto target after a fall
		}
	}
	r := runPath(t, callPos(), nil, 90*time.Second, path, nil)
	a := last(r)
	if a.ExitReason != ReasonTargetTake || !strings.Contains(a.ExitText, "momentum weak") {
		t.Fatalf("want TARGET_TAKE momentum weak, got %s (%s)", a.ExitReason, a.ExitText)
	}
}

func TestRunnerNoLevelsTrails(t *testing.T) {
	path := func(el time.Duration) int64 {
		s := el.Seconds()
		if s <= 100 {
			return lin(s, 0, 100, 0, 50)
		}
		return 44 * pt // give back 6pt of a 10pt extension
	}
	r := runPath(t, callPos(), nil, 110*time.Second, path, nil)
	ev, ok := hasEvent(r, "TARGET_RUN")
	if !ok || !strings.Contains(ev.Text, "no level ahead") {
		t.Fatalf("want runner with no level, got %+v", r.events)
	}
	if a := last(r); a.ExitReason != ReasonRunTrail {
		t.Fatalf("want RUN_TRAIL, got %s (%s)", a.ExitReason, a.ExitText)
	}
}

func TestRunnerMicroSupportRaisesLock(t *testing.T) {
	book := bookWith(entryIdx + 100*pt)
	path := func(el time.Duration) int64 {
		s := el.Seconds()
		switch {
		case s <= 110:
			return lin(s, 0, 110, 0, 55)
		case s <= 112:
			return lin(s, 110, 112, 55, 49) // 6pt pullback
		default:
			return lin(s, 112, 130, 49, 58) // new high confirms 49 as support
		}
	}
	r := runPath(t, callPos(), book, 130*time.Second, path, nil)
	ev, ok := hasEvent(r, "NEW_LEVEL")
	if !ok || !strings.Contains(ev.Text, LevelMicroLow) {
		t.Fatalf("want MICRO_LOW new support, got %+v last=%s (%s)", r.events, last(r).ExitReason, last(r).ExitText)
	}
	if a := last(r); a.Latched || a.LockLevel != entryIdx+46*pt {
		t.Fatalf("lock should be 49-3=46pt and still holding, got lock=%d latched=%v %s", a.LockLevel-entryIdx, a.Latched, a.ExitText)
	}
}

func TestRunnerNewOurSRLevelAheadAndBehind(t *testing.T) {
	book := bookWith(entryIdx + 100*pt)
	path := func(el time.Duration) int64 { return lin(el.Seconds(), 0, 120, 0, 60) }
	added := false
	r := runPath(t, callPos(), book, 120*time.Second, path, func(el time.Duration, tr *Tracker) {
		if el >= 100*time.Second && !added { // price ~50pt, runner on
			added = true
			book.SetOurSR([]Level{
				{Price: entryIdx + 100*pt, Type: "OUR_SR:swing", Since: t0.Add(-time.Hour)},
				{Price: entryIdx + 65*pt, Type: "OUR_SR:swing", Since: t0.Add(el)}, // new R ahead
				{Price: entryIdx + 45*pt, Type: "OUR_SR:vwap", Since: t0.Add(el)},  // new S behind
			})
		}
	})
	var ahead, behind bool
	for _, e := range r.events {
		if e.Reason == "NEW_LEVEL" && strings.Contains(e.Text, "ahead") && strings.Contains(e.Text, pts(entryIdx+65*pt)) {
			ahead = true
		}
		if e.Reason == "NEW_LEVEL" && strings.Contains(e.Text, "lock raised") && strings.Contains(e.Text, pts(entryIdx+45*pt)) {
			behind = true
		}
	}
	if !ahead || !behind {
		t.Fatalf("ahead=%v behind=%v events=%+v", ahead, behind, r.events)
	}
	a := last(r)
	if a.NextLevel == nil || a.NextLevel.Price != entryIdx+65*pt || a.LockLevel != entryIdx+42*pt {
		t.Fatalf("next=%+v lock=%d", a.NextLevel, a.LockLevel-entryIdx)
	}
}
