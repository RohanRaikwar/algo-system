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
