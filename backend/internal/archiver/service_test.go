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

// List and Download read back what Upload stored, so the fake is also the
// Downloader for cold reads and restore.
func (f *fakeUploader) List(_ context.Context, dir []string) ([]RemoteFile, error) {
	if f.fail {
		return nil, errors.New("mega down")
	}
	entries, err := os.ReadDir(filepath.Join(append([]string{f.dir}, dir...)...))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []RemoteFile
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, RemoteFile{Name: e.Name(), Size: info.Size(), IsDir: e.IsDir()})
	}
	return out, nil
}

func (f *fakeUploader) Download(_ context.Context, dir []string, name, localPath string) error {
	if f.fail {
		return errors.New("mega down")
	}
	b, err := os.ReadFile(filepath.Join(append(append([]string{f.dir}, dir...), name)...))
	if err != nil {
		return err
	}
	return os.WriteFile(localPath, b, 0o644)
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
	mustExec(t, db, `CREATE TABLE candles_1s (token TEXT, exchange TEXT, ts INTEGER, open INTEGER, high INTEGER,
		low INTEGER, close INTEGER, volume INTEGER, ticks_count INTEGER, PRIMARY KEY (exchange, token, ts))`)
	mustExec(t, db, `CREATE TABLE candles_tf (token TEXT, exchange TEXT, tf INTEGER, ts INTEGER, open INTEGER,
		high INTEGER, low INTEGER, close INTEGER, volume INTEGER, count INTEGER, PRIMARY KEY (exchange, token, tf, ts))`)
	for _, d := range []int{5, 6} {
		for h := 10; h < 15; h++ {
			mustExec(t, db, `INSERT INTO candles_1s VALUES ('99926000', 'NSE', ?, 2500000, 2500000, 2500000, 2500000, 0, 1)`, istTS(d, h))
			mustExec(t, db, `INSERT INTO candles_tf VALUES ('99926000', 'NSE', 60, ?, 2500000, 2500000, 2500000, 2500000, 0, 60)`, istTS(d, h))
		}
	}
	sep20 := time.Date(2026, 9, 20, 11, 0, 0, 0, markethours.IST).Unix()
	mustExec(t, db, `INSERT INTO candles_tf VALUES ('99926000', 'NSE', 60, ?, 2400000, 2400000, 2400000, 2400000, 0, 60)`, sep20)
	db.Close()

	// Signals on Oct 5 and Oct 6; candle_ts is RFC3339 UTC text as the
	// signal journal writes it.
	journal := filepath.Join(dir, "signals.db")
	jdb, _ := sql.Open("sqlite3", journal)
	mustExec(t, jdb, `CREATE TABLE signals (id INTEGER PRIMARY KEY AUTOINCREMENT, strategy TEXT, candle_ts DATETIME NOT NULL,
		UNIQUE(strategy, candle_ts))`)
	for _, d := range []int{5, 6} {
		mustExec(t, jdb, `INSERT INTO signals (strategy, candle_ts) VALUES ('SR', ?)`,
			time.Unix(istTS(d, 11), 0).UTC().Format(time.RFC3339))
	}
	jdb.Close()

	cfg := Config{
		Enabled: true, MegaRootDir: "algo-archive", RunAt: "16:00",
		TmpDir: filepath.Join(dir, "tmp"), ManifestPath: filepath.Join(dir, "manifest.db"),
		CandlesPath: candles, ExitwatchPath: filepath.Join(dir, "missing.db"),
		JournalPaths: []string{journal},
		Keep1sDays:   0, KeepTFDays: 10, KeepFeaturesDays: 3, WarmupDays: 5,
	}
	return &Service{cfg: cfg, uploader: up, store: up, now: func() time.Time { return testNow }}
}

func TestRunOnce_UploadsThenPrunes(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"algo-archive/2026/09/20/candles-NIFTY-2026-09-20.db.gz",
		"algo-archive/2026/10/05/candles-NIFTY-2026-10-05.db.gz",
		"algo-archive/2026/10/06/candles-NIFTY-2026-10-06.db.gz",
		"algo-archive/2026/10/05/signals-2026-10-05.db.gz",
		"algo-archive/2026/10/06/signals-2026-10-06.db.gz",
	}
	if strings.Join(up.names, ",") != strings.Join(want, ",") {
		t.Fatalf("uploads = %v, want %v", up.names, want)
	}

	// The Oct 6 archive holds exactly that day's rows, with the live keys.
	raw := unpack(t, filepath.Join(up.dir, "algo-archive/2026/10/06/candles-NIFTY-2026-10-06.db.gz"))
	if n := count(t, raw, `SELECT COUNT(*) FROM candles_1s`); n != 5 {
		t.Fatalf("archived 1s rows = %d, want 5", n)
	}
	if n := count(t, raw, `SELECT COUNT(*) FROM sqlite_master WHERE name='candles_tf' AND sql LIKE '%PRIMARY KEY%'`); n != 1 {
		t.Fatalf("day file lost the candles_tf primary key")
	}
	// The journal day folder holds only that day's signal, not the whole journal.
	sig := unpack(t, filepath.Join(up.dir, "algo-archive/2026/10/06/signals-2026-10-06.db.gz"))
	if n := count(t, sig, `SELECT COUNT(*) FROM signals`); n != 1 {
		t.Fatalf("Oct 6 signals file rows = %d, want 1", n)
	}
	if n := count(t, s.cfg.JournalPaths[0], `SELECT COUNT(*) FROM signals`); n != 2 {
		t.Fatalf("local journal pruned: %d rows, want 2", n)
	}

	c := s.cfg.CandlesPath
	if n := count(t, c, `SELECT COUNT(*) FROM candles_1s`); n != 0 {
		t.Fatalf("local 1s rows = %d, want 0 (keep 0 days)", n)
	}
	// candles_tf keeps 10 days: Oct 5–6 stay, Sep 20 goes.
	if n := count(t, c, `SELECT COUNT(*) FROM candles_tf`); n != 10 {
		t.Fatalf("local tf rows = %d, want 10", n)
	}
	if n := count(t, s.cfg.ManifestPath, `SELECT COUNT(*) FROM archived`); n != 5 {
		t.Fatalf("manifest rows = %d, want 5", n)
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

// unpack gunzips a day file into a temp SQLite file.
func unpack(t *testing.T, gzPath string) string {
	t.Helper()
	gz, err := os.Open(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	zr, err := gzip.NewReader(gz)
	if err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(t.TempDir(), "day.db")
	out, _ := os.Create(raw)
	io.Copy(out, zr)
	out.Close()
	return raw
}

func TestRunOnce_ReexportsOldJournalSnapshots(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	// An older archiver recorded a whole-journal snapshot for Oct 6.
	man, err := openManifest(s.cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	man.record(context.Background(), manifestEntry{Source: "signals", Day: "2026-10-06", RemotePath: "x", Rows: 0})
	man.Close()

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(up.names, ","), "signals-2026-10-06.db.gz") {
		t.Fatalf("Oct 6 journal not re-exported per day: %v", up.names)
	}
}

func TestColdReader_PagesBackThroughArchive(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := s.cfg
	cfg.ColdCacheDir = filepath.Join(t.TempDir(), "cache")
	r := newColdReader(up, cfg)

	// Before Oct 6 10:00 IST: Oct 5's five candles, oldest first. Sep 20
	// lies past 10 days with no candles, so the walk stops before it.
	got, err := r.ReadTFCandlesBefore("NSE", "99926000", 60, istTS(6, 10), 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0].TS.Unix() != istTS(5, 10) || got[4].TS.Unix() != istTS(5, 14) {
		t.Fatalf("got %d candles %v..., want Oct 5 10:00–14:00", len(got), got)
	}

	// A walk that starts within 10 days of Sep 20 reaches it across the
	// weekend and empty days.
	got, _ = r.ReadTFCandlesBefore("NSE", "99926000", 60, sepTS(28, 10), 5)
	if len(got) != 1 || got[0].Close != 2400000 {
		t.Fatalf("Sep 20 candle not reached: %v", got)
	}

	// Cached: a second read works with MEGA down.
	up.fail = true
	got, _ = r.ReadTFCandlesBefore("NSE", "99926000", 60, istTS(6, 10), 3)
	if len(got) != 3 || got[2].TS.Unix() != istTS(5, 14) {
		t.Fatalf("cached read = %d candles", len(got))
	}

	// Past the start of the archive the walk stops with what it has.
	up.fail = false
	got, _ = r.ReadTFCandlesBefore("NSE", "99926000", 60, sepTS(20, 11), 10)
	if len(got) != 0 {
		t.Fatalf("before archive start: got %d candles", len(got))
	}
}

func TestDayFetcher_ExpiresUnusedFiles(t *testing.T) {
	dir := t.TempDir()
	f := newDayFetcher(nil, "algo-archive", dir, 0)
	old := filepath.Join(dir, "candles-2026-10-05.db")
	fresh := filepath.Join(dir, "candles-2026-10-06.db")
	live := filepath.Join(dir, "candles.db") // a live DB if the cache dir were data/
	for _, p := range []string{old, fresh, live} {
		os.WriteFile(p, []byte("x"), 0o644)
	}
	stale := time.Now().Add(-11 * time.Minute)
	os.Chtimes(old, stale, stale)
	os.Chtimes(live, stale, stale)

	f.expire(10 * time.Minute)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("file unused for 11m not expired")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("recently used file expired: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("live candles.db deleted: %v", err)
	}
}

func sepTS(day, hour int) int64 {
	return time.Date(2026, 9, day, hour, 0, 0, 0, markethours.IST).Unix()
}

func TestRestore_RefillsEmptyVolume(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// New account: empty volume.
	for _, p := range []string{s.cfg.CandlesPath, s.cfg.JournalPaths[0], s.cfg.ManifestPath} {
		os.Remove(p)
		os.Remove(p + "-wal")
		os.Remove(p + "-shm")
	}
	s.store = up
	if err := s.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := s.cfg.CandlesPath
	// candles_tf keeps 10 days back from Oct 6: Oct 5–6, not Sep 20.
	if n := count(t, c, `SELECT COUNT(*) FROM candles_tf`); n != 10 {
		t.Fatalf("restored tf rows = %d, want 10", n)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM sqlite_master WHERE name='candles_tf' AND sql LIKE '%PRIMARY KEY%'`); n != 1 {
		t.Fatalf("restored candles_tf has no primary key")
	}
	// Journals come back whole.
	if n := count(t, s.cfg.JournalPaths[0], `SELECT COUNT(*) FROM signals`); n != 2 {
		t.Fatalf("restored signals = %d, want 2", n)
	}
	if n := count(t, s.cfg.ManifestPath, `SELECT COUNT(*) FROM archived`); n != 5 {
		t.Fatalf("rebuilt manifest rows = %d, want 5", n)
	}

	// The next daily run uploads nothing again.
	up.names = nil
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(up.names) != 0 {
		t.Fatalf("run after restore uploaded %v", up.names)
	}

	// With candles present, restore does not contact MEGA at all.
	db, _ := sql.Open("sqlite3", c)
	mustExec(t, db, `DELETE FROM candles_tf WHERE ts < ?`, istTS(6, 0))
	db.Close()
	os.Remove(s.cfg.JournalPaths[0])
	up.fail = true
	if err := s.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM candles_tf`); n != 5 {
		t.Fatalf("restore touched live data: %d rows, want 5", n)
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

// A late NIFTY row backfilled into an archived NIFTY day is merged into
// that day's file when marked; an unmarked late row is not.
func TestRunOnce_ReuploadsMarkedDay(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite3", s.cfg.CandlesPath)
	mustExec(t, db, `INSERT INTO candles_tf VALUES ('99926000', 'NSE', 60, ?, 2500100, 2500100, 2500100, 2500100, 0, 60)`, istTS(5, 9))
	mustExec(t, db, `INSERT INTO candles_tf VALUES ('99926000', 'NSE', 60, ?, 2500100, 2500100, 2500100, 2500100, 0, 60)`, istTS(6, 9))
	if err := MarkDirty(context.Background(), db, "candles-NIFTY", "2026-10-05"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	up.names = nil
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(up.names, ",") != "algo-archive/2026/10/05/candles-NIFTY-2026-10-05.db.gz" {
		t.Fatalf("re-uploads = %v, want only Oct 5 NIFTY", up.names)
	}
	raw := unpack(t, filepath.Join(up.dir, "algo-archive/2026/10/05/candles-NIFTY-2026-10-05.db.gz"))
	if n := count(t, raw, `SELECT COUNT(*) FROM candles_tf`); n != 6 {
		t.Fatalf("tf rows in re-uploaded file = %d, want 6", n)
	}
	// The 1s candles already pruned locally are still in the new file.
	if n := count(t, raw, `SELECT COUNT(*) FROM candles_1s`); n != 5 {
		t.Fatalf("1s rows in re-uploaded file = %d, want 5 kept from the original", n)
	}
	if n := count(t, s.cfg.CandlesPath, `SELECT COUNT(*) FROM archive_dirty`); n != 0 {
		t.Fatalf("dirty mark not cleared")
	}
	up.names = nil
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(up.names) != 0 {
		t.Fatalf("unchanged day uploaded again: %v", up.names)
	}
}

// Each index gets its own candle file per day; SENSEX history added after
// NIFTY's days were archived is uploaded as SENSEX files, and the files
// hold only their index.
func TestRunOnce_CandlesSplitPerIndex(t *testing.T) {
	t.Setenv("INDEX_TOKEN", "NSE:99926000,BSE:99919000")
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite3", s.cfg.CandlesPath)
	mustExec(t, db, `INSERT INTO candles_tf VALUES ('99919000', 'BSE', 60, ?, 8400000, 8400000, 8400000, 8400000, 0, 60)`, istTS(5, 11))
	// An option candle (stored before options stopped) never gets a file of its own.
	mustExec(t, db, `INSERT INTO candles_tf VALUES ('1163095', 'BFO', 60, ?, 30000, 30000, 30000, 30000, 0, 60)`, istTS(5, 11))
	db.Close()

	up.names = nil
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	names := strings.Join(up.names, ",")
	if !strings.Contains(names, "2026/10/05/candles-SENSEX-2026-10-05.db.gz") || strings.Contains(names, "candles-NIFTY") ||
		strings.Contains(names, "/candles-2026") {
		t.Fatalf("uploads = %v, want the SENSEX day only (no option file)", up.names)
	}
	raw := unpack(t, filepath.Join(up.dir, "algo-archive/2026/10/05/candles-SENSEX-2026-10-05.db.gz"))
	if n := count(t, raw, `SELECT COUNT(*) FROM candles_tf WHERE token != '99919000'`); n != 0 {
		t.Fatalf("SENSEX file holds %d rows of other tokens", n)
	}
	// The option row was never uploaded and is already deleted locally.
	if n := count(t, s.cfg.CandlesPath, `SELECT COUNT(*) FROM candles_tf WHERE exchange='BFO'`); n != 0 {
		t.Fatalf("option candle still stored locally")
	}
}

// Days archived before the split (one combined "candles" file) still serve
// chart history.
func TestColdReader_FallsBackToCombinedFile(t *testing.T) {
	up := &fakeUploader{dir: t.TempDir()}
	s := newTestService(t, up)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(up.dir, "algo-archive/2026/10/05")
	if err := os.Rename(filepath.Join(day, "candles-NIFTY-2026-10-05.db.gz"), filepath.Join(day, "candles-2026-10-05.db.gz")); err != nil {
		t.Fatal(err)
	}
	cfg := s.cfg
	cfg.ColdCacheDir = filepath.Join(t.TempDir(), "cache")
	got, err := newColdReader(up, cfg).ReadTFCandlesBefore("NSE", "99926000", 60, istTS(6, 10), 5)
	if err != nil || len(got) != 5 {
		t.Fatalf("got %d candles err %v, want Oct 5 from the combined file", len(got), err)
	}
}
