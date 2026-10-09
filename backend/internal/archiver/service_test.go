package archiver

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/markethours"
)

// fakeUploader keeps uploaded files in a local dir.
type fakeUploader struct {
	dir   string
	fail  bool
	names []string
}

func (f *fakeUploader) Upload(_ context.Context, localPath string, dir []string, name string, localSize int64) (int64, error) {
	if f.fail {
		return 0, errors.New("mega down")
	}
	dst := filepath.Join(append([]string{f.dir}, append(dir, name)...)...)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	b, err := os.ReadFile(localPath)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return 0, err
	}
	f.names = append(f.names, strings.Join(append(dir, name), "/"))
	return int64(len(b)), nil
}

// Tuesday 2026-10-06, 17:00 IST: after the default 16:00 run time.
var testNow = time.Date(2026, 10, 6, 17, 0, 0, 0, markethours.IST)

func istTS(day, hour int) int64 {
	return time.Date(2026, 10, day, hour, 0, 0, 0, markethours.IST).Unix()
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func count(t *testing.T, path, q string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// newTestService builds candles.db with 1s and TF rows on Sep 20 and on
// Oct 5–6, plus a journal.
func newTestService(t *testing.T, up *fakeUploader) *Service {
	t.Helper()
	dir := t.TempDir()
	candles := filepath.Join(dir, "candles.db")
	db, err := sql.Open("sqlite3", candles)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `CREATE TABLE candles_1s (token TEXT, ts INTEGER, close INTEGER)`)
	mustExec(t, db, `CREATE TABLE candles_tf (token TEXT, tf INTEGER, ts INTEGER, close INTEGER)`)
	for _, d := range []int{5, 6} {
		for h := 10; h < 15; h++ {
			mustExec(t, db, `INSERT INTO candles_1s VALUES ('99926000', ?, 2500000)`, istTS(d, h))
			mustExec(t, db, `INSERT INTO candles_tf VALUES ('99926000', 60, ?, 2500000)`, istTS(d, h))
		}
	}
	sep20 := time.Date(2026, 9, 20, 11, 0, 0, 0, markethours.IST).Unix()
	mustExec(t, db, `INSERT INTO candles_tf VALUES ('99926000', 60, ?, 2400000)`, sep20)
	db.Close()

	journal := filepath.Join(dir, "signals.db")
	jdb, _ := sql.Open("sqlite3", journal)
	mustExec(t, jdb, `CREATE TABLE signals (id INTEGER, ts INTEGER)`)
	mustExec(t, jdb, `INSERT INTO signals VALUES (1, ?)`, istTS(6, 11))
	jdb.Close()

	cfg := Config{
		Enabled: true, MegaRootDir: "algo-archive", RunAt: "16:00",
		TmpDir: filepath.Join(dir, "tmp"), ManifestPath: filepath.Join(dir, "manifest.db"),
		CandlesPath: candles, ExitwatchPath: filepath.Join(dir, "missing.db"),
		JournalPaths: []string{journal},
		Keep1sDays:   0, KeepTFDays: 10, KeepFeaturesDays: 3, WarmupDays: 5,
	}
	return &Service{cfg: cfg, uploader: up, now: func() time.Time { return testNow }}
}

func TestRunOnce_UploadsThenPrunes(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"algo-archive/2026/09/20/candles-2026-09-20.db.gz",
		"algo-archive/2026/10/05/candles-2026-10-05.db.gz",
		"algo-archive/2026/10/06/candles-2026-10-06.db.gz",
		"algo-archive/2026/10/06/signals-2026-10-06.db.gz",
	}
	if strings.Join(up.names, ",") != strings.Join(want, ",") {
		t.Fatalf("uploads = %v, want %v", up.names, want)
	}

	// The Oct 6 archive holds exactly that day's rows.
	gz, err := os.Open(filepath.Join(up.dir, "algo-archive/2026/10/06/candles-2026-10-06.db.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(gz)
	raw := filepath.Join(t.TempDir(), "day.db")
	out, _ := os.Create(raw)
	io.Copy(out, zr)
	out.Close()
	gz.Close()
	if n := count(t, raw, `SELECT COUNT(*) FROM candles_1s`); n != 5 {
		t.Fatalf("archived 1s rows = %d, want 5", n)
	}

	c := s.cfg.CandlesPath
	if n := count(t, c, `SELECT COUNT(*) FROM candles_1s`); n != 0 {
		t.Fatalf("local 1s rows = %d, want 0 (keep 0 days)", n)
	}
	// candles_tf keeps 10 days: Oct 5–6 stay, Sep 20 goes.
	if n := count(t, c, `SELECT COUNT(*) FROM candles_tf`); n != 10 {
		t.Fatalf("local tf rows = %d, want 10", n)
	}
	if n := count(t, s.cfg.ManifestPath, `SELECT COUNT(*) FROM archived`); n != 4 {
		t.Fatalf("manifest rows = %d, want 4", n)
	}

	// A second run finds everything archived and uploads nothing.
	up.names = nil
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(up.names) != 0 {
		t.Fatalf("second run uploaded %v", up.names)
	}
}

func TestRunOnce_FailedUploadDeletesNothing(t *testing.T) {
	s := newTestService(t, &fakeUploader{dir: t.TempDir(), fail: true})
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := s.cfg.CandlesPath
	if n := count(t, c, `SELECT COUNT(*) FROM candles_1s`); n != 10 {
		t.Fatalf("local 1s rows = %d, want 10", n)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM candles_tf`); n != 11 {
		t.Fatalf("local tf rows = %d, want 11", n)
	}
}

func TestRunOnce_SkipsDuringTradingWindow(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	s.now = func() time.Time { return time.Date(2026, 10, 6, 11, 0, 0, 0, markethours.IST) }
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(up.names) != 0 {
		t.Fatalf("uploaded during market hours: %v", up.names)
	}
}

func TestValidate(t *testing.T) {
	ok := Config{
		Enabled: true, MegaEmail: "a", MegaPassword: "b", MegaRootDir: "x",
		TmpDir: "t", ManifestPath: "m", RunAt: "16:00", KeepTFDays: 10, WarmupDays: 5,
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, mod := range map[string]func(*Config){
		"tf window within warmup": func(c *Config) { c.KeepTFDays = 5 },
		"run before close":        func(c *Config) { c.RunAt = "15:00" },
		"bad run time":            func(c *Config) { c.RunAt = "4pm" },
		"no credentials":          func(c *Config) { c.MegaPassword = "" },
	} {
		c := ok
		mod(&c)
		if c.Validate() == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if (Config{}).Validate() != nil {
		t.Errorf("disabled config should always validate")
	}
}
