package backtest

import (
	"testing"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

var btIST = time.FixedZone("IST", 5*3600+30*60)

// rangeDays builds n sessions of a 23,900-24,100 triangle wave (period 2h)
// as directional 1m candles, then a 5m hammer at support on the last day.
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

func TestBacktestRunsRangeStrategyEndToEnd(t *testing.T) {
	candles := rangeDays(4)
	// Replace day 4's 10:25-10:29 with bars that form a 5m hammer at support (~23,898).
	sup := int64(2389800)
	hammer := []struct{ o, h, l, c int64 }{
		{sup + 500, sup + 700, sup + 200, sup + 300},
		{sup + 300, sup + 400, sup - 2000, sup - 1500},
		{sup - 1500, sup - 200, sup - 1800, sup - 300},
		{sup - 300, sup + 900, sup - 400, sup + 800},
		{sup + 800, sup + 1100, sup + 700, sup + 1000},
	}
	target := time.Date(2026, 1, 8, 10, 25, 0, 0, btIST)
	for i := range candles {
		for k, h := range hammer {
			if candles[i].TS.Equal(target.Add(time.Duration(k) * time.Minute)) {
				candles[i].Open, candles[i].High, candles[i].Low, candles[i].Close = h.o, h.h, h.l, h.c
			}
		}
	}
	rc := strategy.DefaultNifty50RangeConfig()
	rc.MeanReversionEnabled = true // the fixture is a mean-reversion setup
	e := New(Config{Exchange: "NSE", Token: "99926000", Qty: 1, StrategyType: "nifty50_range", StrategyCfgRange: &rc})
	res, err := e.RunWithCandles(candles)
	if err != nil {
		t.Fatal(err)
	}
	if res.Metrics.TotalTrades == 0 {
		t.Fatal("range strategy produced no trades on a clean range with a 5m hammer at support")
	}
	for _, tr := range res.Trades {
		if tr.ExitTime.IsZero() || tr.ExitReason == "" {
			t.Errorf("trade %d not closed: %+v", tr.ID, tr)
		}
	}
	first := res.Trades[0]
	if first.Side != "CALL" || first.EntryTime.In(btIST).Format("15:04") != "10:29" {
		t.Errorf("first trade = %s at %s, want CALL at 10:29", first.Side, first.EntryTime.In(btIST).Format("15:04"))
	}
}

func TestBacktestOptionModelPricesEachTrade(t *testing.T) {
	candles := rangeDays(4)
	rc := strategy.DefaultNifty50RangeConfig()
	rc.MeanReversionEnabled = true
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
	e := New(Config{Exchange: "NSE", Token: "99926000", Qty: 1, StrategyType: "nifty50_range", StrategyCfgRange: &rc,
		Option: OptionModel{Enabled: true, IVPct: 13, RatePct: 6.5, PremiumSLPct: 20, SlippageBps: 50, SlippageMinPsa: 50, StrikeStep: 50}})
	res, err := e.RunWithCandles(candles)
	if err != nil || len(res.Trades) == 0 {
		t.Fatalf("no trades: %v", err)
	}
	tr := res.Trades[0]
	if tr.Strike == 0 || tr.FNOEntryPrice <= 0 || tr.FNOExitPrice <= 0 {
		t.Fatalf("trade not option-priced: %+v", tr)
	}
	// P&L now comes from premiums, not index points.
	if tr.PnLPaise() != tr.FNOExitPrice-tr.FNOEntryPrice {
		t.Fatalf("P&L not premium-based: %+v", tr)
	}
}
