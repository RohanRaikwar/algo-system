// Package backtest provides a reusable backtesting engine that replays
// historical candle data through the EMA1MCombined strategy, simulates
// trades (including tick-level stoploss via candle extremes), and
// computes comprehensive trade analytics.
package backtest

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// ── IST timezone ──────────────────────────────────────────────────────

var istLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		// Fallback: IST = UTC+5:30
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	return loc
}()

// ── Config ────────────────────────────────────────────────────────────

// Config holds all parameters for a backtest run.
type Config struct {
	DBPath       string                    // path to historical SQLite DB
	Exchange     string                    // e.g. "NSE"
	Token        string                    // e.g. "99926000"
	Qty          int64                     // trade quantity for P&L
	From         string                    // optional start date "YYYY-MM-DD" (empty = all)
	To           string                    // optional end date "YYYY-MM-DD" (empty = all)
	Output       string                    // "console", "json", "csv"
	OutDir       string                    // output directory for file exports
	StrategyType string                    // "nifty50_fno" (default), "nifty50_fno_sl", "ema3_hybrid"
	StrategyCfg  strategy.Nifty50FnOConfig // strategy-level config (optional, uses defaults if zero)
	StrategyCfg10Pts *strategy.Nifty5010PtsConfig // optional override for nifty50_10pts
	CandleTF     int                       // candle timeframe in minutes (1=1m, 2=2m, 5=5m; default=1)

	// ── FNO option price tracking ──
	CallFNOToken string // NFO token for CALL option (e.g. "57709")
	PutFNOToken  string // NFO token for PUT option (e.g. "57710")
	FNOExchange  string // exchange for FNO tokens (default: "NFO")
}

// ── Trade ─────────────────────────────────────────────────────────────

// Trade records one simulated round-trip trade.
type Trade struct {
	ID          int                   `json:"id"`
	Side        strategy.PositionSide `json:"side"`
	EntryTime   time.Time             `json:"entry_time"`
	ExitTime    time.Time             `json:"exit_time"`
	EntryPrice  int64                 `json:"entry_price"` // index price in paise
	ExitPrice   int64                 `json:"exit_price"`  // index price in paise
	EntryReason string                `json:"entry_reason"`
	ExitReason  string                `json:"exit_reason"`

	// FNO option contract prices (paise). Zero if FNO data unavailable.
	FNOEntryPrice int64 `json:"fno_entry_price,omitempty"`
	FNOExitPrice  int64 `json:"fno_exit_price,omitempty"`
}

// PnLPaise returns the trade P&L in paise.
// When FNO option prices are available, P&L is computed from option prices
// (buy low / sell high for both CALL and PUT options).
// Otherwise falls back to index-price based P&L.
func (t Trade) PnLPaise() int64 {
	// Use FNO prices if available
	if t.FNOEntryPrice > 0 && t.FNOExitPrice > 0 {
		switch t.Side {
		case strategy.SideCall:
			// CALL option: buy CE at entry, sell CE at exit
			return t.FNOExitPrice - t.FNOEntryPrice
		case strategy.SidePut:
			// PUT option: buy PE at entry, sell PE at exit
			return t.FNOExitPrice - t.FNOEntryPrice
		default:
			return 0
		}
	}
	// Fallback: index-based P&L
	switch t.Side {
	case strategy.SideCall:
		return t.ExitPrice - t.EntryPrice
	case strategy.SidePut:
		return t.EntryPrice - t.ExitPrice
	default:
		return 0
	}
}

// PnLRupees returns the trade P&L in rupees.
func (t Trade) PnLRupees() float64 {
	return float64(t.PnLPaise()) / 100.0
}

// Duration returns how long the trade was held.
func (t Trade) Duration() time.Duration {
	return t.ExitTime.Sub(t.EntryTime)
}

// ── DaySummary ────────────────────────────────────────────────────────

// DaySummary holds per-trading-day metrics.
type DaySummary struct {
	Date        string  `json:"date"` // "YYYY-MM-DD"
	Trades      int     `json:"trades"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	PnL         float64 `json:"pnl"`            // rupees
	CumPnL      float64 `json:"cumulative_pnl"` // rupees
	MaxDrawdown float64 `json:"max_drawdown"`   // rupees (negative)
}

// ── Result ────────────────────────────────────────────────────────────

// Result holds the complete output of one backtest run.
type Result struct {
	Config       Config       `json:"config"`
	TotalCandles int          `json:"total_candles"`
	Trades       []Trade      `json:"trades"`
	DaySummaries []DaySummary `json:"day_summaries"`
	Metrics      Metrics      `json:"metrics"`
	EquityCurve  []float64    `json:"equity_curve"` // cumulative P&L after each trade
}

// Metrics holds computed analytics for the backtest.
type Metrics struct {
	TotalTrades  int     `json:"total_trades"`
	CallTrades   int     `json:"call_trades"`
	PutTrades    int     `json:"put_trades"`
	Wins         int     `json:"wins"`
	Losses       int     `json:"losses"`
	WinRate      float64 `json:"win_rate"`      // 0–100
	NetPnL       float64 `json:"net_pnl"`       // rupees
	GrossProfit  float64 `json:"gross_profit"`  // rupees
	GrossLoss    float64 `json:"gross_loss"`    // rupees (negative)
	ProfitFactor float64 `json:"profit_factor"` // gross_profit / |gross_loss|
	BestTrade    float64 `json:"best_trade"`    // rupees
	WorstTrade   float64 `json:"worst_trade"`   // rupees (negative)
	AvgWin       float64 `json:"avg_win"`       // rupees
	AvgLoss      float64 `json:"avg_loss"`      // rupees (negative)
	MaxDrawdown  float64 `json:"max_drawdown"`  // rupees (negative)
	SharpeRatio  float64 `json:"sharpe_ratio"`  // annualized
	AvgDuration  string  `json:"avg_duration"`  // human-readable
	TotalDays    int     `json:"total_days"`
}

// ── Engine ─────────────────────────────────────────────────────────────

// Engine orchestrates a full backtest run.
type Engine struct {
	cfg Config

	// FNO candle price maps: unix timestamp → close price (paise)
	callPrices map[int64]int64
	putPrices  map[int64]int64
}

type candleSource string

const (
	candleSourceHistorical candleSource = "historical_candles"
	candleSourceTF         candleSource = "candles_tf"
)

// New creates a new backtest engine with the given config.
func New(cfg Config) *Engine {
	return &Engine{cfg: cfg}
}

// Run executes the backtest: load candles → replay strategy → compute metrics.
func (e *Engine) Run() (*Result, error) {
	// ── Open DB ──
	db, err := sql.Open("sqlite3", e.cfg.DBPath+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// ── Load index candles ──
	candles, err := e.loadCandles(db)
	if err != nil {
		return nil, fmt.Errorf("load candles: %w", err)
	}
	if len(candles) == 0 {
		return &Result{Config: e.cfg}, nil
	}

	// ── Load FNO option candles (if configured) ──
	if e.cfg.CallFNOToken != "" || e.cfg.PutFNOToken != "" {
		if err := e.loadFNOCandles(db); err != nil {
			log.Printf("[backtest] ⚠️  FNO candle load error (continuing without FNO prices): %v", err)
		}
	}

	// ── Replay through strategy ──
	trades := e.replayStrategy(candles)

	// ── Compute analytics ──
	metrics := computeMetrics(trades, e.cfg.Qty)
	daySummaries := computeDaySummaries(trades, e.cfg.Qty)
	equityCurve := computeEquityCurve(trades, e.cfg.Qty)

	return &Result{
		Config:       e.cfg,
		TotalCandles: len(candles),
		Trades:       trades,
		DaySummaries: daySummaries,
		Metrics:      metrics,
		EquityCurve:  equityCurve,
	}, nil
}

// LoadCandlesForSweep loads candles into memory once for parallel sweep execution.
// It bypasses the FNO option price loading since sweeps only need index data for triggers.
func (e *Engine) LoadCandlesForSweep() ([]model.TFCandle, error) {
	db, err := sql.Open("sqlite3", e.cfg.DBPath+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	candles, err := e.loadCandles(db)
	if err != nil {
		return nil, fmt.Errorf("load candles: %w", err)
	}
	return candles, nil
}

// RunWithCandles executes a backtest using pre-loaded candles (skips DB access).
// Ideal for massive parallel sweep optimization.
func (e *Engine) RunWithCandles(candles []model.TFCandle) (Result, error) {
	if len(candles) == 0 {
		return Result{Config: e.cfg}, nil
	}

	// ── Replay through strategy ──
	trades := e.replayStrategy(candles)

	// ── Compute analytics ──
	metrics := computeMetrics(trades, e.cfg.Qty)
	daySummaries := computeDaySummaries(trades, e.cfg.Qty)
	equityCurve := computeEquityCurve(trades, e.cfg.Qty)

	return Result{
		Config:       e.cfg,
		TotalCandles: len(candles),
		Trades:       trades,
		DaySummaries: daySummaries,
		Metrics:      metrics,
		EquityCurve:  equityCurve,
	}, nil
}

// ── Candle Loading ────────────────────────────────────────────────────

func (e *Engine) loadCandles(db *sql.DB) ([]model.TFCandle, error) {
	source, err := e.detectCandleSource(db)
	if err != nil {
		return nil, err
	}

	var raw []model.TFCandle
	switch source {
	case candleSourceHistorical:
		raw, err = e.loadHistoricalCandles(db)
	case candleSourceTF:
		raw, err = e.loadTFCandles(db)
	default:
		err = fmt.Errorf("unsupported candle source %q", source)
	}
	if err != nil {
		return nil, err
	}

	log.Printf("[backtest] Using %s for backtest candle data", source)

	tfMin := e.cfg.CandleTF
	stratLower := strings.ToLower(strings.TrimSpace(e.cfg.StrategyType))
	if stratLower == "nifty50_fno_sl2" {
		if tfMin != 2 {
			log.Printf("[backtest] Forcing 2m candles for %s (requested tf=%dm)", e.cfg.StrategyType, tfMin)
		}
		tfMin = 2
	}
	if tfMin <= 1 {
		return raw, nil
	}

	outTFSec := tfMin * 60 // always compute correct TF in seconds
	return aggregateCandles(raw, tfMin, outTFSec), nil
}

func (e *Engine) loadHistoricalCandles(db *sql.DB) ([]model.TFCandle, error) {
	query := `
		SELECT exchange, token, tf, ts, open, high, low, close, volume
		FROM historical_candles
		WHERE exchange = ? AND token = ? AND tf = 60`
	args := []any{e.cfg.Exchange, e.cfg.Token}

	if e.cfg.From != "" {
		fromTime, err := time.Parse("2006-01-02", e.cfg.From)
		if err != nil {
			return nil, fmt.Errorf("invalid --from date: %w", err)
		}
		query += " AND ts >= ?"
		args = append(args, fromTime.Unix())
	}
	if e.cfg.To != "" {
		toTime, err := time.Parse("2006-01-02", e.cfg.To)
		if err != nil {
			return nil, fmt.Errorf("invalid --to date: %w", err)
		}
		// End of day
		toTime = toTime.Add(24*time.Hour - time.Second)
		query += " AND ts <= ?"
		args = append(args, toTime.Unix())
	}

	query += " ORDER BY ts ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candles []model.TFCandle
	for rows.Next() {
		var (
			ex, tok    string
			tfSec      int
			tsUnix     int64
			o, h, l, c float64
			volume     int64
		)
		if err := rows.Scan(&ex, &tok, &tfSec, &tsUnix, &o, &h, &l, &c, &volume); err != nil {
			return nil, err
		}

		candles = append(candles, model.TFCandle{
			Token:    tok,
			Exchange: ex,
			TF:       tfSec,
			TS:       time.Unix(tsUnix, 0).UTC(),
			Open:     rupeeToPaise(o),
			High:     rupeeToPaise(h),
			Low:      rupeeToPaise(l),
			Close:    rupeeToPaise(c),
			Volume:   volume,
			Count:    1,
			Forming:  false,
		})
	}
	return candles, rows.Err()
}

// aggregateCandles merges N consecutive 1-minute candles into N-minute OHLCV candles.
func aggregateCandles(candles1m []model.TFCandle, nMinutes, outTFSec int) []model.TFCandle {
	if len(candles1m) == 0 || nMinutes <= 1 {
		return candles1m
	}

	var result []model.TFCandle
	var bucket []model.TFCandle

	flush := func() {
		if len(bucket) == 0 {
			return
		}
		agg := bucket[0]
		for _, c := range bucket[1:] {
			if c.High > agg.High {
				agg.High = c.High
			}
			if c.Low < agg.Low {
				agg.Low = c.Low
			}
			agg.Close = c.Close
			agg.Volume += c.Volume
		}
		agg.TS = bucket[len(bucket)-1].TS // use last candle's timestamp
		agg.TF = outTFSec
		result = append(result, agg)
		bucket = bucket[:0]
	}

	for _, c := range candles1m {
		bucket = append(bucket, c)
		if len(bucket) >= nMinutes {
			flush()
		}
	}
	flush() // remaining partial bucket

	log.Printf("[backtest] Aggregated %d 1m candles → %d %dm candles (tf=%ds)", len(candles1m), len(result), nMinutes, outTFSec)
	return result
}

func rupeeToPaise(r float64) int64 {
	return int64(math.Round(r * 100))
}

func (e *Engine) loadTFCandles(db *sql.DB) ([]model.TFCandle, error) {
	query := `
		SELECT exchange, token, tf, ts, open, high, low, close, COALESCE(volume, 0)
		FROM candles_tf
		WHERE exchange = ? AND token = ? AND tf = 60`
	args := []any{e.cfg.Exchange, e.cfg.Token}

	if e.cfg.From != "" {
		fromTime, err := time.Parse("2006-01-02", e.cfg.From)
		if err != nil {
			return nil, fmt.Errorf("invalid --from date: %w", err)
		}
		query += " AND ts >= ?"
		args = append(args, fromTime.Unix())
	}
	if e.cfg.To != "" {
		toTime, err := time.Parse("2006-01-02", e.cfg.To)
		if err != nil {
			return nil, fmt.Errorf("invalid --to date: %w", err)
		}
		toTime = toTime.Add(24*time.Hour - time.Second)
		query += " AND ts <= ?"
		args = append(args, toTime.Unix())
	}

	query += " ORDER BY ts ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candles []model.TFCandle
	for rows.Next() {
		var (
			ex, tok         string
			tfSec           int
			tsUnix          int64
			o, h, l, c, vol int64
		)
		if err := rows.Scan(&ex, &tok, &tfSec, &tsUnix, &o, &h, &l, &c, &vol); err != nil {
			return nil, err
		}

		candles = append(candles, model.TFCandle{
			Token:    tok,
			Exchange: ex,
			TF:       tfSec,
			TS:       time.Unix(tsUnix, 0).UTC(),
			Open:     o,
			High:     h,
			Low:      l,
			Close:    c,
			Volume:   vol,
			Count:    1,
			Forming:  false,
		})
	}
	return candles, rows.Err()
}

// loadFNOCandles loads CE and PE option candle data into timestamp→price maps.
func (e *Engine) loadFNOCandles(db *sql.DB) error {
	source, err := e.detectCandleSource(db)
	if err != nil {
		return err
	}

	fnoExchange := e.cfg.FNOExchange
	if fnoExchange == "" {
		fnoExchange = "NFO"
	}

	loadPrices := func(token string) (map[int64]int64, error) {
		if token == "" {
			return nil, nil
		}
		var query string
		args := []any{fnoExchange, token}
		switch source {
		case candleSourceHistorical:
			query = `SELECT ts, close FROM historical_candles
				WHERE exchange = ? AND token = ? AND tf = 60`
		case candleSourceTF:
			query = `SELECT ts, close FROM candles_tf
				WHERE exchange = ? AND token = ? AND tf = 60`
		default:
			return nil, fmt.Errorf("unsupported candle source %q", source)
		}

		if e.cfg.From != "" {
			fromTime, err := time.Parse("2006-01-02", e.cfg.From)
			if err != nil {
				return nil, fmt.Errorf("invalid --from date: %w", err)
			}
			query += " AND ts >= ?"
			args = append(args, fromTime.Unix())
		}
		if e.cfg.To != "" {
			toTime, err := time.Parse("2006-01-02", e.cfg.To)
			if err != nil {
				return nil, fmt.Errorf("invalid --to date: %w", err)
			}
			toTime = toTime.Add(24*time.Hour - time.Second)
			query += " AND ts <= ?"
			args = append(args, toTime.Unix())
		}

		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		prices := make(map[int64]int64)
		switch source {
		case candleSourceHistorical:
			for rows.Next() {
				var tsUnix int64
				var closePrice float64
				if err := rows.Scan(&tsUnix, &closePrice); err != nil {
					return nil, err
				}
				prices[tsUnix] = rupeeToPaise(closePrice)
			}
		case candleSourceTF:
			for rows.Next() {
				var tsUnix int64
				var closePrice int64
				if err := rows.Scan(&tsUnix, &closePrice); err != nil {
					return nil, err
				}
				prices[tsUnix] = closePrice
			}
		}
		return prices, rows.Err()
	}

	e.callPrices, err = loadPrices(e.cfg.CallFNOToken)
	if err != nil {
		return fmt.Errorf("load CALL FNO candles (token=%s): %w", e.cfg.CallFNOToken, err)
	}
	e.putPrices, err = loadPrices(e.cfg.PutFNOToken)
	if err != nil {
		return fmt.Errorf("load PUT FNO candles (token=%s): %w", e.cfg.PutFNOToken, err)
	}

	log.Printf("[backtest] Loaded FNO candles: CALL=%d prices (token=%s), PUT=%d prices (token=%s)",
		len(e.callPrices), e.cfg.CallFNOToken, len(e.putPrices), e.cfg.PutFNOToken)
	return nil
}

func (e *Engine) detectCandleSource(db *sql.DB) (candleSource, error) {
	if ok, err := tableExists(db, string(candleSourceHistorical)); err != nil {
		return "", err
	} else if ok {
		return candleSourceHistorical, nil
	}
	if ok, err := tableExists(db, string(candleSourceTF)); err != nil {
		return "", err
	} else if ok {
		return candleSourceTF, nil
	}
	return "", fmt.Errorf("no supported candle table found (expected historical_candles or candles_tf)")
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type IN ('table', 'view') AND name = ? LIMIT 1`,
		name,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found == name, nil
}

// lookupFNOPrice returns the FNO option close price for the given side and
// candle timestamp. Returns 0 if FNO data is not available.
func (e *Engine) lookupFNOPrice(side strategy.PositionSide, tsUnix int64) int64 {
	switch side {
	case strategy.SideCall:
		if e.callPrices != nil {
			return e.callPrices[tsUnix]
		}
	case strategy.SidePut:
		if e.putPrices != nil {
			return e.putPrices[tsUnix]
		}
	}
	return 0
}

// ── Strategy Replay ───────────────────────────────────────────────────

// backtestStrategy is implemented by both EMA1MCombined and EMAMTFCombined.
type backtestStrategy interface {
	Name() string
	OnTFCandle(candle model.TFCandle) *strategy.Signal
	OnTick(tick model.Tick) *strategy.Signal
	ResetPositions()
}

func (e *Engine) replayStrategy(candles []model.TFCandle) []Trade {
	var strat backtestStrategy

	switch strings.ToLower(strings.TrimSpace(e.cfg.StrategyType)) {
	case "nifty50_fno_sl":
		slCfg := strategy.DefaultNifty50FnOSLConfig()
		src := e.cfg.StrategyCfg
		if src.EMA6Period > 0 {
			slCfg.EMA6Period = src.EMA6Period
		}
		if src.BufferSize > 0 {
			slCfg.BufferSize = src.BufferSize
		}
		if src.CooldownCandles >= 0 {
			slCfg.CooldownCandles = src.CooldownCandles
		}
		slCfg.MinReEntrySeparationPct = src.MinReEntrySeparationPct
		slCfg.SidewaysEnabled = src.SidewaysEnabled
		if src.MaxChopCrosses > 0 {
			slCfg.MaxChopCrosses = src.MaxChopCrosses
		}
		if src.MA21FlatPct > 0 {
			slCfg.MA21FlatPct = src.MA21FlatPct
		}
		if src.NarrowRangePct > 0 {
			slCfg.NarrowRangePct = src.NarrowRangePct
		}
		// Explicitly disable momentum bypass for NIFTY50_FNO_SL.
		slCfg.MomentumBypassPct = 0
		if src.SkipFirstMinutes >= 0 {
			slCfg.SkipFirstMinutes = src.SkipFirstMinutes
		}
		if src.SkipLastMinutes >= 0 {
			slCfg.SkipLastMinutes = src.SkipLastMinutes
		}
		slCfg.LunchFilterEnabled = src.LunchFilterEnabled
		if src.LunchStartHHMM != "" {
			slCfg.LunchStartHHMM = src.LunchStartHHMM
		}
		if src.LunchEndHHMM != "" {
			slCfg.LunchEndHHMM = src.LunchEndHHMM
		}
		slCfg.TrendEnabled = src.TrendEnabled
		if src.TrendLookback > 0 {
			slCfg.TrendLookback = src.TrendLookback
		}
		if src.TrendSlopePct > 0 {
			slCfg.TrendSlopePct = src.TrendSlopePct
		}
		if src.HardSLPct > 0 {
			slCfg.IndexHardSLPct = src.HardSLPct
		}
		if src.TrailSLPct > 0 {
			slCfg.IndexTrailSLPct = src.TrailSLPct
		}
		if src.TrailStartPct > 0 {
			slCfg.IndexTrailStartPct = src.TrailStartPct
		}
		slCfg.ReEntryAfterSL = src.ReEntryAfterSL
		// Bind the SL strategy to whichever index stream is under test.
		slCfg.IndexToken = e.cfg.Exchange + ":" + e.cfg.Token

		strat = strategy.NewNifty50FnOSLWithConfig(e.cfg.Qty, slCfg)
		log.Printf("[backtest] Using NIFTY50_FNO_SL strategy (momentum=off, sep=%.3f%%, idxHardSL=%.2f%%, idxTrailSL=%.2f%%)",
			slCfg.MinReEntrySeparationPct, slCfg.IndexHardSLPct, slCfg.IndexTrailSLPct)
	case "nifty50_fno_sl2":
		sl2Cfg := strategy.DefaultNifty50FnOSL2Config()
		src := e.cfg.StrategyCfg
		if src.MinReEntrySeparationPct > 0 {
			sl2Cfg.MinReEntrySeparationPct = src.MinReEntrySeparationPct
		}
		if src.HardSLPct > 0 {
			sl2Cfg.IndexHardSLPct = src.HardSLPct
		}
		if src.TrailSLPct > 0 {
			sl2Cfg.IndexTrailSLPct = src.TrailSLPct
		}
		if src.TrailStartPct > 0 {
			sl2Cfg.IndexTrailStartPct = src.TrailStartPct
		}
		sl2Cfg.ReEntryAfterSL = src.ReEntryAfterSL
		sl2Cfg.IndexToken = e.cfg.Exchange + ":" + e.cfg.Token

		strat = strategy.NewNifty50FnOSL2WithConfig(e.cfg.Qty, sl2Cfg)
		log.Printf("[backtest] Using NIFTY50_FNO_SL2 strategy (EMA2/EMA6/SMA15, sep=%.3f%%, trailSL=%.2f%%)",
			sl2Cfg.MinReEntrySeparationPct, sl2Cfg.IndexTrailSLPct)
	case "nifty50_10pts":
		// Pull the baseline 6-month optimized configuration
		optCfg := strategy.DefaultNifty5010PtsConfig()
		if e.cfg.StrategyCfg10Pts != nil {
			optCfg = *e.cfg.StrategyCfg10Pts
		}
		
		strat = strategy.NewNifty5010PtsWithConfig(e.cfg.Qty, optCfg)
		log.Printf("[backtest] Using NIFTY50_10PTS strategy (optimized defaults: target=%.2f%%, momentum=%.2f%%)",
			optCfg.FNOTargetProfitPct, optCfg.MomentumBypassPct)
	default:
		// Use provided StrategyCfg if non-zero, otherwise fall back to defaults.
		cfg := e.cfg.StrategyCfg
		if cfg.EMA6Period == 0 {
			cfg = strategy.DefaultNifty50FnOConfig()
		}
		strat = strategy.NewNifty50FnOWithConfig(e.cfg.Qty, cfg)
		log.Printf("[backtest] Using NIFTY50_FNO strategy (sep=%.3f%%)", cfg.MinReEntrySeparationPct)
	}

	var trades []Trade
	var openTrade *Trade
	tradeID := 0

	// Track current trading day for EOD force-close
	var currentDay string

	for _, candle := range candles {
		candleDay := candle.TS.In(istLoc).Format("2006-01-02")
		candleIST := candle.TS.In(istLoc)

		// ── End-of-day force close ──
		if currentDay != "" && candleDay != currentDay && openTrade != nil {
			// New day started: force-close the previous day's open trade
			openTrade.ExitTime = candle.TS.In(istLoc)
			openTrade.ExitPrice = candle.Open // use next day's open as exit price
			openTrade.FNOExitPrice = e.lookupFNOPrice(openTrade.Side, candle.TS.Unix())
			openTrade.ExitReason = "end of day (forced close)"
			trades = append(trades, *openTrade)
			log.Printf("[backtest] ⚠️  EOD FORCE EXIT %s @ %s", openTrade.Side, openTrade.ExitTime.In(istLoc).Format("15:04"))
			openTrade = nil

			// Reset position state for new day (EMAs stay warm)
			strat.ResetPositions()
		}
		currentDay = candleDay

		// Flush HTF aggregators at day start for MTF strategy
		// (handled internally by the MTF strategy's daily reset logic)

		// ── Skip candles outside market hours (09:15–15:30 IST) ──
		marketOpen := time.Date(candleIST.Year(), candleIST.Month(), candleIST.Day(), 9, 15, 0, 0, istLoc)
		marketClose := time.Date(candleIST.Year(), candleIST.Month(), candleIST.Day(), 15, 30, 0, 0, istLoc)
		if candleIST.Before(marketOpen) || candleIST.After(marketClose) {
			continue
		}

		// ── Force close at 15:29 (last candle before market close) ──
		lastCandle := time.Date(candleIST.Year(), candleIST.Month(), candleIST.Day(), 15, 29, 0, 0, istLoc)
		if candleIST.Equal(lastCandle) || candleIST.After(lastCandle) {
			if openTrade != nil {
				openTrade.ExitTime = candle.TS.In(istLoc)
				openTrade.ExitPrice = candle.Close
				openTrade.FNOExitPrice = e.lookupFNOPrice(openTrade.Side, candle.TS.Unix())
				openTrade.ExitReason = "end of day (15:29 close)"
				trades = append(trades, *openTrade)
				log.Printf("[backtest] ⚠️  EOD FORCE EXIT %s @ %s index=%d fno=%d",
					openTrade.Side, candle.TS.In(istLoc).Format("15:04"), candle.Close, openTrade.FNOExitPrice)
				openTrade = nil
			}
			// Still feed candle to strategy to keep EMAs warm
			strat.OnTFCandle(candle)
			continue
		}

		// ── Stoploss check via candle extremes ──
		if openTrade != nil {
			slSignal := checkCandleStoplossTF(strat, candle)
			if slSignal != nil {
				openTrade.ExitTime = candle.TS.In(istLoc)
				openTrade.ExitPrice = candle.Close
				openTrade.FNOExitPrice = e.lookupFNOPrice(openTrade.Side, candle.TS.Unix())
				openTrade.ExitReason = slSignal.Reason
				trades = append(trades, *openTrade)
				log.Printf("[backtest] 🔴 SL EXIT %s @ %s index=%d fno=%d reason=%s",
					openTrade.Side, candle.TS.In(istLoc).Format("15:04"), candle.Close, openTrade.FNOExitPrice, slSignal.Reason)
				openTrade = nil
				continue
			}
		}

		sig := strat.OnTFCandle(candle)
		if sig == nil {
			continue
		}

		switch sig.Action {
		case strategy.ActionBuy:
			if openTrade != nil {
				// Close existing trade first
				openTrade.ExitTime = candle.TS.In(istLoc)
				openTrade.ExitPrice = candle.Close
				openTrade.FNOExitPrice = e.lookupFNOPrice(openTrade.Side, candle.TS.Unix())
				openTrade.ExitReason = "replaced by new entry"
				trades = append(trades, *openTrade)
			}
			tradeID++
			openTrade = &Trade{
				ID:            tradeID,
				Side:          sig.Side,
				EntryTime:     candle.TS.In(istLoc),
				EntryPrice:    candle.Close,
				EntryReason:   sig.Reason,
				FNOEntryPrice: e.lookupFNOPrice(sig.Side, candle.TS.Unix()),
			}
			log.Printf("[backtest] 🟢 ENTRY %s @ %s index=%d fno=%d reason=%s",
				sig.Side, candle.TS.In(istLoc).Format("15:04"), candle.Close, openTrade.FNOEntryPrice, sig.Reason)

		case strategy.ActionExit:
			if openTrade != nil {
				openTrade.ExitTime = candle.TS.In(istLoc)
				openTrade.ExitPrice = candle.Close
				openTrade.FNOExitPrice = e.lookupFNOPrice(openTrade.Side, candle.TS.Unix())
				openTrade.ExitReason = sig.Reason
				trades = append(trades, *openTrade)
				log.Printf("[backtest] 🔴 EXIT %s @ %s index=%d fno=%d reason=%s",
					sig.Side, candle.TS.In(istLoc).Format("15:04"), candle.Close, openTrade.FNOExitPrice, sig.Reason)
				openTrade = nil
			}
		}
	}

	// Close any open trade at end of data
	if openTrade != nil && len(candles) > 0 {
		lastCandle := candles[len(candles)-1]
		openTrade.ExitTime = lastCandle.TS.In(istLoc)
		openTrade.ExitPrice = lastCandle.Close
		openTrade.FNOExitPrice = e.lookupFNOPrice(openTrade.Side, lastCandle.TS.Unix())
		openTrade.ExitReason = "end of data"
		trades = append(trades, *openTrade)
		log.Printf("[backtest] ⚠️  FORCE EXIT %s @ %s (end of data)", openTrade.Side, lastCandle.TS.In(istLoc).Format("15:04"))
	}

	return trades
}

// checkCandleStoplossTF simulates tick-level stoploss using candle extremes.
func checkCandleStoplossTF(strat backtestStrategy, candle model.TFCandle) *strategy.Signal {
	// Check Low first (CALL SL trigger), then High (PUT SL trigger)
	lowTick := model.Tick{
		Token:    candle.Token,
		Exchange: candle.Exchange,
		Price:    candle.Low,
		TickTS:   candle.TS,
	}
	if sig := strat.OnTick(lowTick); sig != nil {
		return sig
	}

	highTick := model.Tick{
		Token:    candle.Token,
		Exchange: candle.Exchange,
		Price:    candle.High,
		TickTS:   candle.TS,
	}
	return strat.OnTick(highTick)
}

// ── Analytics Computation ─────────────────────────────────────────────

// computeMetrics calculates all summary metrics from a list of trades.
func computeMetrics(trades []Trade, qty int64) Metrics {
	if len(trades) == 0 {
		return Metrics{}
	}

	var (
		wins, losses           int
		callTrades, putTrades  int
		grossProfit, grossLoss float64
		bestTrade, worstTrade  float64
		totalWin, totalLoss    float64
		totalDuration          time.Duration
	)

	// Day tracking for total_days
	daySet := make(map[string]bool)

	for _, t := range trades {
		pnl := t.PnLRupees() * float64(qty)

		switch t.Side {
		case strategy.SideCall:
			callTrades++
		case strategy.SidePut:
			putTrades++
		}

		entryDay := t.EntryTime.In(istLoc).Format("2006-01-02")
		daySet[entryDay] = true

		totalDuration += t.Duration()

		if pnl >= 0 {
			wins++
			grossProfit += pnl
			totalWin += pnl
			if pnl > bestTrade {
				bestTrade = pnl
			}
		} else {
			losses++
			grossLoss += pnl // negative
			totalLoss += pnl
			if pnl < worstTrade {
				worstTrade = pnl
			}
		}
	}

	netPnL := grossProfit + grossLoss
	winRate := float64(wins) / float64(len(trades)) * 100

	var profitFactor float64
	if grossLoss != 0 {
		profitFactor = grossProfit / math.Abs(grossLoss)
	}

	var avgWin, avgLoss float64
	if wins > 0 {
		avgWin = totalWin / float64(wins)
	}
	if losses > 0 {
		avgLoss = totalLoss / float64(losses)
	}

	// Max drawdown
	maxDrawdown := computeMaxDrawdown(trades, qty)

	// Sharpe ratio
	sharpeRatio := computeSharpeRatio(trades, qty)

	// Average duration
	avgDur := totalDuration / time.Duration(len(trades))

	return Metrics{
		TotalTrades:  len(trades),
		CallTrades:   callTrades,
		PutTrades:    putTrades,
		Wins:         wins,
		Losses:       losses,
		WinRate:      math.Round(winRate*10) / 10,
		NetPnL:       math.Round(netPnL*100) / 100,
		GrossProfit:  math.Round(grossProfit*100) / 100,
		GrossLoss:    math.Round(grossLoss*100) / 100,
		ProfitFactor: math.Round(profitFactor*100) / 100,
		BestTrade:    math.Round(bestTrade*100) / 100,
		WorstTrade:   math.Round(worstTrade*100) / 100,
		AvgWin:       math.Round(avgWin*100) / 100,
		AvgLoss:      math.Round(avgLoss*100) / 100,
		MaxDrawdown:  math.Round(maxDrawdown*100) / 100,
		SharpeRatio:  math.Round(sharpeRatio*100) / 100,
		AvgDuration:  formatDuration(avgDur),
		TotalDays:    len(daySet),
	}
}

// computeMaxDrawdown calculates the largest peak-to-trough decline in equity.
func computeMaxDrawdown(trades []Trade, qty int64) float64 {
	if len(trades) == 0 {
		return 0
	}

	var cumPnL, peak, maxDD float64
	for _, t := range trades {
		pnl := t.PnLRupees() * float64(qty)
		cumPnL += pnl
		if cumPnL > peak {
			peak = cumPnL
		}
		dd := cumPnL - peak
		if dd < maxDD {
			maxDD = dd
		}
	}
	return maxDD
}

// computeSharpeRatio computes the annualized Sharpe ratio.
// Uses daily P&L returns, annualized with sqrt(252).
func computeSharpeRatio(trades []Trade, qty int64) float64 {
	if len(trades) < 2 {
		return 0
	}

	// Group by day
	dayPnL := make(map[string]float64)
	for _, t := range trades {
		day := t.EntryTime.In(istLoc).Format("2006-01-02")
		dayPnL[day] += t.PnLRupees() * float64(qty)
	}

	if len(dayPnL) < 2 {
		return 0
	}

	// Calculate mean and stddev
	var returns []float64
	for _, pnl := range dayPnL {
		returns = append(returns, pnl)
	}

	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	variance := 0.0
	for _, r := range returns {
		diff := r - mean
		variance += diff * diff
	}
	variance /= float64(len(returns) - 1)
	stddev := math.Sqrt(variance)

	if stddev == 0 {
		return 0
	}

	// Annualize: Sharpe = (mean / stddev) * sqrt(252)
	return (mean / stddev) * math.Sqrt(252)
}

// computeEquityCurve returns cumulative P&L after each trade.
func computeEquityCurve(trades []Trade, qty int64) []float64 {
	curve := make([]float64, len(trades))
	var cumPnL float64
	for i, t := range trades {
		cumPnL += t.PnLRupees() * float64(qty)
		curve[i] = math.Round(cumPnL*100) / 100
	}
	return curve
}

// computeDaySummaries groups trades by trading day and computes per-day stats.
func computeDaySummaries(trades []Trade, qty int64) []DaySummary {
	if len(trades) == 0 {
		return nil
	}

	// Ordered day tracking
	type dayData struct {
		date   string
		trades int
		wins   int
		losses int
		pnl    float64
	}

	dayOrder := make([]string, 0)
	dayMap := make(map[string]*dayData)

	for _, t := range trades {
		day := t.EntryTime.In(istLoc).Format("2006-01-02")
		if _, ok := dayMap[day]; !ok {
			dayMap[day] = &dayData{date: day}
			dayOrder = append(dayOrder, day)
		}
		d := dayMap[day]
		d.trades++
		pnl := t.PnLRupees() * float64(qty)
		d.pnl += pnl
		if pnl >= 0 {
			d.wins++
		} else {
			d.losses++
		}
	}

	var summaries []DaySummary
	var cumPnL float64
	var peak float64
	var maxDD float64

	for _, day := range dayOrder {
		d := dayMap[day]
		cumPnL += d.pnl
		if cumPnL > peak {
			peak = cumPnL
		}
		dd := cumPnL - peak
		if dd < maxDD {
			maxDD = dd
		}

		summaries = append(summaries, DaySummary{
			Date:        d.date,
			Trades:      d.trades,
			Wins:        d.wins,
			Losses:      d.losses,
			PnL:         math.Round(d.pnl*100) / 100,
			CumPnL:      math.Round(cumPnL*100) / 100,
			MaxDrawdown: math.Round(maxDD*100) / 100,
		})
	}

	return summaries
}

// ── Helpers ───────────────────────────────────────────────────────────

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
