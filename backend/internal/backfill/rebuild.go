package backfill

import (
	"context"
	"database/sql"
	"log"
	"time"

	"trading-systemv1/internal/archiver"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/underlying"
)

// Rebuild builds, for each of the last cfg.Days trading days that has 1m
// candles, every enabled TF the day has no rows for (or whose rows are off
// the TF's boundaries, model.TFBucket), from those 1m rows.
// A TF added to ENABLED_TFS then gets its past days without an API call
// (Run skips days whose 1m rows are complete). It returns the number of
// TF rows inserted.
func Rebuild(ctx context.Context, cfg Config, db *sql.DB) (int, error) {
	if cfg.Days <= 0 {
		return 0, nil
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	total := 0
	for _, p := range underlying.All() {
		built := 0
		for _, day := range tradingDays(cfg.Now(), cfg.Days) {
			n, err := rebuildDay(ctx, db, p.IndexExchange, p.IndexToken, day, cfg.TFs)
			if err != nil {
				return total, err
			}
			if n > 0 {
				if err := archiver.MarkDirty(ctx, db, archiver.CandleSource(p.Key), day.Format("2006-01-02")); err != nil {
					return total, err
				}
			}
			built += n
		}
		if built > 0 {
			log.Printf("[backfill] ✅ %s: %d TF candles rebuilt from 1m rows", p.Key, built)
		}
		total += built
	}
	return total, nil
}

// rebuildDay fills the TFs one day has no rows for.
func rebuildDay(ctx context.Context, db *sql.DB, exchange, token string, day time.Time, tfs []int) (int, error) {
	start := day.Unix()
	var todo []int
	for _, tf := range tfs {
		if tf <= 60 || tf%60 != 0 {
			continue
		}
		// A day with no rows, or with bars not on the TF's boundaries
		// (10m/30m/1h stored before they moved to the 09:15 open), is rebuilt.
		var n, off int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*), COALESCE(SUM((ts - ?) % ? != 0), 0) FROM candles_tf
			 WHERE exchange = ? AND token = ? AND tf = ? AND ts >= ? AND ts < ?`,
			model.TFOffset(int64(tf)), tf, exchange, token, tf, start, start+86400).Scan(&n, &off); err != nil {
			return 0, err
		}
		if n == 0 || off > 0 {
			todo = append(todo, tf)
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}
	bars, err := oneMinute(ctx, db, exchange, token, start)
	if err != nil || len(bars) == 0 {
		return 0, err
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
	for _, tf := range todo {
		if _, err := tx.ExecContext(ctx, `DELETE FROM candles_tf WHERE exchange = ? AND token = ? AND tf = ? AND ts >= ? AND ts < ?`,
			exchange, token, tf, start, start+86400); err != nil {
			return 0, err
		}
		for _, c := range aggregate(bars, int64(tf)) {
			res, err := stmt.ExecContext(ctx, token, exchange, tf, c.ts, c.open, c.high, c.low, c.close, c.volume, c.count)
			if err != nil {
				return 0, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted++
			}
		}
	}
	return inserted, tx.Commit()
}

// oneMinute reads one IST day's stored 1m bars.
func oneMinute(ctx context.Context, db *sql.DB, exchange, token string, start int64) ([]candle, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, open, high, low, close, COALESCE(volume, 0) FROM candles_tf
		 WHERE exchange = ? AND token = ? AND tf = 60 AND ts >= ? AND ts < ? ORDER BY ts`,
		exchange, token, start, start+86400)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candle
	for rows.Next() {
		var c candle
		if err := rows.Scan(&c.ts, &c.open, &c.high, &c.low, &c.close, &c.volume); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
