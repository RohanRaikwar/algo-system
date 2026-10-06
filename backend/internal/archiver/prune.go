package archiver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"syscall"
)

// pruneChunk bounds each DELETE transaction so the writer lock is never
// held long enough to stall a service's batch insert past its busy_timeout.
const pruneChunk = 50000

// pruneBefore deletes rows of table with ts < cutoff, in chunks. It
// returns the number of rows deleted.
func pruneBefore(ctx context.Context, db *sql.DB, table string, cutoff int64) (int64, error) {
	exists, err := tableExists(ctx, db, table)
	if err != nil || !exists {
		return 0, err
	}
	var total int64
	for {
		res, err := db.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE rowid IN (SELECT rowid FROM `+table+` WHERE ts < ? LIMIT ?)`,
			cutoff, pruneChunk)
		if err != nil {
			return total, fmt.Errorf("prune %s: %w", table, err)
		}
		n, _ := res.RowsAffected()
		total += n
		if n < pruneChunk {
			return total, nil
		}
	}
}

// compact checkpoints the WAL and VACUUMs so deleted pages return to the
// filesystem; a DELETE alone never shrinks a SQLite file. VACUUM builds
// the new copy in a temp file and writes it back through the WAL, so the
// volume needs up to the file's current size free; it is skipped (with an
// error, nothing changed) when it is too full.
func compact(ctx context.Context, db *sql.DB, path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	free, err := freeBytes(path)
	if err != nil {
		return err
	}
	if free < uint64(st.Size()) {
		return fmt.Errorf("skip VACUUM of %s: %d bytes free, need %d", path, free, st.Size())
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuum %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	return nil
}

func freeBytes(path string) (uint64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, err
	}
	return fs.Bavail * uint64(fs.Bsize), nil
}
