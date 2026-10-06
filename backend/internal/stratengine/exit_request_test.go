package stratengine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

type fakeExiter struct {
	side          strategy.PositionSide
	fnoToken, why string
	open          bool
}

func (f *fakeExiter) ExitRequested(side strategy.PositionSide, fnoToken, reason string) *strategy.Signal {
	f.side, f.fnoToken, f.why = side, fnoToken, reason
	if !f.open {
		return nil
	}
	f.open = false
	return &strategy.Signal{StrategyName: "NIFTY50_SR", Action: strategy.ActionExit, Side: side, Reason: reason}
}

func exitPayload(r model.ExitRequest) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func TestExitRequestClosesAllowedStrategy(t *testing.T) {
	now := time.Now()
	svc := &Service{cfg: Config{ExitWatchAuto: []string{"NIFTY50_SR"}}, srStrategy: strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())}
	req := model.ExitRequest{Strategy: "NIFTY50_SR", Side: "CALL", FNOToken: "NFO:45001", Reason: "EXITWATCH REVERSAL", TS: now}

	// No position open in a fresh strategy: ignored with a reason.
	if _, err := svc.exitRequestSignal(exitPayload(req), now); err == nil || !strings.Contains(err.Error(), "no open") {
		t.Fatalf("err = %v", err)
	}
}

func TestExitRequestRefusals(t *testing.T) {
	now := time.Now()
	svc := &Service{cfg: Config{ExitWatchAuto: []string{"NIFTY50_SR", "NIFTY50_FNO"}}, srStrategy: strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())}
	cases := map[string]model.ExitRequest{
		"real orders":  {Strategy: "NIFTY50_FNO", Side: "CALL", TS: now},
		"not allowed":  {Strategy: "NIFTY50_RANGE", Side: "CALL", TS: now},
		"stale":        {Strategy: "NIFTY50_SR", Side: "CALL", TS: now.Add(-time.Minute)},
		"no timestamp": {Strategy: "NIFTY50_SR", Side: "CALL"},
	}
	for name, req := range cases {
		if sig, err := svc.exitRequestSignal(exitPayload(req), now); sig != nil || err == nil {
			t.Errorf("%s: sig=%v err=%v", name, sig, err)
		}
	}
	if _, err := svc.exitRequestSignal("{", now); err == nil {
		t.Error("bad JSON accepted")
	}
}

func TestExitRequestPassesUnqualifiedToken(t *testing.T) {
	now := time.Now()
	f := &fakeExiter{open: true}
	svc := &Service{cfg: Config{ExitWatchAuto: []string{"NIFTY50_SR"}}}
	req := model.ExitRequest{Strategy: "NIFTY50_SR", Side: "PUT", FNOToken: "NFO:45002", Reason: "EXITWATCH STALL", TS: now}
	sig, err := svc.exitRequestVia(f, exitPayload(req), now)
	if err != nil || sig == nil || sig.Side != strategy.SidePut || f.fnoToken != "45002" || f.why != "EXITWATCH STALL" {
		t.Fatalf("sig=%+v err=%v fake=%+v", sig, err, f)
	}
}
