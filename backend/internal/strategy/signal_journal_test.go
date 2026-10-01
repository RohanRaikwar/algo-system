package strategy

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalRecordsTradedOption(t *testing.T) {
	j, err := NewSignalJournal(filepath.Join(t.TempDir(), "signals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	sig := Signal{
		StrategyName: "NIFTY50_RANGE", Action: ActionBuy, Side: SidePut,
		Token: "99926000", Exchange: "NSE", Price: 11040, Qty: 65,
		FNOToken: "45678", FNOSymbol: "NIFTY06OCT2622500PE",
	}
	if err := j.Record(sig, time.Date(2026, 10, 1, 6, 45, 1, 0, time.UTC), nil, false, false); err != nil {
		t.Fatal(err)
	}
	recs, err := j.GetSignals(10)
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs=%v err=%v", recs, err)
	}
	if recs[0].FNOToken != "45678" || recs[0].FNOSymbol != "NIFTY06OCT2622500PE" {
		t.Fatalf("fno fields = %q %q", recs[0].FNOToken, recs[0].FNOSymbol)
	}
}

// A journal created before the fno columns gains them on open.
func TestJournalMigratesFNOColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Skipf("sqlite3 driver: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE signals (id INTEGER PRIMARY KEY AUTOINCREMENT, strategy TEXT NOT NULL, action TEXT NOT NULL,
		token TEXT NOT NULL, exchange TEXT NOT NULL, reason TEXT, ema_values TEXT, candle_ts DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	j, err := NewSignalJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err := j.Record(Signal{StrategyName: "S", Action: ActionExit, Side: SideCall, Token: "1", Exchange: "NSE", FNOSymbol: "X"}, time.Now(), nil, false, false); err != nil {
		t.Fatal(err)
	}
	if recs, _ := j.GetSignals(1); len(recs) != 1 || recs[0].FNOSymbol != "X" {
		t.Fatalf("recs = %+v", recs)
	}
}
