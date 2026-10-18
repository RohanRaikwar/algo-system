package stratengine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

type fakeCommander struct {
	fakeExiter
	edit    strategy.LevelEdit
	editTok string
	err     error
}

func (f *fakeCommander) SetLevels(side strategy.PositionSide, fnoToken string, e strategy.LevelEdit) error {
	f.side, f.editTok, f.edit = side, fnoToken, e
	return f.err
}

func cmdPayload(c model.PositionCommand) string {
	b, _ := json.Marshal(c)
	return string(b)
}

func TestPositionCommandRefusals(t *testing.T) {
	now := time.Now()
	svc := &Service{cfg: Config{}, srStrategy: strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig())}
	cases := map[string]model.PositionCommand{
		"real orders":   {Strategy: model.RealOrderStrategy, Side: "CALL", Action: model.PositionExit, TS: now},
		"unknown":       {Strategy: "OTHER_SR", Side: "CALL", Action: model.PositionExit, TS: now},
		"stale":         {Strategy: "NIFTY50_SR", Side: "CALL", Action: model.PositionExit, TS: now.Add(-time.Minute)},
		"no timestamp":  {Strategy: "NIFTY50_SR", Side: "CALL", Action: model.PositionExit},
		"bad action":    {Strategy: "NIFTY50_SR", Side: "CALL", Action: "buy", TS: now},
		"bad side":      {Strategy: "NIFTY50_SR", Side: "LONG", Action: model.PositionExit, TS: now},
		"no level":      {Strategy: "NIFTY50_SR", Side: "CALL", Action: model.PositionSetLevels, TS: now},
		"negative stop": {Strategy: "NIFTY50_SR", Side: "CALL", Action: model.PositionSetLevels, IndexStop: -5, TS: now},
		"flat book":     {Strategy: "NIFTY50_SR", Side: "CALL", Action: model.PositionExit, TS: now},
	}
	for name, c := range cases {
		if sig, err := svc.positionCommand(cmdPayload(c), now); sig != nil || err == nil {
			t.Errorf("%s: sig=%v err=%v", name, sig, err)
		}
	}
	if _, err := svc.positionCommand("{", now); err == nil {
		t.Error("bad JSON accepted")
	}
}

func TestPositionCommandRoutesSensex(t *testing.T) {
	now := time.Now()
	cfg := strategy.DefaultNifty50SRConfig()
	cfg.NameOverride = "SENSEX_SR"
	svc := &Service{srStrategy: strategy.NewNifty50SR(65, strategy.DefaultNifty50SRConfig()),
		srExtras: []*srExtra{{strat: strategy.NewNifty50SR(20, cfg)}}}
	c := model.PositionCommand{Strategy: "SENSEX_SR", Side: "PUT", Action: model.PositionExit, TS: now}
	// Reaches SENSEX_SR, which has no open position.
	if _, err := svc.positionCommand(cmdPayload(c), now); err == nil || !strings.Contains(err.Error(), "no open SENSEX_SR") {
		t.Fatalf("err = %v", err)
	}
}

func TestPositionCommandVia(t *testing.T) {
	svc := &Service{}
	f := &fakeCommander{fakeExiter: fakeExiter{open: true}}
	sig, err := svc.positionCommandVia(f, model.PositionCommand{Strategy: "NIFTY50_SR", Side: "CALL", FNOToken: "NFO:45001", Action: model.PositionExit})
	if err != nil || sig == nil || sig.Action != strategy.ActionExit || f.fnoToken != "45001" || f.why != "MANUAL EXIT" {
		t.Fatalf("exit sig=%+v err=%v fake=%+v", sig, err, f)
	}

	set := model.PositionCommand{Strategy: "NIFTY50_SR", Side: "PUT", FNOToken: "NFO:45002", Action: model.PositionSetLevels,
		IndexStop: 2290000, FNOSL: 7000, FNOTarget: -1}
	if sig, err := svc.positionCommandVia(f, set); sig != nil || err != nil {
		t.Fatalf("set sig=%v err=%v", sig, err)
	}
	want := strategy.LevelEdit{IndexStop: 2290000, FNOSL: 7000, FNOTarget: -1}
	if f.edit != want || f.editTok != "45002" || f.side != strategy.SidePut {
		t.Fatalf("edit %+v tok=%s side=%s", f.edit, f.editTok, f.side)
	}
	f.err = errors.New("not below LTP")
	if _, err := svc.positionCommandVia(f, set); err == nil {
		t.Fatal("strategy refusal lost")
	}
}

func TestPaperTargetOffForRealStrategy(t *testing.T) {
	c := strategy.DefaultNifty50SRConfig()
	c.FNOTargetPct = 40
	if got := paperTarget(c, "NIFTY50_SR", []string{"NIFTY50_FNO"}); got.FNOTargetPct != 40 {
		t.Errorf("paper target %d", got.FNOTargetPct)
	}
	if got := paperTarget(c, "NIFTY50_SR", []string{"NIFTY50_SR"}); got.FNOTargetPct != 0 {
		t.Errorf("real strategy target %d", got.FNOTargetPct)
	}
}
