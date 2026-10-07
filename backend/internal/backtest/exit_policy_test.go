package backtest

import (
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/exitpolicy"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// hammerRange is the range fixture with day 4's 5m hammer at support.
func hammerRange() []model.TFCandle {
	candles := rangeDays(4)
	sup := int64(2389800)
	target := time.Date(2026, 1, 8, 10, 25, 0, 0, btIST)
	hammer := [][4]int64{{sup + 500, sup + 700, sup + 200, sup + 300}, {sup + 300, sup + 400, sup - 2000, sup - 1500},
		{sup - 1500, sup - 200, sup - 1800, sup - 300}, {sup - 300, sup + 900, sup - 400, sup + 800}, {sup + 800, sup + 1100, sup + 700, sup + 1000}}
	for i := range candles {
		for k, h := range hammer {
			if candles[i].TS.Equal(target.Add(time.Duration(k) * time.Minute)) {
				candles[i].Open, candles[i].High, candles[i].Low, candles[i].Close = h[0], h[1], h[2], h[3]
			}
		}
	}
	return candles
}

// alwaysFlat: every trade is flat and out of time after one minute.
func alwaysFlat(mode exitpolicy.Mode) *exitpolicy.StrategyConfig {
	return &exitpolicy.StrategyConfig{Mode: mode, Theta: exitpolicy.ThetaConfig{
		MaxFlatMin: 1, DecayPctOfGain: 1000, FlatProgressPct: 100000, FallbackFlatPremPct: 100000, ConfirmMin: 1}}
}

func runPolicy(t *testing.T, pol *exitpolicy.StrategyConfig) []Trade {
	t.Helper()
	rc := strategy.DefaultNifty50RangeConfig()
	rc.MeanReversionEnabled = true
	e := New(Config{Exchange: "NSE", Token: "99926000", Qty: 1, StrategyType: "nifty50_range", StrategyCfgRange: &rc,
		Option:     OptionModel{Enabled: true, IVPct: 13, RatePct: 6.5, SlippageBps: 50, SlippageMinPsa: 50, StrikeStep: 50},
		ExitPolicy: pol})
	res, err := e.RunWithCandles(hammerRange())
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
