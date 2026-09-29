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
	stratFlag := flag.String("strategy", "nifty50_range", "Strategy type: nifty50_range, nifty50_gamma, nifty50_sr")
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
	srRangeADX := flag.Float64("sr-range-adx", 0, "nifty50_sr: RANGE regime while 15m ADX below this (0 = default 22)")
	srTrendADX := flag.Float64("sr-trend-adx", 0, "nifty50_sr: TREND regime while 15m ADX above this (0 = default 25)")
	srConfirm := flag.Int("sr-confirm", 0, "nifty50_sr: confirmations needed of 4 (VWAP, EMA, RSI, candle) (0 = default 3)")
	srMaxTrades := flag.Int("sr-max-trades", 0, "nifty50_sr: max entries per day (0 = default 3)")
	srMaxLosses := flag.Int("sr-max-losses", 0, "nifty50_sr: stop for the day after this many losses in a row (0 = default 2)")
	srDayLoss := flag.Int64("sr-day-loss-pts", 0, "nifty50_sr: stop for the day after this many index points lost (0 = default 60)")
	srStopATR := flag.Int64("sr-stop-atr", 0, "nifty50_sr: stop beyond the level by this %% of 15m ATR (0 = default 50)")
	srStopMin := flag.Int64("sr-stop-min", 0, "nifty50_sr: minimum stop distance beyond the level, index points (0 = default 10)")
	srEntryTF := flag.Int("sr-entry-tf", 0, "nifty50_sr: entry bar minutes, 1 or 5 (0 = default 1)")
	srSetups := flag.String("sr-setups", "", "nifty50_sr: comma list of fade,retest,pullback to enable (empty = all). Strike greeks filter is live-only")
	optModel := flag.Bool("option-model", false, "Price each trade's own option (strike, weekly expiry) with Black-Scholes; P&L in option premium")
	optIV := flag.Float64("option-iv", 13, "Flat IV %% for -option-model when no India VIX history is loaded")
	premSL := flag.Int64("premium-sl", 20, "Modeled premium hard SL %% (0 = off), as the live strategy")
	cmpStrikes := flag.Bool("compare-strikes", false, "With -option-model: price the same trades at the signal strike and at the option picker's delta and expected-return picks (use -premium-sl 0 for like-for-like exits)")
	flag.Parse()

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
		StrategyCfgSR:    srOverrides(*srRangeADX, *srTrendADX, *srConfirm, *srMaxTrades, *srMaxLosses, *srDayLoss, *srStopATR, *srStopMin, *srEntryTF, *srSetups),
		StrategyCfgRange: rangeOverrides(*rangeMaxADX, *rangeBOTarget, *rangeBOEnd, *rangeEntryTF, *rangeFlag, *flagBars, *flagWidth, *htfTrend, *htfLevels),
		Option: backtest.OptionModel{
			Enabled: *optModel, IVPct: *optIV, RatePct: 6.5, PremiumSLPct: *premSL,
			SlippageBps: 50, SlippageMinPsa: 50, StrikeStep: 50,
		},
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

	if *cmpStrikes {
		vs, err := engine.CompareStrikes(result.Trades)
		if err != nil {
			log.Fatalf("[stratbacktest] strike comparison: %v", err)
		}
		fmt.Println("\n  Strike choice comparison (same trades, same entry/exit times; P&L per unit, rupees)")
		fmt.Printf("  %-30s %7s %8s %9s %12s %10s %11s\n", "choice", "trades", "refused", "win rate", "P&L ₹", "avg ₹/trd", "avg strike")
		for _, v := range vs {
			avg := 0.0
			if v.Trades > 0 {
				avg = float64(v.PnLPaise) / 100 / float64(v.Trades)
			}
			fmt.Printf("  %-30s %7d %8d %8.1f%% %12.2f %10.2f %11.0f\n",
				v.Name, v.Trades, v.Refused, v.WinRate()*100, float64(v.PnLPaise)/100, avg, v.AvgStrike)
		}
		for _, v := range vs[1:] {
			fmt.Printf("  %s: signal strike on the same %d trades ₹%.2f; on the %d refused trades ₹%.2f; refused by %v\n",
				v.Name, v.Trades, float64(v.SignalPnLOnPriced)/100, v.Refused, float64(v.SignalPnLOnRefused)/100, v.RefusedBy)
		}
	}
}

// srOverrides builds a nifty50_sr config from CLI flags (nil = defaults).
func srOverrides(rangeADX, trendADX float64, confirm, maxTrades, maxLosses int, dayLossPts, stopATR, stopMinPts int64, entryTF int, setups string) *strategy.Nifty50SRConfig {
	if rangeADX == 0 && trendADX == 0 && confirm == 0 && maxTrades == 0 && maxLosses == 0 && dayLossPts == 0 &&
		stopATR == 0 && stopMinPts == 0 && entryTF == 0 && setups == "" {
		return nil
	}
	cfg := strategy.DefaultNifty50SRConfig()
	if entryTF > 0 {
		cfg.EntryTFMinutes = entryTF
	}
	if stopATR > 0 {
		cfg.StopATRPct = stopATR
	}
	if stopMinPts > 0 {
		cfg.StopMinPts = stopMinPts * 100
	}
	if rangeADX > 0 {
		cfg.Context.RangeMaxADX = rangeADX
	}
	if trendADX > 0 {
		cfg.Context.TrendMinADX = trendADX
	}
	if confirm > 0 {
		cfg.MinConfirmations = confirm
	}
	if maxTrades > 0 {
		cfg.MaxTradesPerDay = maxTrades
	}
	if maxLosses > 0 {
		cfg.MaxConsecLosses = maxLosses
	}
	if dayLossPts > 0 {
		cfg.MaxDayLossPts = dayLossPts * 100
	}
	if setups != "" {
		on := map[string]bool{}
		for _, s := range strings.Split(setups, ",") {
			on[strings.TrimSpace(strings.ToLower(s))] = true
		}
		cfg.FadeEnabled, cfg.RetestEnabled, cfg.PullbackEnabled = on["fade"], on["retest"], on["pullback"]
	}
	return &cfg
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
