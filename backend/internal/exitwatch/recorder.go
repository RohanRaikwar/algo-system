package exitwatch

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Recorder writes decision changes and 1Hz feature rows to SQLite off the
// hot path. Rows are queued and batch-inserted; a full queue drops rows.
type Recorder struct {
	db      *sql.DB
	ch      chan recRow
	dropped atomic.Int64
}

type recRow struct {
	table string // "decisions" or "features_1hz"
	v     View
}

const recorderSchema = `
CREATE TABLE IF NOT EXISTS decisions (
	ts INTEGER NOT NULL, strategy TEXT, side TEXT, fno_token TEXT,
	decision TEXT, exit_reason TEXT, p REAL,
	index_ltp INTEGER, premium_ltp INTEGER, index_entry INTEGER, target_level INTEGER,
	fno_entry_price INTEGER, progress REAL, peak_progress REAL, features_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_decisions_ts ON decisions(ts);
CREATE TABLE IF NOT EXISTS features_1hz (
	ts INTEGER NOT NULL, strategy TEXT, side TEXT, fno_token TEXT,
	decision TEXT, exit_reason TEXT, p REAL,
	index_ltp INTEGER, premium_ltp INTEGER, index_entry INTEGER, target_level INTEGER,
	fno_entry_price INTEGER, progress REAL, peak_progress REAL, features_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_features_ts ON features_1hz(ts);`

// OpenRecorder opens (or creates) the SQLite file.
func OpenRecorder(path string) (*Recorder, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(recorderSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("exitwatch schema: %w", err)
	}
	return &Recorder{db: db, ch: make(chan recRow, 4096)}, nil
}

func (r *Recorder) enqueue(table string, v View) {
	select {
	case r.ch <- recRow{table, v}:
	default:
		r.dropped.Add(1)
	}
}

// Run batch-inserts queued rows every second until done is closed, then
// drains the queue and closes the DB.
func (r *Recorder) Run(done <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	batch := make([]recRow, 0, 256)
	for {
		select {
		case row := <-r.ch:
			batch = append(batch, row)
			continue
		case <-t.C:
		case <-done:
		drain:
			for {
				select {
				case row := <-r.ch:
					batch = append(batch, row)
				default:
					break drain
				}
			}
			r.write(batch)
			r.db.Close()
			return
		}
		r.write(batch)
		batch = batch[:0]
		if n := r.dropped.Swap(0); n > 0 {
			log.Printf("[exitwatch] recorder queue full, dropped %d rows", n)
		}
	}
}

func (r *Recorder) write(rows []recRow) {
	if len(rows) == 0 {
		return
	}
	tx, err := r.db.Begin()
	if err != nil {
		log.Printf("[exitwatch] recorder begin: %v", err)
		return
	}
	for _, row := range rows {
		v := row.v
		fj, _ := json.Marshal(v.Features)
		_, err := tx.Exec(`INSERT INTO `+row.table+` (ts, strategy, side, fno_token, decision, exit_reason, p,
			index_ltp, premium_ltp, index_entry, target_level, fno_entry_price, progress, peak_progress, features_json)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			v.TS.UnixMilli(), v.Strategy, v.Side, v.FNOToken, string(v.Decision), v.ExitReason, v.P,
			v.IndexLTP, v.PremiumLTP, v.IndexEntry, v.TargetLevel, v.FNOEntryPrice, v.Progress, v.PeakProgress, string(fj))
		if err != nil {
			log.Printf("[exitwatch] recorder insert: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[exitwatch] recorder commit: %v", err)
	}
}
