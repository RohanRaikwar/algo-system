package sqlite

import (
	"path/filepath"
	"testing"
)

// Each token gets its own latest N candles — not N rows shared across all.
func TestReadRecentTFCandles_PerToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	w, err := New(WriterConfig{DBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, tok := range []string{"A", "B"} {
		for i := int64(1); i <= 5; i++ {
			if _, err := w.db.Exec(`INSERT INTO candles_tf (token, exchange, tf, ts, open, high, low, close, volume, count)
				VALUES (?, 'NSE', 60, ?, 1, 1, 1, ?, 0, 1)`, tok, i*60, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := NewReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, err := r.ReadRecentTFCandles(60, 3)
	if err != nil {
		t.Fatal(err)
	}
	per := map[string][]int64{}
	for _, c := range got {
		per[c.Token] = append(per[c.Token], c.Close)
	}
	for _, tok := range []string{"A", "B"} {
		if c := per[tok]; len(c) != 3 || c[0] != 3 || c[2] != 5 {
			t.Fatalf("token %s closes %v, want latest 3 in order [3 4 5]", tok, c)
		}
	}
}

// Paging back skips the overnight gap: the page is the latest candles
// before the cursor, however far back they are, never ones after it.
func TestReadTFCandlesBefore_AcrossGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	w, err := New(WriterConfig{DBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Five candles on day 1 (close 0..4), five a day later (close 5..9).
	for i, ts := range []int64{60, 120, 180, 240, 300, 86400 + 60, 86400 + 120, 86400 + 180, 86400 + 240, 86400 + 300} {
		if _, err := w.db.Exec(`INSERT INTO candles_tf (token, exchange, tf, ts, open, high, low, close, volume, count)
			VALUES ('A', 'NSE', 60, ?, 1, 1, 1, ?, 0, 1)`, ts, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, err := r.ReadTFCandlesBefore("NSE", "A", 60, 86400+60, 3)
	if err != nil {
		t.Fatal(err)
	}
	var closes []int64
	for _, c := range got {
		closes = append(closes, c.Close)
	}
	if len(closes) != 3 || closes[0] != 2 || closes[2] != 4 {
		t.Fatalf("closes %v, want day 1's last 3 in order [2 3 4]", closes)
	}
}
