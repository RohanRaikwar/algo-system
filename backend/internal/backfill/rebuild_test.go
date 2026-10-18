package backfill

import (
	"context"
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
)

func TestRebuild_AddsMissingTFsFromStoredMinutes(t *testing.T) {
	t.Setenv("UNDERLYINGS", "")
	db := testDB(t)
	if _, err := Run(context.Background(), Config{Days: 2, TFs: []int{60, 300}, Now: now}, &fakeAPI{}, db); err != nil {
		t.Fatal(err)
	}
	n, err := Rebuild(context.Background(), Config{Days: 2, TFs: []int{60, 300, 600, 900, 1800}, Now: now}, db)
	if err != nil {
		t.Fatal(err)
	}
	// 375 minutes from 09:15: 38 ten-minute, 25 fifteen-minute, 13 thirty-minute bars a day.
	if n != 2*(38+25+13) {
		t.Fatalf("rebuilt %d", n)
	}
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE tf=900`); c != 2*25 {
		t.Fatalf("15m rows = %d", c)
	}
	// 10m/30m start at the 09:15 open (model.TFBucket): 09:15, 09:45, …
	first := time.Date(2026, 10, 13, 9, 15, 0, 0, markethours.IST).Unix()
	for _, tf := range []int{600, 1800} {
		if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE tf=? AND ts=?`, tf, first); c != 1 {
			t.Fatalf("%ds bar does not start at 09:15", tf)
		}
	}
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE tf=1800 AND count != 1800`); c != 2 {
		t.Fatalf("30m bars not full except the 15:15 one: %d", c)
	}
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE tf=300`); c != 2*75 {
		t.Fatalf("existing 5m rows changed: %d", c)
	}
	// Complete days are left alone.
	if n, _ := Rebuild(context.Background(), Config{Days: 2, TFs: []int{600, 900, 1800}, Now: now}, db); n != 0 {
		t.Fatalf("second rebuild %d", n)
	}

	// 30m rows stored on clock boundaries (09:00, 09:30, …) are rebuilt on the open.
	old := time.Date(2026, 10, 14, 9, 0, 0, 0, markethours.IST).Unix()
	if _, err := db.Exec(`DELETE FROM candles_tf WHERE tf=1800 AND ts >= ?`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO candles_tf VALUES ('99926000','NSE',1800,?,1,2,1,2,0,60)`, old); err != nil {
		t.Fatal(err)
	}
	if n, _ := Rebuild(context.Background(), Config{Days: 2, TFs: []int{1800}, Now: now}, db); n != 13 {
		t.Fatalf("realigned %d, want 13", n)
	}
	if c := count(t, db, `SELECT COUNT(*) FROM candles_tf WHERE tf=1800 AND (ts - 900) % 1800 != 0`); c != 0 {
		t.Fatalf("misaligned 30m rows left: %d", c)
	}
}
