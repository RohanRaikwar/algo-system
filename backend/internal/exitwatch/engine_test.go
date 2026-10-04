package exitwatch

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

type memSink struct {
	pubs      []Payload
	decisions []View
	features  []View
	signals   []SignalEvent
}

func (m *memSink) Publish(p Payload)     { m.pubs = append(m.pubs, p) }
func (m *memSink) RecordDecision(v View) { m.decisions = append(m.decisions, v) }
func (m *memSink) RecordFeatures(v View) { m.features = append(m.features, v) }
func (m *memSink) Signal(ev SignalEvent) { m.signals = append(m.signals, ev) }

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
	pw, dw := p, d
	pw.Weights, dw.Weights = nil, nil
	if p.Compile().W != d.Compile().W || !reflect.DeepEqual(pw, dw) {
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

func signalsWith(sink *memSink, action, reason string) []SignalEvent {
	var out []SignalEvent
	for _, s := range sink.signals {
		if s.Action == action && strings.Contains(s.Reason, " "+reason+":") {
			out = append(out, s)
		}
	}
	return out
}

func TestEngineEmitsRunnerSignalsInPubSignalShape(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	e.SetExchange("99926000", "NSE")
	e.SetOurSRLevels("99926000", []Level{{Price: entryIdx + 70*pt, Type: "OUR_SR:swing"}, {Price: entryIdx + 100*pt, Type: "OUR_SR:pdh"}})
	e.SetATR("99926000", 15*pt)
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	for el := time.Duration(0); el <= 160*time.Second; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		m := runToR2Reject(el)
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + m, EventTS: ts})
		e.OnTick(model.Tick{Token: "OPT1", Price: entryPrem + m/2, EventTS: ts})
	}
	holds := signalsWith(sink, ActionWatchHold, "TARGET_RUN")
	exits := signalsWith(sink, ActionWatchExit, ReasonSRReject)
	if len(holds) != 1 || len(exits) != 1 {
		t.Fatalf("want 1 hold + 1 exit, got %d/%d: %+v", len(holds), len(exits), sink.signals)
	}
	x := exits[0]
	if x.StrategyName != "NIFTY50_SR" || x.Side != "CALL" || x.Token != "99926000" || x.Exchange != "NSE" ||
		x.OrderMode != "SHADOW" || x.FNOToken != "OPT1" || x.EntryFNOPrice != entryPrem || x.Price <= entryIdx+40*pt ||
		!strings.HasPrefix(x.Reason, "EXITWATCH SHADOW RUNNER SR_REJECT: rejected at resistance") {
		t.Fatalf("bad exit signal: %+v", x)
	}
	if _, err := time.Parse(time.RFC3339Nano, x.TS); err != nil {
		t.Fatalf("ts: %v", err)
	}
	if v := sink.pubs[len(sink.pubs)-1].Positions[0]; v.Phase != PhaseRunner || v.ExitReason != ReasonSRReject {
		t.Fatalf("view: phase=%s reason=%s", v.Phase, v.ExitReason)
	}
}

func TestEngineGhostFollowsClosedRunner(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	e.SetOurSRLevels("99926000", []Level{{Price: entryIdx + 70*pt, Type: "OUR_SR:swing"}})
	e.SetATR("99926000", 15*pt)
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	closed := false
	for el := time.Duration(0); el <= 160*time.Second; el += 250 * time.Millisecond {
		ts := t0.Add(el)
		m := runToR2Reject(el)
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + m, EventTS: ts})
		if !closed && m >= 42*pt { // strategy takes its target on the 1m close
			closed = true
			e.SetPositions(nil, ts)
			if e.Open() != 1 || !e.Wants("99926000") {
				t.Fatal("runner must stay tracked as a ghost")
			}
		}
	}
	if len(signalsWith(sink, ActionWatchHold, "GHOST")) != 1 {
		t.Fatalf("missing GHOST hold: %+v", sink.signals)
	}
	exits := signalsWith(sink, ActionWatchExit, ReasonSRReject)
	if len(exits) != 1 || !strings.Contains(exits[0].Reason, "GHOST") || !strings.Contains(exits[0].Reason, "extra=+") {
		t.Fatalf("ghost exit wrong: %+v", exits)
	}
	if e.Open() != 0 {
		t.Fatal("ghost not removed after its exit")
	}
}

func TestEngineGhostExpires(t *testing.T) {
	sink := &memSink{}
	p := DefaultParams()
	p.GhostMaxMin = 1
	e := NewEngine(p.Compile(), sink, true)
	e.SetATR("99926000", 15*pt)
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	var ts time.Time
	for el := time.Duration(0); el <= 100*time.Second; el += 250 * time.Millisecond {
		ts = t0.Add(el)
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + lin(el.Seconds(), 0, 100, 0, 50), EventTS: ts})
	}
	e.SetPositions(nil, ts)
	for el := time.Duration(0); el <= 70*time.Second; el += time.Second {
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + 50*pt + int64(el/time.Second)*10, EventTS: ts.Add(el)})
	}
	if len(signalsWith(sink, ActionWatchExit, "GHOST_END")) != 1 || e.Open() != 0 {
		t.Fatalf("ghost should end after 1 min: open=%d %+v", e.Open(), sink.signals)
	}
}

func TestEngineTightenSignalRateLimited(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	for el := time.Duration(0); el <= 300*time.Second; el += 250 * time.Millisecond {
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + chop(el), EventTS: t0.Add(el)})
	}
	n := len(signalsWith(sink, ActionWatchTighten, "TIGHTEN"))
	if n < 1 || n > 6 {
		t.Fatalf("want 1..6 TIGHTEN signals in 5 min, got %d", n)
	}
}

func TestParseLevelFeeds(t *testing.T) {
	tok, ls, err := parseAnalystLevels([]byte(`{"token":"99926000","levels":[{"price":2410000,"type":"HORIZONTAL","created_at":"2026-10-05T04:00:00Z"},{"price":0}]}`))
	if err != nil || tok != "99926000" || len(ls) != 1 || ls[0].Type != "ANALYST:HORIZONTAL" || ls[0].Since.IsZero() {
		t.Fatalf("analyst: %v %s %+v", err, tok, ls)
	}
	tok, ls, err = parseOurSRLevels([]byte(`{"key":"NSE:99926000","levels":[{"price":2405000,"touches":3,"source":"swing"}]}`))
	if err != nil || tok != "99926000" || len(ls) != 1 || ls[0].Type != "OUR_SR:swing" || !ls[0].Since.IsZero() {
		t.Fatalf("our sr: %v %s %+v", err, tok, ls)
	}
	if _, _, err := parseOurSRLevels([]byte(`{"levels":[]}`)); err == nil {
		t.Fatal("missing key must error")
	}
	if _, _, err := parseAnalystLevels([]byte(`nope`)); err == nil {
		t.Fatal("bad json must error")
	}
}

func TestEngineOurSRLevelPublishedMidRunRaisesLock(t *testing.T) {
	sink := &memSink{}
	e := NewEngine(DefaultParams().Compile(), sink, true)
	e.SetATR("99926000", 15*pt)
	e.SetOurSRLevels("99926000", []Level{{Price: entryIdx + 100*pt, Type: "OUR_SR:pdh"}})
	e.SetPositions([]model.PositionContext{ctxCall()}, t0)
	for el := time.Duration(0); el <= 120*time.Second; el += 250 * time.Millisecond {
		if el == 100*time.Second { // like a pub:sr update: no Since on the wire
			e.SetOurSRLevels("99926000", []Level{
				{Price: entryIdx + 100*pt, Type: "OUR_SR:pdh"},
				{Price: entryIdx + 45*pt, Type: "OUR_SR:swing"},
			})
		}
		e.OnTick(model.Tick{Token: "99926000", Price: entryIdx + lin(el.Seconds(), 0, 120, 0, 60), EventTS: t0.Add(el)})
	}
	if got := signalsWith(sink, ActionWatchHold, "NEW_LEVEL"); len(got) != 1 || !strings.Contains(got[0].Reason, "lock raised to 24042.00") {
		t.Fatalf("want one lock raise to 24042, got %+v", got)
	}
}
