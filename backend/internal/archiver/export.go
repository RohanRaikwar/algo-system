package archiver

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"trading-systemv1/internal/markethours"

	_ "github.com/mattn/go-sqlite3"
)

// openDB opens a live service database. busy_timeout lets the archiver
// wait out a service's write transaction instead of failing on SQLITE_BUSY.
// One connection: ATTACH is per-connection state.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// dayBounds returns the unix-second range [start, end) of an IST day.
func dayBounds(day time.Time) (start, end int64) {
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, markethours.IST)
	return d.Unix(), d.AddDate(0, 0, 1).Unix()
}

func dayKey(day time.Time) string { return day.In(markethours.IST).Format("2006-01-02") }

// tableExists reports whether table exists in the main schema.
func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
	return n > 0, err
}

// earliestTS returns the smallest ts across tables, or ok=false when every
// table is empty or missing.
func earliestTS(ctx context.Context, db *sql.DB, tables []string) (ts int64, ok bool, err error) {
	for _, t := range tables {
		exists, err := tableExists(ctx, db, t)
		if err != nil {
			return 0, false, err
		}
		if !exists {
			continue
		}
		var v sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT MIN(ts) FROM `+t).Scan(&v); err != nil {
			return 0, false, fmt.Errorf("min ts %s: %w", t, err)
		}
		if v.Valid && (!ok || v.Int64 < ts) {
			ts, ok = v.Int64, true
		}
	}
	return ts, ok, nil
}

// exportDay copies the rows of tables whose ts falls in [start, end) into
// a fresh SQLite file at dst. It returns the number of rows copied; zero
// means the day had no data and dst should be ignored.
func exportDay(ctx context.Context, db *sql.DB, tables []string, start, end int64, dst string) (int64, error) {
	_ = os.Remove(dst)
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS a`, dst); err != nil {
		return 0, fmt.Errorf("attach %s: %w", dst, err)
	}
	defer db.ExecContext(context.Background(), `DETACH DATABASE a`)

	var total int64
	for _, t := range tables {
		exists, err := tableExists(ctx, db, t)
		if err != nil {
			return 0, err
		}
		if !exists {
			continue
		}
		if _, err := db.ExecContext(ctx,
			`CREATE TABLE a.`+t+` AS SELECT * FROM main.`+t+` WHERE ts >= ? AND ts < ?`, start, end); err != nil {
			return 0, fmt.Errorf("export %s: %w", t, err)
		}
		// CREATE TABLE AS reports no rows affected; count the copy instead.
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM a.`+t).Scan(&n); err != nil {
			return 0, fmt.Errorf("count %s: %w", t, err)
		}
		total += n
	}
	return total, nil
}

// snapshotDB writes a consistent copy of the whole database to dst.
func snapshotDB(ctx context.Context, db *sql.DB, dst string) error {
	_ = os.Remove(dst)
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dst, err)
	}
	return nil
}

// gzipFile compresses src into dst, returning dst's size and sha256.
func gzipFile(src, dst string) (size int64, sum string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	zw := gzip.NewWriter(io.MultiWriter(out, h))
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		return 0, "", err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return 0, "", err
	}
	if err := out.Close(); err != nil {
		return 0, "", err
	}
	st, err := os.Stat(dst)
	if err != nil {
		return 0, "", err
	}
	return st.Size(), hex.EncodeToString(h.Sum(nil)), nil
}
