package archiver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/underlying"
)

// coldMissStop ends a walk back after this many days in a row with no
// candles for the instrument (no file, or no matching rows): longer than
// any market closure, so the history has ended (archiving had not started,
// or the contract did not trade yet).
const coldMissStop = 10

// coldTimeout bounds one chart page; MEGA downloads of a few day files
// fit well inside it.
const coldTimeout = 30 * time.Second

// ColdReader serves TF candles older than the local SQLite window from the
// MEGA day files, for chart scroll-back.
type ColdReader struct {
	f       *dayFetcher
	maxDays int
}

// NewColdReader returns nil when MEGA credentials are not configured.
func NewColdReader(cfg Config) *ColdReader {
	if cfg.MegaEmail == "" || cfg.MegaPassword == "" {
		return nil
	}
	return newColdReader(newMegaStore(cfg.MegaEmail, cfg.MegaPassword), cfg)
}

func newColdReader(store Downloader, cfg Config) *ColdReader {
	maxDays := cfg.ColdMaxDays
	if maxDays <= 0 {
		maxDays = 365
	}
	r := &ColdReader{
		f:       newDayFetcher(store, cfg.MegaRootDir, cfg.ColdCacheDir, int64(cfg.ColdCacheMB)<<20),
		maxDays: maxDays,
	}
	if cfg.ColdCacheTTL > 0 {
		r.f.expire(cfg.ColdCacheTTL) // leftovers from the last run
		go r.expireLoop(cfg.ColdCacheTTL)
	}
	return r
}

// expireLoop deletes unused cached days for the life of the process (the
// gateway), checking every minute or every ttl if shorter.
func (r *ColdReader) expireLoop(ttl time.Duration) {
	every := time.Minute
	if ttl < every {
		every = ttl
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		r.f.expire(ttl)
	}
}

// ReadTFCandlesBefore returns up to limit candles with ts < beforeTS,
// oldest first, walking back one IST day file at a time. On a MEGA error it
// returns what it already has, so the chart shows what is available.
func (r *ColdReader) ReadTFCandlesBefore(exchange, token string, tf int, beforeTS int64, limit int) ([]model.TFCandle, error) {
	if limit <= 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), coldTimeout)
	defer cancel()

	var newestFirst []model.TFCandle
	day := time.Unix(beforeTS-1, 0).In(markethours.IST)
	misses := 0
	for i := 0; i < r.maxDays && misses < coldMissStop && len(newestFirst) < limit; i, day = i+1, day.AddDate(0, 0, -1) {
		path, err := r.fetchCandles(ctx, exchange, token, day)
		if errors.Is(err, ErrNoArchive) {
			misses++
			continue
		}
		if err != nil {
			log.Printf("[archiver] cold read %s: %v", dayKey(day), err)
			break
		}
		got, err := readDayCandles(path, exchange, token, tf, beforeTS, limit-len(newestFirst))
		if err != nil {
			log.Printf("[archiver] cold read %s: %v", dayKey(day), err)
			break
		}
		if len(got) == 0 {
			misses++
			continue
		}
		misses = 0
		newestFirst = append(newestFirst, got...)
	}
	for i, j := 0, len(newestFirst)-1; i < j; i, j = i+1, j-1 {
		newestFirst[i], newestFirst[j] = newestFirst[j], newestFirst[i]
	}
	return newestFirst, nil
}

// readDayCandles reads one day file's candles_tf rows, newest first.
func readDayCandles(path, exchange, token string, tf int, beforeTS int64, limit int) ([]model.TFCandle, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if ok, err := tableExists(context.Background(), db, "candles_tf"); err != nil || !ok {
		return nil, err
	}
	rows, err := db.Query(`
		SELECT token, exchange, tf, ts, open, high, low, close, volume, count
		FROM candles_tf
		WHERE exchange = ? AND token = ? AND tf = ? AND ts < ?
		ORDER BY ts DESC
		LIMIT ?`, exchange, token, tf, beforeTS, limit)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", path, err)
	}
	defer rows.Close()
	var out []model.TFCandle
	for rows.Next() {
		var c model.TFCandle
		var ts int64
		var vol, cnt sql.NullInt64
		if err := rows.Scan(&c.Token, &c.Exchange, &c.TF, &ts, &c.Open, &c.High, &c.Low, &c.Close, &vol, &cnt); err != nil {
			return nil, fmt.Errorf("scan %s: %w", path, err)
		}
		c.TS = time.Unix(ts, 0).UTC()
		c.Volume, c.Count = vol.Int64, int(cnt.Int64)
		out = append(out, c)
	}
	return out, rows.Err()
}

// fetchCandles returns the day file holding exchange:token's candles: the
// index's own file, or the combined file of days archived before candles
// were split per index.
func (r *ColdReader) fetchCandles(ctx context.Context, exchange, token string, day time.Time) (string, error) {
	if p, ok := underlying.ByIndexKey(exchange + ":" + token); ok {
		path, err := r.f.fetch(ctx, CandleSource(p.Key), day)
		if !errors.Is(err, ErrNoArchive) {
			return path, err
		}
	}
	return r.f.fetch(ctx, legacyCandles, day)
}
