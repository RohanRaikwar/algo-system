package exitwatch

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

type memSink struct {
	pubs      []Payload
	decisions []View
	features  []View
}

func (m *memSink) Publish(p Payload)     { m.pubs = append(m.pubs, p) }
func (m *memSink) RecordDecision(v View) { m.decisions = append(m.decisions, v) }
func (m *memSink) RecordFeatures(v View) { m.features = append(m.features, v) }

func ctxCall() model.PositionContext {
	p := callPos()
	p.IndexToken = "99926000"
	return p
}

func TestEngineRoutesTicksAndPublishesExit(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	e.SetATR("99926000", 1500)
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	if e.Open() != 1 || !e.Wants("99926000") || !e.Wants("OPT1") || e.Wants("OTHER") {
		t.Fatalf("routing state wrong")
	}

	for el := time.Duration(0); el <= 140*time.Second; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		m := stallReverse(el)
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + m, EventTS: ts})
		e.OnTick(model.Tick{Token: "OPT1", Price: entryPrem + m/2, EventTS: ts})
		e.OnTick(model.Tick{Token: "OTHER", Price: 1, EventTS: ts}) // ignored
	}

	last := sink.pubs[len(sink.pubs)-1]
	if !last.Shadow || len(last.Positions) != 1 || last.Positions[0].Decision != DecisionExit {
		t.Fatalf("want shadow EXIT in last payload, got %+v", last)
	}
	var sawExit bool
	for _, d := range sink.decisions {
		if d.Decision == DecisionExit {
			sawExit = true
		}
	}
	if !sawExit {
		t.Fatal("EXIT decision not recorded")
	}
	// one features row per second, ~141 seconds
	if n := len(sink.features); n < 135 || n > 145 {
		t.Fatalf("want ~141 feature rows, got %d", n)
	}
	// 2 ticks/250ms for 140s but publish throttled to ≤ 2/s plus changes
	if n := len(sink.pubs); n > 2*141+10 {
		t.Fatalf("publish not throttled: %d", n)
	}
}

func TestEngineKeepsLatchAcrossStopMoveAndDropsClosed(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	p := ctxCall()
	e.SetPositions([]model.PositionContext{p}, t0)
	for el := time.Duration(0); el <= 140*time.Second; el += 250 * time.Millisecond {
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + stallReverse(el), EventTS: t0.Add(el)})
	}
	p.StopLevel = p.IndexEntry // breakeven
	e.SetPositions([]model.PositionContext{p}, t0.Add(141*time.Second))
	last := sink.pubs[len(sink.pubs)-1]
	if last.Positions[0].Decision != DecisionExit || last.Positions[0].StopLevel != p.IndexEntry {
		t.Fatalf("latch or stop update lost: %+v", last.Positions[0])
	}

	e.SetPositions(nil, t0.Add(142*time.Second))
	if e.Open() != 0 {
		t.Fatal("closed position still tracked")
	}
	if last := sink.pubs[len(sink.pubs)-1]; len(last.Positions) != 0 {
		t.Fatalf("want empty payload after close, got %+v", last)
	}
}

func TestEngineIdleDoesNotPublish(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	for i := 0; i < 10; i++ {
		e.Evaluate(t0.Add(time.Duration(i) * time.Second))
	}
	if len(sink.pubs) != 0 {
		t.Fatalf("idle engine published %d payloads", len(sink.pubs))
	}
}

func TestComputeATR(t *testing.T) {
	cs := []model.TFCandle{
		{High: 110, Low: 100, Close: 105},
		{High: 112, Low: 104, Close: 110}, // TR 8
		{High: 111, Low: 100, Close: 101}, // TR max(11, 1, 10) = 11
		{High: 120, Low: 115, Close: 118}, // gap up: TR max(5, 19, -14) = 19
	}
	atr, ok := computeATR(cs)
	if !ok || atr != (8+11+19)/3 {
		t.Fatalf("atr=%d ok=%v", atr, ok)
	}
	if _, ok := computeATR(cs[:1]); ok {
		t.Fatal("one candle must not give ATR")
	}
}

func TestShippedParamsFileMatchesDefaults(t *testing.T) {
	p, err := LoadParams("../../config/exitwatch.json")
	if err != nil {
		t.Fatal(err)
	}
	d := DefaultParams()
	if p.Compile().W != d.Compile().W || p.Bias != d.Bias || p.ExitP != d.ExitP || p.TimeExitMin != d.TimeExitMin {
		t.Fatalf("config/exitwatch.json drifted from DefaultParams")
	}
}

func TestEngineClockFollowsTicksNotWall(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	// t0 is far from wall time: timer calls must not jump stall by the skew
	e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + 1000, EventTS: t0})
	e.Tick(time.Now().Add(500 * time.Millisecond))
	last := sink.pubs[len(sink.pubs)-1].Positions[0]
	if st := last.Features["stall"]; st > 1 {
		t.Fatalf("stall %v: wall clock leaked into tick time", st)
	}
	// time never moves back: an older tick after a timer advance
	e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + 1000, EventTS: t0.Add(-time.Second)})
	if e.nowTS.Before(t0.Add(400 * time.Millisecond)) {
		t.Fatalf("engine clock moved back to %v", e.nowTS)
	}
}
