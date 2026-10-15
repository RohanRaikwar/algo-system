package archiver

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/markethours"
)

// ErrNoArchive means MEGA holds no file for that source and day: a
// weekend, a holiday, or a day before archiving began.
var ErrNoArchive = errors.New("no archive for day")

// dayDir is the remote folder of an IST day: <root>/YYYY/MM/DD.
func dayDir(root string, day time.Time) []string {
	ist := day.In(markethours.IST)
	return []string{strings.Trim(root, "/"), ist.Format("2006"), ist.Format("01"), ist.Format("02")}
}

// dayFileName is the remote name of one source's day file.
func dayFileName(source string, day time.Time) string {
	return fmt.Sprintf("%s-%s.db.gz", source, dayKey(day))
}

// dayFetcher downloads day files from MEGA and keeps them unpacked in a
// local cache dir, evicting the least recently used past capBytes.
// Concurrent requests for one file share a single download.
type dayFetcher struct {
	store    Downloader
	root     string
	dir      string
	capBytes int64 // 0 = no eviction

	mu       sync.Mutex
	inflight map[string]*fetchCall
}

type fetchCall struct {
	done chan struct{}
	path string
	err  error
}

func newDayFetcher(store Downloader, root, dir string, capBytes int64) *dayFetcher {
	return &dayFetcher{store: store, root: root, dir: dir, capBytes: capBytes, inflight: map[string]*fetchCall{}}
}

// fetch returns the local path of source's unpacked SQLite file for day,
// downloading it when not cached. It returns ErrNoArchive when MEGA has
// no such file.
func (f *dayFetcher) fetch(ctx context.Context, source string, day time.Time) (string, error) {
	name := dayFileName(source, day)
	local := filepath.Join(f.dir, strings.TrimSuffix(name, ".gz"))
	if _, err := os.Stat(local); err == nil {
		now := time.Now()
		_ = os.Chtimes(local, now, now) // mark recently used
		return local, nil
	}

	f.mu.Lock()
	if c, ok := f.inflight[local]; ok {
		f.mu.Unlock()
		select {
		case <-c.done:
			return c.path, c.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	c := &fetchCall{done: make(chan struct{})}
	f.inflight[local] = c
	f.mu.Unlock()

	c.path, c.err = f.download(ctx, day, name, local)
	f.mu.Lock()
	delete(f.inflight, local)
	f.mu.Unlock()
	close(c.done)
	if c.err == nil {
		f.evict(local)
	}
	return c.path, c.err
}

func (f *dayFetcher) download(ctx context.Context, day time.Time, name, local string) (string, error) {
	dir := dayDir(f.root, day)
	files, err := f.store.List(ctx, dir)
	if err != nil {
		return "", err
	}
	var size int64 = -1
	for _, rf := range files {
		if !rf.IsDir && rf.Name == name {
			size = rf.Size
			break
		}
	}
	if size < 0 {
		return "", ErrNoArchive
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return "", err
	}
	gz := local + ".gz.part"
	defer os.Remove(gz)
	if err := f.store.Download(ctx, dir, name, gz); err != nil {
		return "", err
	}
	if got := fileSize(gz); got != size {
		return "", fmt.Errorf("download %s: got %d bytes, want %d", name, got, size)
	}
	part := local + ".part"
	defer os.Remove(part)
	if err := gunzipFile(gz, part); err != nil {
		return "", fmt.Errorf("gunzip %s: %w", name, err)
	}
	if err := os.Rename(part, local); err != nil {
		return "", err
	}
	log.Printf("[archiver] fetched mega:/%s/%s (%s)", strings.Join(dir, "/"), name, mb(size))
	return local, nil
}

// evict removes the least recently used cached files until the cache fits
// capBytes, never the file just fetched.
func (f *dayFetcher) evict(keep string) {
	if f.capBytes <= 0 {
		return
	}
	paths, _ := filepath.Glob(filepath.Join(f.dir, "*.db"))
	type entry struct {
		path string
		size int64
		used time.Time
	}
	var all []entry
	var total int64
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		all = append(all, entry{p, st.Size(), st.ModTime()})
		total += st.Size()
	}
	sort.Slice(all, func(i, j int) bool { return all[i].used.Before(all[j].used) })
	for _, e := range all {
		if total <= f.capBytes {
			return
		}
		if e.path == keep {
			continue
		}
		if os.Remove(e.path) == nil {
			total -= e.size
		}
	}
}

// gunzipFile decompresses src into dst.
func gunzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer zr.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, zr); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// FetchDayFolder downloads every file of one IST day folder into outDir,
// unpacking .db.gz files to plain SQLite. It returns the local paths.
func FetchDayFolder(ctx context.Context, cfg Config, day time.Time, outDir string) ([]string, error) {
	store := newMegaStore(cfg.MegaEmail, cfg.MegaPassword)
	dir := dayDir(cfg.MegaRootDir, day)
	files, err := store.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	var out []string
	for _, rf := range files {
		if rf.IsDir {
			continue
		}
		dst := filepath.Join(outDir, rf.Name)
		if err := store.Download(ctx, dir, rf.Name, dst); err != nil {
			return out, err
		}
		if strings.HasSuffix(rf.Name, ".gz") {
			plain := strings.TrimSuffix(dst, ".gz")
			if err := gunzipFile(dst, plain); err != nil {
				return out, fmt.Errorf("gunzip %s: %w", rf.Name, err)
			}
			os.Remove(dst)
			dst = plain
		}
		out = append(out, dst)
	}
	if len(out) == 0 {
		return nil, ErrNoArchive
	}
	return out, nil
}
