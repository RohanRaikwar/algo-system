package exitwatch

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

// earlyEvents feeds index + option ticks every 250ms for dur and returns
// the EARLY_FAIL events the tracker queued.
func earlyEvents(t *testing.T, pos Position, p Params, dur time.Duration, path pathFn) []Event {
	t.Helper()
	tr, err := NewTracker(pos, p.Compile())
	if err != nil {
		t.Fatal(err)
	}
	tr.SetATR(1500)
	sign := int64(1)
	if pos.Side == "PUT" {
		sign = -1
	}
	var out []Event
	for el := time.Duration(0); el <= dur; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		move := path(el)
		tr.OnIndexTick(pos.IndexEntry+sign*move, ts)
		tr.OnOptionTick(pos.FNOEntryPrice+move/2, 0, 0, 0, ts)
		tr.Evaluate(ts)
		for _, ev := range tr.DrainEvents() {
			if ev.Reason == ReasonEarlyFail {
				out = append(out, ev)
			}
		}
	}
	return out
}

// ramp moves linearly from 0 to to over d, then holds.
func ramp(to int64, d time.Duration) pathFn {
	return func(el time.Duration) int64 {
		if el >= d {
			return to
		}
		return int64(float64(to) * el.Seconds() / d.Seconds())
	}
}

// Stop 20 pts away: the advice needs 10 pts against (50%), floor 8 pts.
func TestEarlyFailFiresOnceWhenTradeGoesStraightAgainst(t *testing.T) {
	for _, pos := range []Position{callPos(), putPos()} {
		evs := earlyEvents(t, pos, DefaultParams(), 4*time.Minute, ramp(-1200, 2*time.Minute))
		if len(evs) != 1 {
			t.Fatalf("%s: want 1 EARLY_FAIL, got %d: %+v", pos.Side, len(evs), evs)
		}
		if evs[0].Action != ActionWatchExit || !strings.HasPrefix(evs[0].Text, "-10.") || !strings.Contains(evs[0].Text, "-20.00 pts from entry") {
			t.Fatalf("%s: bad event %+v", pos.Side, evs[0])
		}
	}
}

func TestEarlyFailQuietCases(t *testing.T) {
	off := DefaultParams()
	off.EarlyFailWindowSec = 0
	upFirst := func(el time.Duration) int64 { // +6 pts at 20s, then -15
		if el <= 20*time.Second {
			return int64(600 * el.Seconds() / 20)
		}
		return 600 - int64(2100*(el.Seconds()-20)/100)
	}
	late := func(el time.Duration) int64 { // flat 5m20s, then -15 pts
		if el < 320*time.Second {
			return 0
		}
		return -1500
	}
	cases := []struct {
		name string
		p    Params
		path pathFn
	}{
		{"was in profit first", DefaultParams(), upFirst},
		{"after the window", DefaultParams(), late},
		{"below the threshold", DefaultParams(), ramp(-900, time.Minute)},
		{"switched off", off, ramp(-1500, time.Minute)},
	}
	for _, c := range cases {
		if evs := earlyEvents(t, callPos(), c.p, 6*time.Minute, c.path); len(evs) != 0 {
			t.Errorf("%s: want no EARLY_FAIL, got %+v", c.name, evs)
		}
	}
}

// Advice only: even with auto-exit on for the strategy, EARLY_FAIL is a
// WATCH_EXIT signal and never an exit request.
func TestEngineEarlyFailIsAdviceNotExitRequest(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	e.SetAutoExit([]string{"NIFTY50_SR"})
	e.SetExchange("99926000", "NSE")
	e.SetATR("99926000", 1500)
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	path := ramp(-1500, 2*time.Minute)
	for el := time.Duration(0); el <= 4*time.Minute; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		m := path(el)
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + m, EventTS: ts})
		e.OnTick(model.Tick{Token: "OPT1", Price: entryPrem + m/2, EventTS: ts})
	}
	evs := signalsWith(sink, ActionWatchExit, ReasonEarlyFail)
	if len(evs) != 1 {
		t.Fatalf("want 1 EARLY_FAIL signal, got %+v", sink.signals)
	}
	if x := evs[0]; x.Price != x.CurrentFNOPrice || x.Price >= entryPrem || !strings.Contains(x.Reason, " idx=") {
		t.Fatalf("not priced at the premium: %+v", x)
	}
	if len(sink.exits) != 0 {
		t.Fatalf("EARLY_FAIL requested an exit: %+v", sink.exits)
	}
}
