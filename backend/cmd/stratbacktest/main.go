// cmd/stratbacktest replays historical 1m candle data through a selected strategy.
//
// Usage:
//
//	go run ./cmd/stratbacktest --db=data/historical.db --token=99926000
//	go run ./cmd/stratbacktest --output=json --outdir=./results
//	go run ./cmd/stratbacktest --help
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"trading-systemv1/internal/backtest"
	"trading-systemv1/internal/strategy"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	// ── Core flags ──
	dbPath := flag.String("db", "data/historical.db", "Path to historical SQLite database")
	token := flag.String("token", "99926000", "Symbol token to backtest")
	exchange := flag.String("exchange", "NSE", "Exchange")
	qty := flag.Int64("qty", 1, "Trade quantity for P&L calculation")
	from := flag.String("from", "", "Start date YYYY-MM-DD (optional)")
	to := flag.String("to", "", "End date YYYY-MM-DD (optional)")
	output := flag.String("output", "console", "Output format: console, json, csv")
	outDir := flag.String("outdir", ".", "Output directory for json/csv exports")
	stratFlag := flag.String("strategy", "nifty50_fno", "Strategy type: nifty50_fno, nifty50_fno_sl, nifty50_10pts")
	target := flag.Int("target", 1000, "Target profit in paise (10 Nifty pts = 1000)")
	vwap := flag.Bool("vwap", false, "Require price > VWAP for CALL and price < VWAP for PUT")
	adx := flag.Int("adx", 0, "Require ADX(14) > threshold to enter trades (0=disabled, 20=trending)")
	sep := flag.Float64("sep", 0, "Re-entry sep guard: min %% EMA6 from SMA21 (0=off)")
	sideways := flag.Bool("sideways", false, "Enable sideways market filter")
	chop := flag.Int("chop", 3, "Max EMA6×EMA9 crosses in 10 candles before chop signal")
	flat := flag.Float64("flat", 0.05, "MA21 max-min/MA21 < this %% = flat")
	rng := flag.Float64("range", 0.30, "Price high-low/close < this %% = narrow range")
	trend := flag.Bool("trend", false, "Enable trend direction filter (block CALL in downtrend, PUT in uptrend)")
	trendLookback := flag.Int("trend-lookback", 50, "Candles to look back for MA21 slope (50 = 50 min)")
	trendSlope := flag.Float64("trend-slope", 0.05, "MA21 must move > this %% over lookback to be trending")
	momentum := flag.Float64("momentum", 0, "Momentum bypass: skip sideways if |close-MA21|/MA21 > this %% (0=off)")
	cooldown := flag.Int("cooldown", 0, "Candles to wait after exit before re-entering (0=immediate)")
	skipFirst := flag.Int("skip-first", 0, "Skip fresh entries in first N minutes after 9:15")
	skipLast := flag.Int("skip-last", 0, "Skip fresh entries in last N minutes before 15:30")
	sl := flag.Float64("sl", 0.15, "Hard stop loss as %% of entry price (0=disabled)")
	trailSL := flag.Float64("trail-sl", 0, "Trailing stop loss as %% behind best price (0=disabled)")
	trailStart := flag.Float64("trail-start", 0.10, "Start trailing after this %% of profit (0=immediate)")
	consecExit := flag.Bool("consec-exit", false, "Exit CALL on lower lows, PUT on higher highs")
	candleTF := flag.Int("tf", 1, "Candle timeframe in minutes (1=1m, 2=2m, 5=5m)")

	// FNO option price tracking
	callToken := flag.String("call-token", "", "NFO token for CALL option (e.g. 57709)")
	putToken := flag.String("put-token", "", "NFO token for PUT option (e.g. 57710)")
	fnoExchange := flag.String("fno-exchange", "NFO", "Exchange for FNO tokens")

	flag.Parse()

	strategyCfg := strategy.DefaultNifty50FnOConfig()
	strategyCfg.TargetProfitPts = int64(*target)
	strategyCfg.VwapEnabled = *vwap
	strategyCfg.AdxThreshold = *adx
	strategyCfg.MinReEntrySeparationPct = *sep
	strategyCfg.SidewaysEnabled = *sideways
	strategyCfg.MaxChopCrosses = *chop
	strategyCfg.MA21FlatPct = *flat
	strategyCfg.NarrowRangePct = *rng
	strategyCfg.TrendEnabled = *trend
	strategyCfg.TrendLookback = *trendLookback
	strategyCfg.TrendSlopePct = *trendSlope
	strategyCfg.MomentumBypassPct = *momentum
	strategyCfg.CooldownCandles = *cooldown
	strategyCfg.SkipFirstMinutes = *skipFirst
	strategyCfg.SkipLastMinutes = *skipLast
	strategyCfg.HardSLPct = *sl
	strategyCfg.TrailSLPct = *trailSL
	strategyCfg.TrailStartPct = *trailStart
	strategyCfg.ConsecCandleExit = *consecExit
	if strings.EqualFold(strings.TrimSpace(*stratFlag), "nifty50_fno_sl") {
		// SL variant intentionally runs with momentum bypass disabled.
		strategyCfg.MomentumBypassPct = 0
	}

	cfg := backtest.Config{
		DBPath:       *dbPath,
		Exchange:     *exchange,
		Token:        *token,
		Qty:          *qty,
		From:         *from,
		To:           *to,
		Output:       *output,
		OutDir:       *outDir,
		StrategyType: *stratFlag,
		StrategyCfg:  strategyCfg,
		CallFNOToken: *callToken,
		PutFNOToken:  *putToken,
		FNOExchange:  *fnoExchange,
		CandleTF:     *candleTF,
	}

	engine := backtest.New(cfg)
	result, err := engine.Run()
	if err != nil {
		log.Fatalf("[stratbacktest] backtest failed: %v", err)
	}

	if result.TotalCandles == 0 {
		fmt.Println("\n  No 1m candles found. Run histdata first.")
		os.Exit(0)
	}

	switch cfg.Output {
	case "json":
		backtest.PrintConsole(result)
		if err := backtest.WriteJSON(result, cfg.OutDir); err != nil {
			log.Fatalf("[stratbacktest] json export failed: %v", err)
		}
	case "csv":
		backtest.PrintConsole(result)
		if err := backtest.WriteCSV(result, cfg.OutDir); err != nil {
			log.Fatalf("[stratbacktest] csv export failed: %v", err)
		}
	default:
		backtest.PrintConsole(result)
	}
}
