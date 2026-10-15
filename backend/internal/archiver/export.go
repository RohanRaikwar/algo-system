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
	"strings"
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
		if err := db.QueryRowContext(ctx, `SELECT MIN(`+t.expr()+`) FROM `+t.name+` WHERE `+t.cond()).Scan(&v); err != nil {
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
			`INSERT INTO a.`+t.name+` SELECT * FROM main.`+t.name+` WHERE `+t.cond()+` AND `+t.expr()+` >= ? AND `+t.expr()+` < ?`, start, end)
		if err != nil {
			return 0, fmt.Errorf("export %s: %w", t.name, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// DirtyTable lists (source, IST day) pairs that received rows after they
// may have been archived. Writers of late rows (the candle backfill) mark
// them here; the archiver merges them into the uploaded file and clears
// them.
const DirtyTable = "archive_dirty"

// MarkDirty records source's day ("2006-01-02") in db's DirtyTable.
func MarkDirty(ctx context.Context, db *sql.DB, source, day string) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+DirtyTable+
		` (source TEXT NOT NULL, day TEXT NOT NULL, PRIMARY KEY (source, day))`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO `+DirtyTable+` (source, day) VALUES (?, ?)`, source, day)
	return err
}

func isDirty(ctx context.Context, db *sql.DB, source, day string) bool {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+DirtyTable+` WHERE source = ? AND day = ?`, source, day).Scan(&n); err != nil {
		return false // no table: nothing marked
	}
	return n > 0
}

func clearDirty(ctx context.Context, db *sql.DB, source, day string) error {
	if ok, err := tableExists(ctx, db, DirtyTable); err != nil || !ok {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM `+DirtyTable+` WHERE source = ? AND day = ?`, source, day)
	return err
}

// fileTableCounts counts the rows of every table in a SQLite file.
func fileTableCounts(path string) (map[string]int64, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	rows.Close()
	out := make(map[string]int64, len(names))
	for _, n := range names {
		var c int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM "` + n + `"`).Scan(&c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, nil
}

// mergeIntoFile copies one day of the live tables into the SQLite file at
// path (a downloaded day file). Rows already in the file are kept; a table
// the file lacks is created with the live CREATE statement.
func mergeIntoFile(ctx context.Context, db *sql.DB, path string, tables []dayTable, start, end int64) error {
	var missing []string
	for _, t := range tables {
		ddl, err := tableDDL(ctx, db, t.name)
		if err != nil {
			return err
		}
		if ddl == "" {
			continue
		}
		have, err := fileHasTable(path, t.name)
		if err != nil {
			return err
		}
		if !have {
			missing = append(missing, ddl)
		}
	}
	if err := createTables(path, missing); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS a`, path); err != nil {
		return fmt.Errorf("attach %s: %w", path, err)
	}
	defer db.ExecContext(context.Background(), `DETACH DATABASE a`)
	for _, t := range tables {
		exists, err := tableExists(ctx, db, t.name)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		cols, err := tableColumns(ctx, db, "a", t.name)
		if err != nil {
			return err
		}
		list := `"` + strings.Join(cols, `","`) + `"`
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO a.`+t.name+` (`+list+`) SELECT `+list+` FROM main.`+t.name+
				` WHERE `+t.cond()+` AND `+t.expr()+` >= ? AND `+t.expr()+` < ?`, start, end); err != nil {
			return fmt.Errorf("merge %s: %w", t.name, err)
		}
	}
	return nil
}

func fileHasTable(path, table string) (bool, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
	return n > 0, err
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
