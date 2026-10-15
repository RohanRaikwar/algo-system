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

// earliestTS returns the smallest day timestamp across tables, or
// ok=false when every table is empty or missing.
func earliestTS(ctx context.Context, db *sql.DB, tables []dayTable) (ts int64, ok bool, err error) {
	for _, t := range tables {
		exists, err := tableExists(ctx, db, t.name)
		if err != nil {
			return 0, false, err
		}
		if !exists {
			continue
		}
		var v sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT MIN(`+t.expr()+`) FROM `+t.name).Scan(&v); err != nil {
			return 0, false, fmt.Errorf("min ts %s: %w", t.name, err)
		}
		if v.Valid && (!ok || v.Int64 < ts) {
			ts, ok = v.Int64, true
		}
	}
	return ts, ok, nil
}

// exportDay copies the rows of tables whose day timestamp falls in
// [start, end) into a fresh SQLite file at dst. Each table is created in
// dst with its live CREATE statement, so a day file has the same keys and
// columns as the service database and can be merged back into it. It
// returns the number of rows copied; zero means the day had no data and dst
// should be ignored.
func exportDay(ctx context.Context, db *sql.DB, tables []dayTable, start, end int64, dst string) (int64, error) {
	_ = os.Remove(dst)
	var present []dayTable
	var ddls []string
	for _, t := range tables {
		ddl, err := tableDDL(ctx, db, t.name)
		if err != nil {
			return 0, err
		}
		if ddl == "" {
			continue
		}
		present = append(present, t)
		ddls = append(ddls, ddl)
	}
	if err := createTables(dst, ddls); err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS a`, dst); err != nil {
		return 0, fmt.Errorf("attach %s: %w", dst, err)
	}
	defer db.ExecContext(context.Background(), `DETACH DATABASE a`)

	var total int64
	for _, t := range present {
		res, err := db.ExecContext(ctx,
			`INSERT INTO a.`+t.name+` SELECT * FROM main.`+t.name+` WHERE `+t.expr()+` >= ? AND `+t.expr()+` < ?`, start, end)
		if err != nil {
			return 0, fmt.Errorf("export %s: %w", t.name, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// tableDDL returns the CREATE TABLE statement of table, or "" when it does
// not exist in schema (main or an attached name).
func tableDDL(ctx context.Context, db *sql.DB, table string) (string, error) {
	return schemaDDL(ctx, db, "main", table)
}

func schemaDDL(ctx context.Context, db *sql.DB, schema, table string) (string, error) {
	var ddl sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT sql FROM `+schema+`.sqlite_master WHERE type='table' AND name=?`, table).Scan(&ddl)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("schema %s.%s: %w", schema, table, err)
	}
	return ddl.String, nil
}

// createTables runs each CREATE statement in a new database file at path.
func createTables(path string, ddls []string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, ddl := range ddls {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("create in %s: %w", path, err)
		}
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
