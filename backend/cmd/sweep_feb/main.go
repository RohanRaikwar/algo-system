package main

import (
	"fmt"
	"io"
	"log"
	"sort"

	"trading-systemv1/internal/backtest"
	"trading-systemv1/internal/strategy"
)

type SweepResult struct {
	EMA6  int
	EMA9  int
	SMA21 int
	NetPts float64
	WinRate float64
	MaxDD float64
	Trades int
}

func main() {
	log.SetFlags(0)
	log.SetOutput(io.Discard)
	
	cfg := backtest.Config{
		DBPath:       "data/historical.db",
		Exchange:     "NSE",
		Token:        "99926000",
		Qty:          1,
		From:         "2026-02-01",
		To:           "2026-02-28",
		StrategyType: "nifty50_10pts",
		CandleTF:     1,
	}

	engine := backtest.New(cfg)
	candles, err := engine.LoadCandlesForSweep()
	if err != nil {
		log.Fatalf("failed to load candles: %v", err)
	}
	
	fmt.Printf("Loaded %d candles for Feb 2026\n", len(candles))
	
	var results []SweepResult
	
	// EMA6 (fast)
	for e1 := 3; e1 <= 9; e1++ {
		// EMA9 (slow, must be > EMA6)
		for e2 := e1 + 2; e2 <= 21; e2 += 2 {

			stratCfg := strategy.DefaultNifty5010PtsConfig()
			stratCfg.EMA6Period = e1
			stratCfg.EMA9Period = e2

			// Keep other params default to strictly test the indicator variance
			cfg.StrategyCfg10Pts = &stratCfg

			eng := backtest.New(cfg)
			res, err := eng.RunWithCandles(candles)
			if err != nil {
				continue
			}

			netPts := res.Metrics.NetPnL

			results = append(results, SweepResult{
				EMA6:    e1,
				EMA9:    e2,
				NetPts:  netPts,
				WinRate: res.Metrics.WinRate,
				MaxDD:   res.Metrics.MaxDrawdown,
				Trades:  res.Metrics.TotalTrades,
			})
		}
	}
	
	// Sort by NetPts descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].NetPts > results[j].NetPts
	})
	
	fmt.Println("Top 15 Indicator Combinations for Feb 2026:")
	fmt.Printf("%-15s | %-10s | %-10s | %-10s | %-10s\n", "EMA/EMA/SMA", "Net P&L", "Max DD", "Win%", "Trades")
	fmt.Println("------------------------------------------------------------------")
	for i := 0; i < 15 && i < len(results); i++ {
		r := results[i]
		fmt.Printf("%2d / %2d / %2d | +%-9.2f | %-10.2f | %-8.1f%% | %d\n",
			r.EMA6, r.EMA9, r.SMA21, r.NetPts, r.MaxDD, r.WinRate, r.Trades)
	}
}
