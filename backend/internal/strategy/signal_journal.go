package strategy

import (
	"database/sql"
	"encoding/json"
	"log"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SignalJournal is a durable, append-only log of strategy signals.
// Supports audit, replay, and deduplication.
type SignalJournal struct {
	mu sync.Mutex
	db *sql.DB
}

// NewSignalJournal opens (or creates) a SQLite signal journal.
func NewSignalJournal(dbPath string) (*SignalJournal, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_sync=NORMAL")
	if err != nil {
		return nil, err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS signals (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		strategy   TEXT NOT NULL,
		action     TEXT NOT NULL,
		side       TEXT NOT NULL DEFAULT '',
		market_state TEXT NOT NULL DEFAULT '',
		token      TEXT NOT NULL,
		exchange   TEXT NOT NULL,
		reason     TEXT,
		ema_values TEXT,
		price      INTEGER DEFAULT 0,
		qty        INTEGER DEFAULT 1,
		live_mode  INTEGER DEFAULT 0,
		profit_cap INTEGER DEFAULT 0,
		candle_ts  DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(strategy, token, exchange, action, side, candle_ts)
	);
	CREATE INDEX IF NOT EXISTS idx_signals_strategy ON signals(strategy);
	CREATE INDEX IF NOT EXISTS idx_signals_token ON signals(token, exchange);
	CREATE INDEX IF NOT EXISTS idx_signals_candle_ts ON signals(candle_ts);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	// Auto-migrate: add columns if missing (for existing DBs)
	db.Exec(`ALTER TABLE signals ADD COLUMN price INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE signals ADD COLUMN side TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE signals ADD COLUMN qty INTEGER DEFAULT 1`)
	db.Exec(`ALTER TABLE signals ADD COLUMN market_state TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE signals ADD COLUMN live_mode INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE signals ADD COLUMN profit_cap INTEGER DEFAULT 0`)

	log.Printf("[signal_journal] opened at %s", dbPath)
	return &SignalJournal{db: db}, nil
}

// SignalRecord represents a row in the signals table.
type SignalRecord struct {
	ID          int64  `json:"id"`
	Strategy    string `json:"strategy"`
	Action      string `json:"action"`
	Side        string `json:"side"`
	MarketState string `json:"market_state"`
	Token       string `json:"token"`
	Exchange    string `json:"exchange"`
	Reason      string `json:"reason"`
	EMAValues   string `json:"ema_values"`
	Price       int64  `json:"price"`
	Qty         int64  `json:"qty"`
	LiveMode    bool   `json:"live_mode"`
	ProfitCap   bool   `json:"profit_cap"`
	CandleTS    string `json:"candle_ts"`
	CreatedAt   string `json:"created_at"`
}

// EMASnapshot captures the EMA values at signal time for audit.
type EMASnapshot struct {
	EMA6  float64 `json:"ema6,omitempty"`
	EMA9  float64 `json:"ema9,omitempty"`
	EMA21 float64 `json:"ema21,omitempty"`
}

// Record persists a signal to the journal. Duplicate signals
// (same strategy+token+exchange+action+candle_ts) are silently ignored.
func (j *SignalJournal) Record(sig Signal, candleTS time.Time, emas *EMASnapshot, liveMode bool, profitCap bool) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	var emaJSON string
	if emas != nil {
		b, _ := json.Marshal(emas)
		emaJSON = string(b)
	}

	qty := sig.Qty
	if qty <= 0 {
		qty = 1
	}

	liveModeInt := 0
	if liveMode {
		liveModeInt = 1
	}

	profitCapInt := 0
	if profitCap {
		profitCapInt = 1
	}

	_, err := j.db.Exec(
		`INSERT OR IGNORE INTO signals (strategy, action, side, market_state, token, exchange, reason, ema_values, price, qty, live_mode, profit_cap, candle_ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sig.StrategyName,
		string(sig.Action),
		string(sig.Side),
		sig.MarketState,
		sig.Token,
		sig.Exchange,
		sig.Reason,
		emaJSON,
		sig.Price,
		qty,
		liveModeInt,
		profitCapInt,
		candleTS.Format(time.RFC3339),
	)
	return err
}

// GetSignals returns the last N signals, newest first.
func (j *SignalJournal) GetSignals(limit int) ([]SignalRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	rows, err := j.db.Query(
		`SELECT id, strategy, action, COALESCE(side,''), COALESCE(market_state,''), token, exchange, reason, COALESCE(ema_values,''), COALESCE(price,0),
		        CASE WHEN qty IS NULL OR qty <= 0 THEN 1 ELSE qty END,
		        COALESCE(live_mode,0),
		        COALESCE(profit_cap,0),
		        candle_ts, created_at
		 FROM signals ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []SignalRecord
	for rows.Next() {
		var r SignalRecord
		var liveModeInt, profitCapInt int
		if err := rows.Scan(&r.ID, &r.Strategy, &r.Action, &r.Side, &r.MarketState, &r.Token, &r.Exchange,
			&r.Reason, &r.EMAValues, &r.Price, &r.Qty, &liveModeInt, &profitCapInt, &r.CandleTS, &r.CreatedAt); err != nil {
			continue
		}
		r.LiveMode = liveModeInt == 1
		r.ProfitCap = profitCapInt == 1
		records = append(records, r)
	}
	return records, nil
}

// Close closes the journal database.
func (j *SignalJournal) Close() error {
	return j.db.Close()
}
