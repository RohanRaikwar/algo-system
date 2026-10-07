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
	"sort"
	"strings"

	"trading-systemv1/internal/backtest"
	"trading-systemv1/internal/exitpolicy"
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
	srNoProgMin := flag.Int("sr-noprog-min", 0, "nifty50_sr: exit when no 1m close reached -sr-noprog-r of R within this many minutes (0 = off)")
	srNoProgR := flag.Int64("sr-noprog-r", 50, "nifty50_sr: progress needed by -sr-noprog-min, %% of R")
	srBreakeven := flag.Int64("sr-breakeven-r", 0, "nifty50_sr: move the stop to entry at this %% of R (0 = default 50)")
	srHardSL := flag.Int64("sr-hard-sl", 0, "nifty50_sr: premium hard SL %% below entry (0 = default 20)")
	srReentryADX := flag.Float64("sr-reentry-adx", 0, "nifty50_sr: same-side re-entry needs 15m ADX ≥ this or above the last entry's (0 = off)")
	srDayExtreme := flag.Int64("sr-day-extreme-pct", -1, "nifty50_sr: no CALL in the top N%% of today's range (PUT: bottom) after -sr-day-run pts; 0 = off (-1 = default)")
	srDayRun := flag.Int64("sr-day-run", -1, "nifty50_sr: day run in index points needed before -sr-day-extreme-pct applies (-1 = default)")
	srDayPuts := flag.String("sr-day-extreme-puts", "", "nifty50_sr: also refuse PUTs at the bottom of the day's range on|off (empty = default off)")
	srDayFrom := flag.String("sr-day-extreme-from", "", "nifty50_sr: day-extreme check starts at HH:MM IST (empty = default)")
	srRetestTol := flag.Int64("sr-retest-tol", -1, "nifty50_sr: retest bar must come within this many index points of the level (0 = touch tolerance, -1 = default)")
	srBoxBars := flag.Int("sr-box-bars", -1, "nifty50_sr: sideways box over this many 5m bars, 0 = off (-1 = default)")
	srBoxATR := flag.Int64("sr-box-atr", -1, "nifty50_sr: box ≤ this %% of 15m ATR counts as sideways (-1 = default)")
	srGate := flag.String("sr-gate", "", "nifty50_sr: day-extreme and sideways checks warn|block (empty = default warn)")
	srBoxFade := flag.String("sr-box-fade", "", "nifty50_sr: sideways box also refuses fades on|off (empty = default)")
	srSetups := flag.String("sr-setups", "", "nifty50_sr: comma list of fade,retest,pullback to enable (empty = all). Strike greeks filter is live-only")
	optModel := flag.Bool("option-model", false, "Price each trade's own option (strike, weekly expiry) with Black-Scholes; P&L in option premium")
	optIV := flag.Float64("option-iv", 13, "Flat IV %% for -option-model when no India VIX history is loaded")
	premSL := flag.Int64("premium-sl", 20, "Modeled premium hard SL %% (0 = off), as the live strategy")
	minPrem := flag.Float64("min-premium", 0, "With -option-model: skip entries whose option premium at the signal strike is below this many rupees (0 = off)")
	expMinDTE := flag.Int("expiry-min-dte", 0, "With -option-model: buy the next weekly expiry when the nearest is fewer than this many days away (live SR: 2)")
	minDTE := flag.Int("min-dte", 0, "With -option-model: skip entries whose expiry is fewer than this many calendar days away (0 = off)")
	cmpStrikes := flag.Bool("compare-strikes", false, "With -option-model: price the same trades at the signal strike and at the option picker's delta and expected-return picks (use -premium-sl 0 for like-for-like exits)")
	thetaExit := flag.String("theta-exit", "off", "With -option-model: exit policy theta rule off|shadow|act (shadow reports what it would have done)")
	thetaFile := flag.String("theta-file", "config/exitpolicy.json", "Exit policy file; the strategy's entry (or defaults) supplies the theta settings")
	thetaFlatMin := flag.Int("theta-max-flat-min", 0, "Theta rule: exit a flat trade after this many minutes (0 = from -theta-file)")
	thetaDecayPct := flag.Int64("theta-decay-pct", 0, "Theta rule: exit once decay reaches this %% of the expected gain (0 = from -theta-file)")
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
		StrategyCfgSR:    srOverrides(*srRangeADX, *srTrendADX, *srConfirm, *srMaxTrades, *srMaxLosses, *srDayLoss, *srStopATR, *srStopMin, *srEntryTF, *srSetups, *srNoProgMin, *srNoProgR, *srBreakeven, *srHardSL, *srReentryADX, *srDayExtreme, *srDayRun, *srDayPuts, *srDayFrom, *srRetestTol, *srBoxBars, *srBoxATR, *srBoxFade, *srGate),
		StrategyCfgRange: rangeOverrides(*rangeMaxADX, *rangeBOTarget, *rangeBOEnd, *rangeEntryTF, *rangeFlag, *flagBars, *flagWidth, *htfTrend, *htfLevels),
		Option: backtest.OptionModel{
			Enabled: *optModel, IVPct: *optIV, RatePct: 6.5, PremiumSLPct: *premSL,
			SlippageBps: 50, SlippageMinPsa: 50, StrikeStep: 50,
			MinEntryPremium: int64(*minPrem * 100), MinEntryDTE: *minDTE, ExpiryMinDTE: *expMinDTE,
		},
		CallFNOToken: *callToken,
		PutFNOToken:  *putToken,
		FNOExchange:  *fnoExchange,
		CandleTF:     *candleTF,
		ExitPolicy:   thetaOverrides(*thetaExit, *thetaFile, *stratFlag, *thetaFlatMin, *thetaDecayPct),
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

	printWarnTally(result.Trades)
	printShadowExits(result.Trades)

	if n := engine.Skipped(); n > 0 {
		fmt.Printf("\n  Entry gate skipped %d entries (min premium ₹%.0f, min DTE %d)\n", n, *minPrem, *minDTE)
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
func srOverrides(rangeADX, trendADX float64, confirm, maxTrades, maxLosses int, dayLossPts, stopATR, stopMinPts int64, entryTF int, setups string, noProgMin int, noProgR, breakevenR, hardSL int64, reentryADX float64, dayExtremePct, dayRunPts int64, dayPuts, dayFrom string, retestTol int64, boxBars int, boxATR int64, boxFade, gate string) *strategy.Nifty50SRConfig {
	if rangeADX == 0 && trendADX == 0 && confirm == 0 && maxTrades == 0 && maxLosses == 0 && dayLossPts == 0 &&
		stopATR == 0 && stopMinPts == 0 && entryTF == 0 && setups == "" && noProgMin == 0 && breakevenR == 0 &&
		hardSL == 0 && reentryADX == 0 && dayExtremePct < 0 && dayRunPts < 0 && dayPuts == "" && dayFrom == "" && retestTol < 0 && boxBars < 0 && boxATR < 0 && boxFade == "" && gate == "" {
		return nil
	}
	cfg := strategy.DefaultNifty50SRConfig()
	if dayExtremePct >= 0 {
		cfg.DayExtremePct = dayExtremePct
	}
	if dayRunPts >= 0 {
		cfg.DayRunPts = dayRunPts * 100
	}
	if dayPuts != "" {
		cfg.DayExtremePuts = dayPuts == "on"
	}
	if dayFrom != "" {
		var h, m int
		if _, err := fmt.Sscanf(dayFrom, "%d:%d", &h, &m); err == nil {
			cfg.DayExtremeFromMin = h*60 + m
		}
	}
	if retestTol >= 0 {
		cfg.RetestTolPts = retestTol * 100
	}
	if boxBars >= 0 {
		cfg.BoxBars = boxBars
	}
	if boxATR >= 0 {
		cfg.BoxATRPct = boxATR
	}
	if boxFade != "" {
		cfg.BoxFadeToo = boxFade == "on"
	}
	if gate != "" {
		cfg.GateMode = gate
	}
	if hardSL > 0 {
		cfg.FNOHardSLPct = hardSL
	}
	if reentryADX > 0 {
		cfg.ReentryMinADX = reentryADX
	}
	if breakevenR > 0 {
		cfg.BreakevenAtR = breakevenR
	}
	if noProgMin > 0 {
		cfg.NoProgressMin, cfg.NoProgressRPct = noProgMin, noProgR
	}
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

// printWarnTally splits trades by the "warn=" flags in their entry reason
// (NIFTY50_SR warn mode), so each warning's trades can be judged.
func printWarnTally(trades []backtest.Trade) {
	type tally struct {
		n, wins int
		pnl     int64
	}
	by := map[string]*tally{}
	add := func(k string, t backtest.Trade) {
		if by[k] == nil {
			by[k] = &tally{}
		}
		p := t.PnLPaise()
		by[k].n++
		by[k].pnl += p
		if p > 0 {
			by[k].wins++
		}
	}
	warned := false
	for _, t := range trades {
		i := strings.Index(t.EntryReason, " warn=")
		if i < 0 {
			add("(none)", t)
			continue
		}
		warned = true
		add("(any warning)", t)
		for _, w := range strings.Split(strings.Fields(t.EntryReason[i+6:])[0], ",") {
			add(w, t)
		}
	}
	if !warned {
		return
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("\n  Entry warnings (trades taken with each flag):")
	fmt.Printf("  %-16s %6s %8s %12s\n", "warning", "trades", "win%", "P&L ₹")
	for _, k := range keys {
		v := by[k]
		fmt.Printf("  %-16s %6d %7.1f%% %12.2f\n", k, v.n, float64(v.wins)*100/float64(v.n), float64(v.pnl)/100)
	}
}

// thetaOverrides builds the backtest's exit policy: the strategy's entry in
// file (defaults when absent), mode from -theta-exit, then flag overrides.
func thetaOverrides(mode, file, stratType string, flatMin int, decayPct int64) *exitpolicy.StrategyConfig {
	if mode == "" || mode == string(exitpolicy.ModeOff) {
		return nil
	}
	if mode != string(exitpolicy.ModeShadow) && mode != string(exitpolicy.ModeAct) {
		log.Fatalf("[stratbacktest] -theta-exit %q: want off, shadow or act", mode)
	}
	cfg, err := exitpolicy.LoadConfig(file)
	if err != nil {
		log.Printf("[stratbacktest] exit policy file: %v — using defaults", err)
		cfg = exitpolicy.DefaultConfig()
	}
	sc, ok := cfg.Strategies[strings.ToUpper(stratType)]
	if !ok {
		sc = exitpolicy.StrategyConfig{Theta: exitpolicy.DefaultThetaConfig()}
	}
	sc.Mode = exitpolicy.Mode(mode)
	if flatMin > 0 {
		sc.Theta.MaxFlatMin = flatMin
	}
	if decayPct > 0 {
		sc.Theta.DecayPctOfGain = decayPct
	}
	log.Printf("[stratbacktest] theta exit %s: %+v", sc.Mode, sc.Theta)
	return &sc
}

// printShadowExits compares, on trades where the shadow exit policy would
// have exited, its modeled sell price against the actual exit.
func printShadowExits(trades []backtest.Trade) {
	var n, better int
	var actual, shadow int64
	for _, t := range trades {
		if t.ShadowExit == "" || t.FNOEntryPrice <= 0 {
			continue
		}
		n++
		actual += t.FNOExitPrice - t.FNOEntryPrice
		shadow += t.ShadowExitFNO - t.FNOEntryPrice
		if t.ShadowExitFNO > t.FNOExitPrice {
			better++
		}
	}
	if n == 0 {
		return
	}
	fmt.Printf("\n  Theta exit (shadow): would have exited %d of %d trades; better on %d\n", n, len(trades), better)
	fmt.Printf("  P&L on those trades per unit: actual ₹%.2f, theta exit ₹%.2f (Δ ₹%.2f)\n",
		float64(actual)/100, float64(shadow)/100, float64(shadow-actual)/100)
}
