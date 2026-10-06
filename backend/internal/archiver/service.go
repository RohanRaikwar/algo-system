// Package archiver moves finished days of SQLite data off the local volume
// into MEGA, then deletes the archived rows past a retention window and
// VACUUMs so the volume shrinks.
//
// It runs once a day after the close (ARCHIVE_RUN_AT, IST) and once at
// startup to catch up missed days. Local rows are deleted only for days
// recorded in the manifest, i.e. confirmed in MEGA with a matching size.
// The process never exits on error: docker-entrypoint.sh stops every
// service when any one dies.
package archiver

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trading-systemv1/internal/markethours"
)

// dayTable is a table with a unix-second ts column, archived per day.
// keepDays < 0 means archived but never pruned.
type dayTable struct {
	name     string
	keepDays int
}

// daySource is one database whose tables are exported a day at a time.
type daySource struct {
	name   string // remote file prefix and manifest key
	path   string
	tables []dayTable
}

// maxCatchUpDays bounds the per-run day loop if a table holds a bogus
// ancient ts.
const maxCatchUpDays = 400

// quietFrom is the IST minute-of-day from which no run starts on a
// trading day (pre-open login at 09:10); runs resume after RunAt.
const quietFrom = 8*60 + 45

// Service is the archiver process.
type Service struct {
	cfg      Config
	uploader Uploader
	now      func() time.Time
	backoff  []time.Duration // waits between upload attempts
}

// New builds the service. A disabled config builds a no-op service.
func New(cfg Config) *Service {
	return &Service{
		cfg:      cfg,
		uploader: newMegaUploader(cfg.MegaEmail, cfg.MegaPassword),
		now:      time.Now,
		backoff:  []time.Duration{30 * time.Second, 2 * time.Minute},
	}
}

func (s *Service) daySources() []daySource {
	return []daySource{
		{name: "candles", path: s.cfg.CandlesPath, tables: []dayTable{
			{"candles_1s", s.cfg.Keep1sDays},
			{"candles_tf", s.cfg.KeepTFDays},
		}},
		{name: "exitwatch", path: s.cfg.ExitwatchPath, tables: []dayTable{
			{"decisions", -1},
			{"features_1hz", s.cfg.KeepFeaturesDays},
		}},
	}
}

// Run blocks until ctx is cancelled. It returns nil on cancel; run errors
// are logged, never returned.
func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.Enabled {
		log.Println("[archiver] disabled (ARCHIVE_ENABLED=false); idle")
		<-ctx.Done()
		return nil
	}
	hour, minute, err := s.cfg.runAt()
	if err != nil {
		return err
	}
	log.Printf("[archiver] started: daily at %02d:%02d IST → mega:/%s, keep 1s=%dd tf=%dd features=%dd",
		hour, minute, s.cfg.MegaRootDir, s.cfg.Keep1sDays, s.cfg.KeepTFDays, s.cfg.KeepFeaturesDays)

	s.safeRun(ctx) // catch up anything missed while down
	for {
		now := s.now().In(markethours.IST)
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, markethours.IST)
		if !now.Before(next) {
			next = next.AddDate(0, 0, 1)
		}
		log.Printf("[archiver] next run %s (in %s)", next.Format("2006-01-02 15:04"), next.Sub(now).Round(time.Second))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(next.Sub(now)):
			s.safeRun(ctx)
		}
	}
}

func (s *Service) safeRun(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[archiver] ❌ run panicked: %v", r)
		}
	}()
	if err := s.RunOnce(ctx); err != nil {
		log.Printf("[archiver] ❌ run failed: %v", err)
	}
}

// RunOnce archives every finished day not yet in the manifest, prunes,
// and compacts. Per-source failures are logged and leave that source's
// unarchived data in place.
func (s *Service) RunOnce(ctx context.Context) error {
	hour, minute, err := s.cfg.runAt()
	if err != nil {
		return err
	}
	now := s.now().In(markethours.IST)
	hm := now.Hour()*60 + now.Minute()
	runAt := hour*60 + minute
	if markethours.IsTradingDay(now) && hm >= quietFrom && hm < runAt {
		log.Printf("[archiver] %s is inside the trading window; skipping until %02d:%02d", now.Format("15:04"), hour, minute)
		return nil
	}
	// The last finished day: today once past RunAt, else yesterday.
	lastDay := now
	if hm < runAt {
		lastDay = now.AddDate(0, 0, -1)
	}

	if err := os.MkdirAll(s.cfg.TmpDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(s.cfg.TmpDir)
	man, err := openManifest(s.cfg.ManifestPath)
	if err != nil {
		return err
	}
	defer man.Close()

	start := time.Now()
	for _, src := range s.daySources() {
		if err := s.archiveDaySource(ctx, man, src, lastDay); err != nil {
			log.Printf("[archiver] ❌ %s: %v", src.name, err)
		}
	}
	for _, path := range s.cfg.JournalPaths {
		if err := s.archiveSnapshot(ctx, man, path, lastDay); err != nil {
			log.Printf("[archiver] ❌ %s: %v", path, err)
		}
	}
	log.Printf("[archiver] run done in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

// archiveDaySource uploads each unarchived day of src up to lastDay, then
// prunes rows older than each table's window, never past the first day
// that failed to upload.
func (s *Service) archiveDaySource(ctx context.Context, man *manifest, src daySource, lastDay time.Time) error {
	if _, err := os.Stat(src.path); os.IsNotExist(err) {
		return nil
	}
	db, err := openDB(src.path)
	if err != nil {
		return err
	}
	defer db.Close()

	names := make([]string, len(src.tables))
	for i, t := range src.tables {
		names[i] = t.name
	}
	minTS, ok, err := earliestTS(ctx, db, names)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	_, lastEnd := dayBounds(lastDay)
	pruneLimit := lastEnd // rows before this are all safely archived
	day := time.Unix(minTS, 0).In(markethours.IST)
	for i := 0; i < maxCatchUpDays && !dayAfter(day, lastDay); i, day = i+1, day.AddDate(0, 0, 1) {
		if err := s.archiveDay(ctx, man, db, src, names, day); err != nil {
			pruneLimit, _ = dayBounds(day)
			log.Printf("[archiver] ❌ %s %s: %v (pruning stops before this day)", src.name, dayKey(day), err)
			break
		}
	}

	var deleted int64
	for _, t := range src.tables {
		if t.keepDays < 0 {
			continue
		}
		cutoff := lastEnd - int64(t.keepDays)*86400
		if cutoff > pruneLimit {
			cutoff = pruneLimit
		}
		n, err := pruneBefore(ctx, db, t.name, cutoff)
		if err != nil {
			return err
		}
		if n > 0 {
			log.Printf("[archiver] %s.%s: deleted %d rows before %s", src.name, t.name, n,
				time.Unix(cutoff, 0).In(markethours.IST).Format("2006-01-02 15:04"))
		}
		deleted += n
	}
	if deleted == 0 {
		return nil
	}
	before := fileSize(src.path)
	if err := compact(ctx, db, src.path); err != nil {
		return err
	}
	log.Printf("[archiver] %s: compacted %s → %s", src.name, mb(before), mb(fileSize(src.path)))
	return nil
}

// archiveDay exports, compresses and uploads one day of src. A day with no
// rows is skipped without a manifest entry. A day is exported once: rows
// written into it after the export (only staging writes after the close)
// are not archived before pruning.
func (s *Service) archiveDay(ctx context.Context, man *manifest, db *sql.DB, src daySource, tables []string, day time.Time) error {
	key := dayKey(day)
	if done, err := man.has(ctx, src.name, key); err != nil || done {
		return err
	}
	start, end := dayBounds(day)
	raw := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("%s-%s.db", src.name, key))
	defer os.Remove(raw)
	rows, err := exportDay(ctx, db, tables, start, end, raw)
	if err != nil {
		return err
	}
	if rows == 0 {
		return nil
	}
	return s.uploadAndRecord(ctx, man, src.name, day, raw, rows)
}

// archiveSnapshot uploads a whole-database copy of a journal for lastDay.
// Journals are never pruned: P&L analytics reads all of their history.
func (s *Service) archiveSnapshot(ctx context.Context, man *manifest, path string, lastDay time.Time) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	key := dayKey(lastDay)
	if done, err := man.has(ctx, name, key); err != nil || done {
		return err
	}
	db, err := openDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	raw := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("%s-%s.db", name, key))
	defer os.Remove(raw)
	if err := snapshotDB(ctx, db, raw); err != nil {
		return err
	}
	return s.uploadAndRecord(ctx, man, name, lastDay, raw, 0)
}

// uploadAndRecord gzips raw, uploads it to <root>/YYYY/MM/, checks the
// remote size and writes the manifest entry.
func (s *Service) uploadAndRecord(ctx context.Context, man *manifest, source string, day time.Time, raw string, rows int64) error {
	gz := raw + ".gz"
	defer os.Remove(gz)
	size, sum, err := gzipFile(raw, gz)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	ist := day.In(markethours.IST)
	dir := []string{strings.Trim(s.cfg.MegaRootDir, "/"), ist.Format("2006"), ist.Format("01")}
	name := filepath.Base(gz)

	var remote int64
	for attempt := 0; ; attempt++ {
		remote, err = s.uploader.Upload(ctx, gz, dir, name, size)
		if err == nil && remote != size {
			err = fmt.Errorf("remote size %d != local %d", remote, size)
		}
		if err == nil || attempt >= len(s.backoff) || ctx.Err() != nil {
			break
		}
		log.Printf("[archiver] upload %s attempt %d failed: %v; retrying in %s", name, attempt+1, err, s.backoff[attempt])
		select {
		case <-ctx.Done():
		case <-time.After(s.backoff[attempt]):
		}
	}
	if err != nil {
		return err
	}
	remotePath := strings.Join(append(dir, name), "/")
	if err := man.record(ctx, manifestEntry{
		Source: source, Day: dayKey(day), RemotePath: remotePath, Rows: rows, Bytes: size, SHA256: sum,
	}); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	log.Printf("[archiver] ✅ uploaded mega:/%s (%s, %d rows)", remotePath, mb(size), rows)
	return nil
}

// dayAfter reports whether a's IST calendar day is after b's.
func dayAfter(a, b time.Time) bool { return dayKey(a) > dayKey(b) }

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
