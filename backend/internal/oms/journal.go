package oms

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Journal is the SQLite Store: append-only order events plus the claimed
// intent ids. synchronous=FULL, because an order event must survive a
// crash before the order it describes is sent.
type Journal struct {
	db *sql.DB
}

const journalSchema = `
CREATE TABLE IF NOT EXISTS intents (
	intent_id TEXT PRIMARY KEY,
	claimed_ts INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS order_events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	ts INTEGER NOT NULL,
	client_order_id TEXT NOT NULL,
	broker_order_id TEXT,
	strategy TEXT,
	position_key TEXT,
	side TEXT,
	symbol TEXT,
	token TEXT,
	state TEXT NOT NULL,
	qty INTEGER,
	filled_qty INTEGER,
	avg_price_paise INTEGER,
	reason TEXT,
	source TEXT
);
CREATE INDEX IF NOT EXISTS idx_order_events_ts ON order_events(ts);
CREATE INDEX IF NOT EXISTS idx_order_events_client ON order_events(client_order_id);`

// OpenJournal opens (or creates) the journal at path.
func OpenJournal(path string) (*Journal, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=FULL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(journalSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("oms journal schema: %w", err)
	}
	return &Journal{db: db}, nil
}

// Close closes the database.
func (j *Journal) Close() error { return j.db.Close() }

// ClaimIntent inserts intentID; false means it was already claimed.
func (j *Journal) ClaimIntent(intentID string, at time.Time) (bool, error) {
	res, err := j.db.Exec(`INSERT OR IGNORE INTO intents(intent_id, claimed_ts) VALUES(?, ?)`, intentID, at.UnixNano())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Append writes one event.
func (j *Journal) Append(ev Event) error {
	_, err := j.db.Exec(`INSERT INTO order_events
		(ts, client_order_id, broker_order_id, strategy, position_key, side, symbol, token,
		 state, qty, filled_qty, avg_price_paise, reason, source)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.TS.UnixNano(), ev.ClientOrderID, ev.BrokerOrderID, ev.Strategy, ev.PositionKey, ev.Side,
		ev.Symbol, ev.Token, string(ev.State), ev.Qty, ev.FilledQty, ev.AvgPricePaise, ev.Reason, ev.Source)
	return err
}

// Events returns events at or after since, in journal order.
func (j *Journal) Events(since time.Time) ([]Event, error) {
	rows, err := j.db.Query(`SELECT ts, client_order_id, broker_order_id, strategy, position_key, side,
		symbol, token, state, qty, filled_qty, avg_price_paise, reason, source
		FROM order_events WHERE ts >= ? ORDER BY seq`, since.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var ev Event
		var ts int64
		var state string
		var broker, strat, pos, side, sym, tok, reason, source sql.NullString
		var qty, filled, avg sql.NullInt64
		if err := rows.Scan(&ts, &ev.ClientOrderID, &broker, &strat, &pos, &side, &sym, &tok,
			&state, &qty, &filled, &avg, &reason, &source); err != nil {
			return nil, err
		}
		ev.TS = time.Unix(0, ts)
		ev.State = State(state)
		ev.BrokerOrderID, ev.Strategy, ev.PositionKey = broker.String, strat.String, pos.String
		ev.Side, ev.Symbol, ev.Token = side.String, sym.String, tok.String
		ev.Reason, ev.Source = reason.String, source.String
		ev.Qty, ev.FilledQty, ev.AvgPricePaise = qty.Int64, filled.Int64, avg.Int64
		out = append(out, ev)
	}
	return out, rows.Err()
}
