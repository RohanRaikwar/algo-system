package backtest

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/exitpolicy"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

var btIST = time.FixedZone("IST", 5*3600+30*60)

// rangeDays builds n sessions of a 23,900-24,100 triangle wave (period 2h)
// as directional 1m candles.
func rangeDays(n int) []model.TFCandle {
	var out []model.TFCandle
	prev := int64(2390000)
	idx := 0
	day := time.Date(2026, 1, 5, 0, 0, 0, 0, btIST)
	for d := 0; d < n; d++ {
		start := time.Date(day.Year(), day.Month(), day.Day(), 9, 15, 0, 0, btIST)
		for i := 0; i < 375; i++ {
			pos := idx % 120
			var p int64
			if pos < 60 {
				p = 2390000 + 20000*int64(pos)/60
			} else {
				p = 2410000 - 20000*int64(pos-60)/60
			}
			idx++
			hi, lo := prev, p
			if p > prev {
				hi, lo = p, prev
			}
			out = append(out, model.TFCandle{Token: "99926000", Exchange: "NSE", TF: 60, TS: start.Add(time.Duration(i) * time.Minute).UTC(),
				Open: prev, High: hi + 200, Low: lo - 200, Close: p})
			prev = p
		}
		day = day.AddDate(0, 0, 1)
	}
	return out
}

// scriptedCall buys one CALL at 10:25 IST on the last fixture day and
// exits at 14:30 unless the exit policy closes it first.
type scriptedCall struct{ open bool }

var (
	scriptedEntry = time.Date(2026, 1, 8, 10, 25, 0, 0, btIST)
	scriptedExit  = time.Date(2026, 1, 8, 14, 30, 0, 0, btIST)
)

func (s *scriptedCall) Name() string                       { return "SCRIPTED" }
func (s *scriptedCall) OnTick(model.Tick) *strategy.Signal { return nil }
func (s *scriptedCall) ResetPositions()                    { s.open = false }
func (s *scriptedCall) OnTFCandle(c model.TFCandle) *strategy.Signal {
	switch {
	case !s.open && c.TS.Equal(scriptedEntry):
		s.open = true
		return &strategy.Signal{StrategyName: s.Name(), Action: strategy.ActionBuy, Side: strategy.SideCall,
			Strike: (c.Close + 2500) / 5000 * 50, TargetMove: 4000, Reason: "scripted entry"}
	case s.open && c.TS.Equal(scriptedExit):
		s.open = false
		return &strategy.Signal{StrategyName: s.Name(), Action: strategy.ActionExit, Side: strategy.SideCall, Reason: "scripted exit"}
	}
	return nil
}

func (s *scriptedCall) ExitRequested(side strategy.PositionSide, _, reason string) *strategy.Signal {
	if !s.open {
		return nil
	}
	s.open = false
	return &strategy.Signal{StrategyName: s.Name(), Action: strategy.ActionExit, Side: side, Reason: reason}
}

// alwaysFlat: every trade is flat and out of time after one minute.
func alwaysFlat(mode exitpolicy.Mode) *exitpolicy.StrategyConfig {
	return &exitpolicy.StrategyConfig{Mode: mode, Theta: exitpolicy.ThetaConfig{
		MaxFlatMin: 1, DecayPctOfGain: 1000, FlatProgressPct: 100000, FallbackFlatPremPct: 100000, ConfirmMin: 1}}
}

func runPolicy(t *testing.T, pol *exitpolicy.StrategyConfig) []Trade {
	t.Helper()
	e := New(Config{Exchange: "NSE", Token: "99926000", Qty: 1,
		Option:     OptionModel{Enabled: true, IVPct: 13, RatePct: 6.5, SlippageBps: 50, SlippageMinPsa: 50, StrikeStep: 50},
		ExitPolicy: pol})
	e.strat = &scriptedCall{}
	res, err := e.RunWithCandles(rangeDays(4))
	if err != nil || len(res.Trades) == 0 {
		t.Fatalf("no trades: %v", err)
	}
	return res.Trades
}

func TestBacktestExitPolicyActs(t *testing.T) {
	base := runPolicy(t, nil)[0]
	tr := runPolicy(t, alwaysFlat(exitpolicy.ModeAct))[0]
	if !strings.HasPrefix(tr.ExitReason, "THETA CALL flat") {
		t.Fatalf("exit reason %q (no policy: %q)", tr.ExitReason, base.ExitReason)
	}
	if !tr.ExitTime.Before(base.ExitTime) {
		t.Fatalf("theta exit %s not before baseline %s", tr.ExitTime, base.ExitTime)
	}
	if tr.FNOExitPrice <= 0 {
		t.Fatalf("theta exit not option-priced: %+v", tr)
	}
}

func TestBacktestExitPolicyShadowKeepsTrades(t *testing.T) {
	base := runPolicy(t, nil)
	sh := runPolicy(t, alwaysFlat(exitpolicy.ModeShadow))
	if len(sh) != len(base) || sh[0].ExitReason != base[0].ExitReason || sh[0].ExitTime != base[0].ExitTime {
		t.Fatalf("shadow changed trades: %+v vs %+v", sh[0], base[0])
	}
	if !strings.HasPrefix(sh[0].ShadowExit, "THETA") || sh[0].ShadowExitFNO <= 0 || sh[0].ShadowExitTime.IsZero() {
		t.Fatalf("shadow exit not recorded: %+v", sh[0])
	}
	if base[0].ShadowExit != "" {
		t.Fatal("no policy must record no shadow exit")
	}
}
