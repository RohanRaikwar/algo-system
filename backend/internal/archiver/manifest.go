package archiver

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// manifest records every file confirmed in remote storage. Local rows are
// deleted only for days that have a manifest entry, so a lost upload can
// never lose data.
type manifest struct{ db *sql.DB }

func openManifest(path string) (*manifest, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS archived (
			source      TEXT    NOT NULL,
			day         TEXT    NOT NULL,
			remote_path TEXT    NOT NULL,
			rows        INTEGER NOT NULL,
			bytes       INTEGER NOT NULL,
			sha256      TEXT    NOT NULL,
			uploaded_at INTEGER NOT NULL,
			PRIMARY KEY (source, day)
		)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("manifest schema: %w", err)
	}
	return &manifest{db: db}, nil
}

func (m *manifest) has(ctx context.Context, source, day string) (bool, error) {
	var n int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM archived WHERE source = ? AND day = ?`, source, day).Scan(&n)
	return n > 0, err
}

type manifestEntry struct {
	Source, Day, RemotePath, SHA256 string
	Rows, Bytes                     int64
}

func (m *manifest) record(ctx context.Context, e manifestEntry) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO archived (source, day, remote_path, rows, bytes, sha256, uploaded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Source, e.Day, e.RemotePath, e.Rows, e.Bytes, e.SHA256, time.Now().Unix())
	return err
}

func (m *manifest) Close() error { return m.db.Close() }
