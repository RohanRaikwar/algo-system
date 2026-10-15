package archiver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/store/sqlite"
	"trading-systemv1/internal/underlying"
)

// Restore refills a fresh volume (a new Railway account or a lost disk)
// from MEGA. It runs before the services start. A fresh volume is one with
// no candles; otherwise it returns at once without contacting MEGA, so a
// MEGA outage never delays a normal start. On a fresh volume, each database
// that is missing or empty is refilled: candles and exitwatch features for
// their keep window, journals whole. Live data is never overwritten. An
// empty manifest is rebuilt from the MEGA listing so the daily run does
// not upload restored days again.
func (s *Service) Restore(ctx context.Context) error {
	srcs := s.daySources()
	fresh, err := sourceEmpty(ctx, srcs[0]) // candles
	if err != nil {
		return fmt.Errorf("%s: %w", srcs[0].name, err)
	}
	if !fresh {
		log.Println("[archiver] restore: local candles present, nothing to restore")
		return nil
	}
	var todo []daySource
	for _, src := range srcs {
		empty, err := sourceEmpty(ctx, src)
		if err != nil {
			return fmt.Errorf("%s: %w", src.name, err)
		}
		if empty {
			todo = append(todo, src)
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.ManifestPath), 0o755); err != nil {
		return err
	}
	man, err := openManifest(s.cfg.ManifestPath)
	if err != nil {
		return err
	}
	defer man.Close()
	manEmpty, err := man.empty(ctx)
	if err != nil {
		return err
	}

	days, err := s.remoteDays(ctx)
	if err != nil {
		return fmt.Errorf("list mega: %w", err)
	}
	log.Printf("[archiver] restore: %d archived days in mega:/%s", len(days), s.cfg.MegaRootDir)

	if manEmpty {
		n, err := s.rebuildManifest(ctx, man, days)
		if err != nil {
			return fmt.Errorf("rebuild manifest: %w", err)
		}
		log.Printf("[archiver] restore: manifest rebuilt with %d entries", n)
	}

	tmp := filepath.Join(s.cfg.TmpDir, "restore")
	defer os.RemoveAll(tmp)
	f := newDayFetcher(s.store, s.cfg.MegaRootDir, tmp, 0)
	for _, src := range todo {
		if err := s.restoreSource(ctx, f, src, days); err != nil {
			log.Printf("[archiver] ❌ restore %s: %v", src.name, err)
		}
	}
	return nil
}

// remoteDay is one YYYY/MM/DD folder and its files.
type remoteDay struct {
	day   time.Time
	files map[string]int64
}

// remoteDays lists every day folder under the root, oldest first.
func (s *Service) remoteDays(ctx context.Context) ([]remoteDay, error) {
	root := strings.Trim(s.cfg.MegaRootDir, "/")
	var out []remoteDay
	years, err := s.store.List(ctx, []string{root})
	if err != nil {
		return nil, err
	}
	for _, y := range numericDirs(years) {
		months, err := s.store.List(ctx, []string{root, y})
		if err != nil {
			return nil, err
		}
		for _, m := range numericDirs(months) {
			ds, err := s.store.List(ctx, []string{root, y, m})
			if err != nil {
				return nil, err
			}
			for _, d := range numericDirs(ds) {
				day, err := time.ParseInLocation("2006/01/02", y+"/"+m+"/"+d, markethours.IST)
				if err != nil {
					continue
				}
				files, err := s.store.List(ctx, []string{root, y, m, d})
				if err != nil {
					return nil, err
				}
				rd := remoteDay{day: day, files: map[string]int64{}}
				for _, rf := range files {
					if !rf.IsDir {
						rd.files[rf.Name] = rf.Size
					}
				}
				out = append(out, rd)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].day.Before(out[j].day) })
	return out, nil
}

func numericDirs(entries []RemoteFile) []string {
	var out []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name); e.IsDir && err == nil {
			out = append(out, e.Name)
		}
	}
	return out
}

// rebuildManifest records every known source file found in MEGA. Rows are
// unknown from a listing and recorded as -1.
func (s *Service) rebuildManifest(ctx context.Context, man *manifest, days []remoteDay) (int, error) {
	n := 0
	for _, rd := range days {
		for _, src := range s.daySources() {
			name := dayFileName(src.name, rd.day)
			size, ok := rd.files[name]
			if !ok {
				continue
			}
			if err := man.record(ctx, manifestEntry{
				Source: src.name, Day: dayKey(rd.day),
				RemotePath: strings.Join(append(dayDir(s.cfg.MegaRootDir, rd.day), name), "/"),
				Rows:       -1, Bytes: size,
			}); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// restoreSource merges src's day files into its local database. Tables
// with a keep window take only rows inside it, counted back from the
// newest archived day; never-pruned tables take every day.
func (s *Service) restoreSource(ctx context.Context, f *dayFetcher, src daySource, days []remoteDay) error {
	var have []remoteDay
	for _, rd := range days {
		if _, ok := rd.files[dayFileName(src.name, rd.day)]; ok {
			have = append(have, rd)
		}
	}
	if len(have) == 0 {
		log.Printf("[archiver] restore %s: nothing in MEGA", src.name)
		return nil
	}
	_, newestEnd := dayBounds(have[len(have)-1].day)
	cutoff := map[string]int64{}
	var earliest int64 = 1<<63 - 1
	for _, t := range src.tables {
		c := int64(0)
		if t.keepDays >= 0 {
			c = newestEnd - int64(t.keepDays)*86400
		}
		cutoff[t.name] = c
		if c < earliest {
			earliest = c
		}
	}

	if err := os.MkdirAll(filepath.Dir(src.path), 0o755); err != nil {
		return err
	}
	if src.name == legacyCandles || strings.HasPrefix(src.name, "candles-") {
		// mdengine's writer owns this schema (primary keys that its
		// INSERT OR REPLACE relies on); older day files lack them.
		w, err := sqlite.New(sqlite.WriterConfig{DBPath: src.path})
		if err != nil {
			return err
		}
		w.Close()
	}
	db, err := openDB(src.path)
	if err != nil {
		return err
	}
	defer db.Close()

	var total int64
	files := 0
	for _, rd := range have {
		if _, end := dayBounds(rd.day); end <= earliest {
			continue
		}
		path, err := f.fetch(ctx, src.name, rd.day)
		if errors.Is(err, ErrNoArchive) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", dayKey(rd.day), err)
		}
		n, err := mergeDayFile(ctx, db, path, restoreTables(src), cutoff)
		os.Remove(path)
		if err != nil {
			return fmt.Errorf("%s: %w", dayKey(rd.day), err)
		}
		total += n
		files++
	}
	log.Printf("[archiver] ✅ restored %s: %d rows from %d day files", src.name, total, files)
	return nil
}

// mergeDayFile copies a day file's rows into the matching local tables,
// creating a missing table from the day file's own CREATE statement.
// Existing rows win (INSERT OR IGNORE).
func mergeDayFile(ctx context.Context, db *sql.DB, path string, tables []dayTable, cutoff map[string]int64) (int64, error) {
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS a`, "file:"+path+"?mode=ro"); err != nil {
		return 0, fmt.Errorf("attach %s: %w", path, err)
	}
	defer db.ExecContext(context.Background(), `DETACH DATABASE a`)

	var total int64
	for _, t := range tables {
		ddl, err := schemaDDL(ctx, db, "a", t.name)
		if err != nil {
			return total, err
		}
		if ddl == "" {
			continue
		}
		local, err := tableDDL(ctx, db, t.name)
		if err != nil {
			return total, err
		}
		if local == "" {
			if _, err := db.ExecContext(ctx, ddl); err != nil {
				return total, fmt.Errorf("create %s: %w", t.name, err)
			}
		}
		cols, err := tableColumns(ctx, db, "a", t.name)
		if err != nil {
			return total, err
		}
		list := `"` + strings.Join(cols, `","`) + `"`
		res, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO main.`+t.name+` (`+list+`) SELECT `+list+` FROM a.`+t.name+` WHERE `+t.cond()+` AND `+t.expr()+` >= ?`,
			cutoff[t.name])
		if err != nil {
			return total, fmt.Errorf("merge %s: %w", t.name, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, ?)`, table, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// sourceEmpty reports whether src's database is missing or holds no rows
// in any of its archived tables.
func sourceEmpty(ctx context.Context, src daySource) (bool, error) {
	if src.path == "" {
		return false, nil
	}
	if _, err := os.Stat(src.path); os.IsNotExist(err) {
		return true, nil
	}
	db, err := openDB(src.path)
	if err != nil {
		return false, err
	}
	defer db.Close()
	_, ok, err := earliestTS(ctx, db, src.tables)
	return !ok, err
}

func (m *manifest) empty(ctx context.Context) (bool, error) {
	var n int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM archived`).Scan(&n)
	return n == 0, err
}

// restoreTables is the row filter applied when a source's day files are
// merged back. Per-index candle files hold only that index; a legacy
// combined candle file (before the split) also held option candles, of
// which only the index rows are restored: no option data is kept.
func restoreTables(src daySource) []dayTable {
	if src.name != legacyCandles {
		out := make([]dayTable, len(src.tables))
		for i, t := range src.tables {
			t.where = ""
			out[i] = t
		}
		return out
	}
	var keys []string
	for _, p := range underlying.All() {
		keys = append(keys, "'"+p.IndexKey()+"'")
	}
	in := "exchange || ':' || token IN (" + strings.Join(keys, ",") + ")"
	out := make([]dayTable, len(src.tables))
	for i, t := range src.tables {
		t.where = in
		out[i] = t
	}
	return out
}
