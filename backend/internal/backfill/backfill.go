// Package backfill fills missing index candles in the candles database from
// Angel One's historical API, so every underlying (NIFTY, SENSEX, …) has
// the same history: a new index starts with its past days, and days the
// container was down are filled for all of them.
//
// Only 1m candles exist upstream; every other enabled timeframe is built
// from them with the live builder's alignment (bucket = ts - ts%tf). 1s
// candles cannot be backfilled. Rows already in the database always win
// (INSERT OR IGNORE): the live feed is never overwritten.
package backfill

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"trading-systemv1/internal/archiver"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/underlying"
)

// fullDay is the 1m row count above which a day counts as complete (a
// session is 375 minutes; a few missing minutes are not worth a call).
const fullDay = 370

// CandleAPI fetches historical candles. *smartconnect.SmartConnect
// satisfies it.
type CandleAPI interface {
	GetCandleData(params map[string]any) (map[string]any, error)
}

// Config controls one run.
type Config struct {
	Days  int           // trading days back from yesterday
	TFs   []int         // timeframes to build, seconds (60 is always stored)
	Delay time.Duration // between API calls
	Now   func() time.Time
}

// candle is one 1m bar in paise.
type candle struct {
	ts                     int64 // unix seconds, bucket start
	open, high, low, close int64
	volume                 int64
}

// maxSpanDays is the most calendar days one ONE_MINUTE request may cover
// (Angel's limit), so a whole month of missing days costs one call.
const maxSpanDays = 30

// rateBackoff is the wait before each retry of a rate-limited request.
var rateBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

// Run fills every configured underlying's index. It returns the number of
// 1m candles inserted; per-request API errors are logged and skipped.
// Missing days are fetched in spans of up to maxSpanDays per request,
// spaced by cfg.Delay and retried with backoff when Angel rate-limits.
func Run(ctx context.Context, cfg Config, api CandleAPI, db *sql.DB) (int, error) {
	if cfg.Days <= 0 {
		return 0, nil
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	days := tradingDays(cfg.Now(), cfg.Days)
	total, calls := 0, 0
	for _, p := range underlying.All() {
		var missing []time.Time
		for _, day := range days {
			n, err := dayRows(ctx, db, p.IndexExchange, p.IndexToken, day)
			if err != nil {
				return total, err
			}
			if n < fullDay {
				missing = append(missing, day)
			}
		}
		filled := 0
		for _, span := range spans(missing) {
			if calls > 0 && cfg.Delay > 0 {
				if err := sleep(ctx, cfg.Delay); err != nil {
					return total, err
				}
			}
			calls++
			from, to := span[0], span[len(span)-1]
			bars, err := fetchSpan(ctx, api, p.IndexExchange, p.IndexToken, from, to)
			if err != nil {
				log.Printf("[backfill] %s %s..%s: %v (skipped)", p.Key, from.Format("2006-01-02"), to.Format("2006-01-02"), err)
				continue
			}
			for _, day := range span {
				ins, err := store(ctx, db, p.IndexExchange, p.IndexToken, dayBars(bars, day), cfg.TFs)
				if err != nil {
					return total, err
				}
				if ins > 0 {
					// The day may already be in MEGA without these rows:
					// have the archiver merge them into its day file.
					if err := archiver.MarkDirty(ctx, db, day.Format("2006-01-02")); err != nil {
						return total, err
					}
				}
				filled += ins
			}
		}
		if filled > 0 {
			log.Printf("[backfill] ✅ %s %s: %d 1m candles filled (%d missing days)", p.Key, p.IndexKey(), filled, len(missing))
		}
		total += filled
	}
	log.Printf("[backfill] done: %d candles, %d API calls", total, calls)
	return total, nil
}

// spans groups sorted days into runs covering at most maxSpanDays
// calendar days each.
func spans(days []time.Time) [][]time.Time {
	var out [][]time.Time
	for _, d := range days {
		if n := len(out); n > 0 && d.Sub(out[n-1][0]) < maxSpanDays*24*time.Hour {
			out[n-1] = append(out[n-1], d)
			continue
		}
		out = append(out, []time.Time{d})
	}
	return out
}

// dayBars keeps the bars of one IST day.
func dayBars(bars []candle, day time.Time) []candle {
	start := day.Unix()
	var out []candle
	for _, c := range bars {
		if c.ts >= start && c.ts < start+86400 {
			out = append(out, c)
		}
	}
	return out
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// isRateLimit reports Angel's throttling replies.
func isRateLimit(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "exceeding access rate") || strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "AB1019") || strings.Contains(msg, "Too many")
}

// tradingDays returns the last n trading days before now's IST date,
// oldest first.
func tradingDays(now time.Time, n int) []time.Time {
	ist := now.In(markethours.IST)
	d := time.Date(ist.Year(), ist.Month(), ist.Day(), 0, 0, 0, 0, markethours.IST)
	var out []time.Time
	for i := 0; len(out) < n && i < n*3; i++ {
		d = d.AddDate(0, 0, -1)
		if markethours.IsTradingDay(d) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// dayRows counts the 1m candles stored for one index on one IST day.
func dayRows(ctx context.Context, db *sql.DB, exchange, token string, day time.Time) (int, error) {
	start := day.Unix()
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM candles_tf WHERE exchange = ? AND token = ? AND tf = 60 AND ts >= ? AND ts < ?`,
		exchange, token, start, start+86400).Scan(&n)
	return n, err
}

// fetchSpan asks Angel for the 1m candles of the sessions from..to,
// retrying with backoff when rate-limited.
func fetchSpan(ctx context.Context, api CandleAPI, exchange, token string, from, to time.Time) ([]candle, error) {
	params := map[string]any{
		"exchange": exchange, "symboltoken": token, "interval": "ONE_MINUTE",
		"fromdate": from.Format("2006-01-02") + " 09:15", "todate": to.Format("2006-01-02") + " 15:30",
	}
	var resp map[string]any
	var err error
	for attempt := 0; ; attempt++ {
		resp, err = api.GetCandleData(params)
		if err == nil || !isRateLimit(err) || attempt >= len(rateBackoff) {
			break
		}
		log.Printf("[backfill] rate-limited; retrying in %s", rateBackoff[attempt])
		if err := sleep(ctx, rateBackoff[attempt]); err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	if st, ok := resp["status"].(bool); ok && !st {
		return nil, fmt.Errorf("status=false: %v", resp["message"])
	}
	return parse(resp), nil
}

// parse reads Angel's [[ts, o, h, l, c, v], …]. Prices arrive as JSON
// numbers in rupees and are converted to integer paise here, at the edge.
func parse(resp map[string]any) []candle {
	rows, _ := resp["data"].([]any)
	out := make([]candle, 0, len(rows))
	for _, r := range rows {
		a, ok := r.([]any)
		if !ok || len(a) < 6 {
			continue
		}
		s, _ := a[0].(string)
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			continue
		}
		c := candle{ts: t.Unix(), open: paise(a[1]), high: paise(a[2]), low: paise(a[3]), close: paise(a[4]), volume: int64(num(a[5]))}
		if c.open <= 0 || c.close <= 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func paise(v any) int64 { return int64(math.Round(num(v) * 100)) }

// store inserts the 1m bars and every TF built from them; it returns how
// many 1m rows were new.
func store(ctx context.Context, db *sql.DB, exchange, token string, bars []candle, tfs []int) (int, error) {
	if len(bars) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO candles_tf
		(token, exchange, tf, ts, open, high, low, close, volume, count) VALUES (?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	inserted := 0
	for _, c := range bars {
		res, err := stmt.ExecContext(ctx, token, exchange, 60, c.ts, c.open, c.high, c.low, c.close, c.volume, 60)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	for _, tf := range tfs {
		if tf <= 60 || tf%60 != 0 {
			continue
		}
		for _, c := range aggregate(bars, int64(tf)) {
			if _, err := stmt.ExecContext(ctx, token, exchange, tf, c.ts, c.open, c.high, c.low, c.close, c.volume, c.count); err != nil {
				return 0, err
			}
		}
	}
	return inserted, tx.Commit()
}

// tfCandle is an aggregated bar with the number of 1s candles it stands for.
type tfCandle struct {
	candle
	count int64
}

// aggregate builds tf-second bars from sorted-or-not 1m bars, aligned like
// the live builder (bucket = ts - ts%tf).
func aggregate(bars []candle, tf int64) []tfCandle {
	sorted := append([]candle(nil), bars...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ts < sorted[j].ts })
	var out []tfCandle
	for _, c := range sorted {
		b := c.ts - c.ts%tf
		if n := len(out); n > 0 && out[n-1].ts == b {
			o := &out[n-1]
			o.high = max(o.high, c.high)
			o.low = min(o.low, c.low)
			o.close = c.close
			o.volume += c.volume
			o.count += 60
			continue
		}
		out = append(out, tfCandle{candle: candle{ts: b, open: c.open, high: c.high, low: c.low, close: c.close, volume: c.volume}, count: 60})
	}
	return out
}
