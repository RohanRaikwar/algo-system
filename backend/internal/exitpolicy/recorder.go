package exitpolicy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Recorder writes exit decisions (shadow and acted) to SQLite off the hot
// path. Rows are queued and batch-inserted; a full queue drops rows.
type Recorder struct {
	db      *sql.DB
	ch      chan recRow
	dropped atomic.Int64
}

type recRow struct {
	d     Decision
	acted bool
	note  string
}

const recorderSchema = `
CREATE TABLE IF NOT EXISTS exit_decisions (
	ts INTEGER NOT NULL, strategy TEXT, side TEXT, fno_token TEXT,
	reason TEXT, shadow INTEGER, acted INTEGER, text TEXT, note TEXT,
	entry_ts INTEGER, index_entry INTEGER, prem_entry INTEGER,
	delta_milli INTEGER, decay_per_day INTEGER, exp_gain INTEGER, detail_json TEXT
);
CREATE INDEX IF NOT EXISTS idx_exit_decisions_ts ON exit_decisions(ts);`

// OpenRecorder opens (or creates) the SQLite file.
func OpenRecorder(path string) (*Recorder, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(recorderSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("exitpolicy schema: %w", err)
	}
	return &Recorder{db: db, ch: make(chan recRow, 1024)}, nil
}

// Record queues d. acted is whether stratengine sent an exit for it; note
// says why not when it did not.
func (r *Recorder) Record(d Decision, acted bool, note string) {
	select {
	case r.ch <- recRow{d, acted, note}:
	default:
		r.dropped.Add(1)
	}
}

// Run batch-inserts queued rows every second until done is closed, then
// drains the queue and closes the DB.
func (r *Recorder) Run(done <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	batch := make([]recRow, 0, 16)
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
			log.Printf("[exitpolicy] recorder queue full, dropped %d rows", n)
		}
	}
}

func (r *Recorder) write(rows []recRow) {
	if len(rows) == 0 {
		return
	}
	tx, err := r.db.Begin()
	if err != nil {
		log.Printf("[exitpolicy] recorder begin: %v", err)
		return
	}
	for _, row := range rows {
		d, p := row.d, row.d.Position
		var g EntryGreeks
		if p.Greeks != nil {
			g = *p.Greeks
		}
		dj, _ := json.Marshal(d.Detail)
		_, err := tx.Exec(`INSERT INTO exit_decisions (ts, strategy, side, fno_token, reason, shadow, acted, text, note,
			entry_ts, index_entry, prem_entry, delta_milli, decay_per_day, exp_gain, detail_json)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.TS.UnixMilli(), p.Strategy, p.Side, p.FNOToken, string(d.Reason), d.Shadow, row.acted, d.Text, row.note,
			p.EntryTS.UnixMilli(), p.IndexEntry, p.PremEntry, g.DeltaMilli, g.DecayPerDayPaise, g.ExpGainPaise, string(dj))
		if err != nil {
			log.Printf("[exitpolicy] recorder insert: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[exitpolicy] recorder commit: %v", err)
	}
}
