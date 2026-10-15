package backfill

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/store/sqlite"
)

// fakeAPI serves a full 375-minute session for every requested day, except
// days listed in fail.
type fakeAPI struct {
	calls []string
	fail  map[string]bool
}

func (f *fakeAPI) GetCandleData(p map[string]any) (map[string]any, error) {
	key := fmt.Sprintf("%v %v", p["symboltoken"], p["fromdate"])
	f.calls = append(f.calls, key)
	day := p["fromdate"].(string)[:10]
	if f.fail[day] {
		return nil, errors.New("AB1004")
	}
	d, _ := time.ParseInLocation("2006-01-02 15:04", day+" 09:15", markethours.IST)
	var rows []any
	for i := 0; i < 375; i++ {
		ts := d.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		px := 84200.0 + float64(i)
		rows = append(rows, []any{ts, px, px + 5, px - 5, px + 1, 0.0})
	}
	return map[string]any{"status": true, "data": rows}, nil
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "candles.db")
	w, err := sqlite.New(sqlite.WriterConfig{DBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Thu 15 Oct 2026, 10:00 IST: the 3 trading days before are Oct 12–14.
var now = func() time.Time { return time.Date(2026, 10, 15, 10, 0, 0, 0, markethours.IST) }

func TestRun_FillsEveryUnderlying(t *testing.T) {
	t.Setenv("UNDERLYINGS", "NIFTY,SENSEX")
	db := testDB(t)
	api := &fakeAPI{}
	n, err := Run(context.Background(), Config{Days: 3, TFs: []int{60, 300, 3600}, Now: now}, api, db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2*3*375 || len(api.calls) != 6 {
		t.Fatalf("inserted %d with %d calls, want %d / 6", n, len(api.calls), 2*3*375)
	}
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE token='99919000' AND exchange='BSE' AND tf=60`); c != 3*375 {
		t.Fatalf("SENSEX 1m rows = %d", c)
	}
	// 375 minutes from 09:15 → 75 five-minute bars a day, aligned to ts%300.
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE token='99919000' AND tf=300`); c != 3*75 {
		t.Fatalf("SENSEX 5m rows = %d", c)
	}
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE tf=300 AND ts % 300 != 0`); c != 0 {
		t.Fatalf("misaligned 5m bars: %d", c)
	}
	// Today (Oct 15) is left to the live feed.
	today := time.Date(2026, 10, 15, 0, 0, 0, 0, markethours.IST).Unix()
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE ts >= ?`, today); c != 0 {
		t.Fatalf("today backfilled: %d rows", c)
	}
	// Prices are paise: first SENSEX bar 84200.00 → 8420000.
	var open int64
	db.QueryRow(`SELECT open FROM candles_tf WHERE token='99919000' AND tf=60 ORDER BY ts LIMIT 1`).Scan(&open)
	if open != 8420000 {
		t.Fatalf("open = %d paise", open)
	}

	// Every filled day is marked for the archiver.
	if c := count(t, db, `SELECT COUNT(*) FROM archive_dirty`); c != 3 {
		t.Fatalf("dirty days = %d, want 3", c)
	}

	// A second run finds complete days and calls nothing.
	api.calls = nil
	if n, _ := Run(context.Background(), Config{Days: 3, TFs: []int{60, 300}, Now: now}, api, db); n != 0 || len(api.calls) != 0 {
		t.Fatalf("second run: %d rows, %v calls", n, api.calls)
	}
}

func TestRun_LiveRowsWinAndFailuresSkip(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	db := testDB(t)
	// A live 1m row already at 09:15 on Oct 13.
	ts := time.Date(2026, 10, 13, 9, 15, 0, 0, markethours.IST).Unix()
	if _, err := db.Exec(`INSERT INTO candles_tf VALUES ('99926000','NSE',60,?,1,2,3,4,0,60)`, ts); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{fail: map[string]bool{"2026-10-12": true}}
	if _, err := Run(context.Background(), Config{Days: 3, TFs: []int{60}, Now: now}, api, db); err != nil {
		t.Fatal(err)
	}
	var open int64
	db.QueryRow(`SELECT open FROM candles_tf WHERE token='99926000' AND tf=60 AND ts=?`, ts).Scan(&open)
	if open != 1 {
		t.Fatalf("live row overwritten: open=%d", open)
	}
	// Oct 12 failed, Oct 13–14 filled.
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE token='99926000' AND tf=60`); c != 2*375 {
		t.Fatalf("NIFTY rows = %d, want 750", c)
	}
}

func TestAggregate_OHLC(t *testing.T) {
	bars := []candle{
		{ts: 600, open: 10, high: 15, low: 9, close: 12, volume: 1},
		{ts: 660, open: 12, high: 20, low: 8, close: 18, volume: 2},
		{ts: 900, open: 18, high: 19, low: 17, close: 17, volume: 3},
	}
	got := aggregate(bars, 300)
	if len(got) != 2 {
		t.Fatalf("bars = %+v", got)
	}
	if b := got[0]; b.ts != 600 || b.open != 10 || b.high != 20 || b.low != 8 || b.close != 18 || b.volume != 3 || b.count != 120 {
		t.Fatalf("first 5m bar = %+v", b)
	}
}
