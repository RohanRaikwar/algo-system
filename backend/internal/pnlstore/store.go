// Package pnlstore is the P&L trade ledger: one row per closed trade (or
// partial exit), built from the executor's fills, paper and real. Prices
// and P&L are paise (int64). The open entries are kept beside it so an
// exit after a restart still knows its entry.
package pnlstore

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Entry is an open position's entry fill.
type Entry struct {
	PosKey     string // the writer's position key; "" = Key() builds one
	Strategy   string
	Underlying string // index key, e.g. NSE:99926000
	Side       string // CALL or PUT
	Leg        string // basket leg id, "" for single-leg
	Short      bool
	Exchange   string
	Token      string
	FNOSymbol  string
	Qty        int64
	Price      int64 // average entry, paise
	At         time.Time
	Real       bool
}

// Key identifies an entry: the writer's position key, else strategy,
// contract and leg.
func (e Entry) Key() string {
	if e.PosKey != "" {
		return e.PosKey
	}
	return e.Strategy + "|" + e.Exchange + ":" + e.Token + "|" + e.Leg
}

// Trade is one closed trade (or partial exit).
type Trade struct {
	ID         int64
	Strategy   string
	Underlying string
	Side       string
	Leg        string
	Short      bool
	Exchange   string
	Token      string
	FNOSymbol  string
	Qty        int64
	EntryPrice int64
	ExitPrice  int64
	Realized   int64 // paise
	EntryAt    time.Time
	ExitAt     time.Time
	ExitReason string
	Real       bool
}

// Store is the SQLite ledger. synchronous=FULL: a booked trade must
// survive a crash.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS trades (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	strategy    TEXT    NOT NULL,
	underlying  TEXT,
	side        TEXT,
	leg         TEXT,
	short       INTEGER NOT NULL DEFAULT 0,
	exchange    TEXT,
	token       TEXT,
	fno_symbol  TEXT,
	qty         INTEGER NOT NULL,
	entry_price INTEGER NOT NULL,
	exit_price  INTEGER NOT NULL,
	realized    INTEGER NOT NULL,
	entry_at    TEXT,
	exit_at     TEXT    NOT NULL,
	exit_reason TEXT,
	real        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_trades_exit ON trades(exit_at);
CREATE INDEX IF NOT EXISTS idx_trades_strategy_exit ON trades(strategy, exit_at);
CREATE TABLE IF NOT EXISTS open_entries (
	key        TEXT PRIMARY KEY,
	strategy   TEXT    NOT NULL,
	underlying TEXT,
	side       TEXT,
	leg        TEXT,
	short      INTEGER NOT NULL DEFAULT 0,
	exchange   TEXT,
	token      TEXT,
	fno_symbol TEXT,
	qty        INTEGER NOT NULL,
	price      INTEGER NOT NULL,
	at         TEXT,
	real       INTEGER NOT NULL DEFAULT 0
);`

// Open opens (or creates) the ledger at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=FULL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("pnl ledger schema: %w", err)
	}
	return &Store{db: db}, nil
}

// OpenReader opens an existing ledger read-only (gateway).
func OpenReader(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// tsLayout is fixed width, so stored times sort and compare as text.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(tsLayout)
}

func parseTS(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t.UTC()
}

// PutEntry writes (or replaces) an open entry.
func (s *Store) PutEntry(e Entry) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO open_entries
		(key, strategy, underlying, side, leg, short, exchange, token, fno_symbol, qty, price, at, real)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.Key(), e.Strategy, e.Underlying, e.Side, e.Leg, e.Short, e.Exchange, e.Token, e.FNOSymbol, e.Qty, e.Price, ts(e.At), e.Real)
	return err
}

// DeleteEntry removes an open entry.
func (s *Store) DeleteEntry(key string) error {
	_, err := s.db.Exec(`DELETE FROM open_entries WHERE key = ?`, key)
	return err
}

// Entries returns every open entry.
func (s *Store) Entries() ([]Entry, error) {
	rows, err := s.db.Query(`SELECT key, strategy, COALESCE(underlying,''), COALESCE(side,''), COALESCE(leg,''), short,
		COALESCE(exchange,''), COALESCE(token,''), COALESCE(fno_symbol,''), qty, price, COALESCE(at,''), real FROM open_entries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var at string
		if err := rows.Scan(&e.PosKey, &e.Strategy, &e.Underlying, &e.Side, &e.Leg, &e.Short, &e.Exchange, &e.Token, &e.FNOSymbol, &e.Qty, &e.Price, &at, &e.Real); err != nil {
			return nil, err
		}
		e.At = parseTS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddTrade appends a closed trade.
func (s *Store) AddTrade(t Trade) error {
	_, err := s.db.Exec(`INSERT INTO trades
		(strategy, underlying, side, leg, short, exchange, token, fno_symbol, qty,
		 entry_price, exit_price, realized, entry_at, exit_at, exit_reason, real)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Strategy, t.Underlying, t.Side, t.Leg, t.Short, t.Exchange, t.Token, t.FNOSymbol, t.Qty,
		t.EntryPrice, t.ExitPrice, t.Realized, ts(t.EntryAt), ts(t.ExitAt), t.ExitReason, t.Real)
	return err
}

// Trades returns the trades that exited in [from, to), oldest first; an
// empty strategy means all.
func (s *Store) Trades(from, to time.Time, strategy string) ([]Trade, error) {
	q := `SELECT id, strategy, COALESCE(underlying,''), COALESCE(side,''), COALESCE(leg,''), short,
		COALESCE(exchange,''), COALESCE(token,''), COALESCE(fno_symbol,''), qty, entry_price, exit_price, realized,
		COALESCE(entry_at,''), exit_at, COALESCE(exit_reason,''), real
		FROM trades WHERE exit_at >= ? AND exit_at < ?`
	args := []any{ts(from), ts(to)}
	if strategy != "" {
		q += ` AND strategy = ?`
		args = append(args, strategy)
	}
	rows, err := s.db.Query(q+` ORDER BY exit_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trade
	for rows.Next() {
		var t Trade
		var entryAt, exitAt string
		if err := rows.Scan(&t.ID, &t.Strategy, &t.Underlying, &t.Side, &t.Leg, &t.Short, &t.Exchange, &t.Token, &t.FNOSymbol,
			&t.Qty, &t.EntryPrice, &t.ExitPrice, &t.Realized, &entryAt, &exitAt, &t.ExitReason, &t.Real); err != nil {
			return nil, err
		}
		t.EntryAt, t.ExitAt = parseTS(entryAt), parseTS(exitAt)
		out = append(out, t)
	}
	return out, rows.Err()
}

// FirstExit is the exit time of the oldest trade; false when the ledger
// is empty.
func (s *Store) FirstExit() (time.Time, bool, error) {
	var v sql.NullString
	if err := s.db.QueryRow(`SELECT MIN(exit_at) FROM trades`).Scan(&v); err != nil {
		return time.Time{}, false, err
	}
	if !v.Valid || v.String == "" {
		return time.Time{}, false, nil
	}
	return parseTS(v.String), true, nil
}
