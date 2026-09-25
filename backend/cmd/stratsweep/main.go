package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"sync"
	"time"
	"trading-systemv1/internal/backtest"
	"trading-systemv1/internal/strategy"
)

type SweepConfig struct {
	SL         float64
	TrailSL    float64
	TrailStart float64
	Sep        float64
	SkipFirst  int
}

type SweepResult struct {
	Config SweepConfig
	Result backtest.Result
}

func main() {
	dbPath := flag.String("db", "data/historical.db", "Path to historical SQLite database")
	token := flag.String("token", "99926000", "Symbol token to backtest")
	exchange := flag.String("exchange", "NSE", "Exchange")
	qty := flag.Int64("qty", 1, "Trade quantity")
	flag.Parse()

	// Disable all strategy and engine logging to prevent console spam
	log.SetOutput(io.Discard)

	// Parameter ranges
	sls := []float64{0.10, 0.15, 0.20, 0.25, 0.30, 0.40, 0.50}
	trails := []float64{0.06, 0.08, 0.10, 0.15, 0.20}
	starts := []float64{0.04, 0.06, 0.10}
	seps := []float64{0.05, 0.10, 0.15}
	skips := []int{15, 30, 45}

	var configs []SweepConfig
	for _, sl := range sls {
		for _, trail := range trails {
			for _, start := range starts {
				for _, sep := range seps {
					for _, skip := range skips {
						configs = append(configs, SweepConfig{
							SL:         sl,
							TrailSL:    trail,
							TrailStart: start,
							Sep:        sep,
							SkipFirst:  skip,
						})
					}
				}
			}
		}
	}

	total := len(configs)
	fmt.Fprintf(os.Stderr, "Testing %d combinations...\n", total)

	// Pre-load candles into memory (using a dummy engine just to load them)
	dummyCfg := backtest.Config{
		DBPath:       *dbPath,
		Exchange:     *exchange,
		Token:        *token,
		StrategyType: "nifty50_fno_sl2",
		CandleTF:     2,
	}
	dummyEngine := backtest.New(dummyCfg)
	
	startLoad := time.Now()
	candles, err := dummyEngine.LoadCandlesForSweep()
	if err != nil {
		log.Fatalf("Failed to load candles: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d 2m candles in %v\n", len(candles), time.Since(startLoad))

	startSweep := time.Now()
	
	results := make([]SweepResult, 0, total)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Worker pool (limit to runtime.NumCPU or reasonable number to avoid memory explosion)
	numWorkers := 16
	jobs := make(chan SweepConfig, total)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			
			// Each worker needs its own engine so they don't share state
			for cfg := range jobs {
				stratCfg := strategy.DefaultNifty50FnOConfig()
				stratCfg.HardSLPct = cfg.SL
				stratCfg.TrailSLPct = cfg.TrailSL
				stratCfg.TrailStartPct = cfg.TrailStart
				stratCfg.MinReEntrySeparationPct = cfg.Sep
				stratCfg.SkipFirstMinutes = cfg.SkipFirst
				
				// SL2 specific overrides
				stratCfg.MomentumBypassPct = 0
				
				engCfg := dummyCfg
				engCfg.StrategyCfg = stratCfg
				engCfg.Qty = *qty
				
				// Run engine using pre-loaded candles
				engine := backtest.New(engCfg)
				res, err := engine.RunWithCandles(candles)
				if err != nil {
					continue
				}

				mu.Lock()
				results = append(results, SweepResult{Config: cfg, Result: res})
				completed := len(results)
				if completed%100 == 0 {
					fmt.Fprintf(os.Stderr, "[%d/%d] Sweeping...\n", completed, total)
				}
				mu.Unlock()
			}
		}()
	}

	for _, c := range configs {
		jobs <- c
	}
	close(jobs)
	wg.Wait()

	fmt.Fprintf(os.Stderr, "Sweep completed in %v\n\n", time.Since(startSweep))

	// Sort by Net PnL descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Result.Metrics.NetPnL > results[j].Result.Metrics.NetPnL
	})

	fmt.Println("sl,trail_sl,trail_start,sep,skip_first,trades,wins,losses,win_rate,net_pnl,profit_factor,max_drawdown,sharpe")
	for i, r := range results {
		c := r.Config
		res := r.Result
		m := res.Metrics
		pnl := float64(m.NetPnL) / 100.0
		dd := float64(m.MaxDrawdown) / 100.0
		fmt.Printf("%.2f,%.2f,%.2f,%.2f,%d,%d,%d,%d,%.1f,%.2f,%.2f,%.2f,%.2f\n",
			c.SL, c.TrailSL, c.TrailStart, c.Sep, c.SkipFirst,
			m.TotalTrades, m.Wins, m.Losses, m.WinRate,
			pnl, m.ProfitFactor, dd, m.SharpeRatio,
		)
		if i >= 49 { // Print top 50
			break
		}
	}
}
