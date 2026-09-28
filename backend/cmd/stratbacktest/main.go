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
	stratFlag := flag.String("strategy", "nifty50_fno", "Strategy type: nifty50_fno, nifty50_fno_sl, nifty50_10pts, nifty50_range")
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

	rangeMaxADX := flag.Float64("range-max-adx", 0, "nifty50_range: 15m ADX cap for the range regime (0 = default)")
	rangeBOTarget := flag.Int64("range-bo-target", 0, "nifty50_range: breakout target as % of range width (0 = default)")
	rangeBOEnd := flag.String("range-bo-end", "", "nifty50_range: last breakout entry time HH:MM (empty = default)")
	rangeEntryTF := flag.Int("range-entry-tf", 0, "nifty50_range: entry timeframe minutes: 1, 2, 3 or 5 (0 = default)")
	rangeFlag := flag.String("range-flag", "", "nifty50_range: flag (trend-day consolidation breakout) on|off (empty = default)")
	flagBars := flag.Int("flag-bars", 0, "nifty50_range: 5m bars in the flag box (0 = default)")
	flagWidth := flag.Int64("flag-width-bps", 0, "nifty50_range: max flag box height in bps of price (0 = default)")
	htfTrend := flag.String("htf-trend", "", "nifty50_range: 1h trend filter on|off (empty = default)")
	htfLevels := flag.String("htf-levels", "", "nifty50_range: add 1h swing levels on|off (empty = default)")
	gRange := flag.Int64("gamma-range-bps", 0, "nifty50_gamma: max 09:15→13:45 range in bps (0 = default 100)")
	gADX := flag.Float64("gamma-adx", 0, "nifty50_gamma: 15m ADX cap at setup (0 = default 20; 99 = off)")
	gSL := flag.Int64("gamma-sl", 0, "nifty50_gamma: premium stop %% (0 = default 30)")
	gTarget := flag.Int64("gamma-target", 0, "nifty50_gamma: premium target %% (0 = default 150)")
	gOTM := flag.Int("gamma-otm", -1, "nifty50_gamma: strikes OTM (-1 = default ATM)")
	optModel := flag.Bool("option-model", false, "Price each trade's own option (strike, weekly expiry) with Black-Scholes; P&L in option premium")
	optIV := flag.Float64("option-iv", 13, "Flat IV %% for -option-model when no India VIX history is loaded")
	premSL := flag.Int64("premium-sl", 20, "Modeled premium hard SL %% (0 = off), as the live strategy")
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
		DBPath:           *dbPath,
		Exchange:         *exchange,
		Token:            *token,
		Qty:              *qty,
		From:             *from,
		To:               *to,
		Output:           *output,
		OutDir:           *outDir,
		StrategyType:     *stratFlag,
		StrategyCfgGamma: gammaOverrides(*gRange, *gADX, *gSL, *gTarget, *gOTM),
		StrategyCfgRange: rangeOverrides(*rangeMaxADX, *rangeBOTarget, *rangeBOEnd, *rangeEntryTF, *rangeFlag, *flagBars, *flagWidth, *htfTrend, *htfLevels),
		Option: backtest.OptionModel{
			Enabled: *optModel, IVPct: *optIV, RatePct: 6.5, PremiumSLPct: *premSL,
			SlippageBps: 50, SlippageMinPsa: 50, StrikeStep: 50,
		},
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

// rangeOverrides builds a nifty50_range config from CLI flags (nil = defaults).
func rangeOverrides(maxADX float64, boTarget int64, boEnd string, entryTF int, flagOn string, flagBars int, flagWidth int64, htfTrend, htfLevels string) *strategy.Nifty50RangeConfig {
	if maxADX == 0 && boTarget == 0 && boEnd == "" && entryTF == 0 && flagOn == "" && flagBars == 0 && flagWidth == 0 && htfTrend == "" && htfLevels == "" {
		return nil
	}
	cfg := strategy.DefaultNifty50RangeConfig()
	if maxADX > 0 {
		cfg.Range.MaxADX = maxADX
	}
	if boTarget > 0 {
		cfg.BreakoutTargetPct = boTarget
	}
	if boEnd != "" {
		var h, m int
		if _, err := fmt.Sscanf(boEnd, "%d:%d", &h, &m); err == nil {
			cfg.SetBreakoutWindowEnd(h, m)
		}
	}
	if entryTF == 1 || entryTF == 2 || entryTF == 3 || entryTF == 5 {
		cfg.EntryTFMinutes = entryTF
	}
	switch flagOn {
	case "on":
		cfg.FlagEnabled = true
	case "off":
		cfg.FlagEnabled = false
	}
	if htfTrend != "" {
		cfg.HTFTrendFilter = htfTrend == "on"
	}
	if htfLevels != "" {
		cfg.Range.HTFLevels = htfLevels == "on"
	}
	if flagBars > 0 {
		cfg.FlagBars = flagBars
	}
	if flagWidth > 0 {
		cfg.FlagMaxWidthBps = flagWidth
	}
	return &cfg
}

// gammaOverrides builds a nifty50_gamma config from CLI flags (nil = defaults).
func gammaOverrides(rangeBps int64, adx float64, sl, target int64, otm int) *strategy.Nifty50GammaConfig {
	if rangeBps == 0 && adx == 0 && sl == 0 && target == 0 && otm < 0 {
		return nil
	}
	cfg := strategy.DefaultNifty50GammaConfig()
	if rangeBps > 0 {
		cfg.MaxRangeBps = rangeBps
	}
	if adx > 0 {
		cfg.MaxADX = adx
	}
	if sl > 0 {
		cfg.PremiumSLPct = sl
	}
	if target > 0 {
		cfg.PremiumTargetPct = target
	}
	if otm >= 0 {
		cfg.OTMSteps = otm
	}
	return &cfg
}
