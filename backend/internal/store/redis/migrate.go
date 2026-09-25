package redis

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// tfStreamKeyRe matches TF candle streams "candle:<tf>s:<exchange>:<token>"
// (not latest keys or legacy copies). 1s streams use auto IDs by design.
var tfStreamKeyRe = regexp.MustCompile(`^candle:([0-9]+)s:[^:]+:[^:]+$`)

// MigrateLegacyTFStreams is a one-time, idempotent startup migration. A TF
// stream whose newest entry has an auto (write-time) ID makes every explicit
// "<bucket ms>-0" ID "too small", so writes fall back to auto IDs forever
// (no retry dedup, broken ID-based pagination). Each such stream is renamed
// to "<key>:legacy:<unix ms>" (data kept for inspection) and its consumer
// groups are recreated at 0 on the fresh key in the same transaction, so
// readers neither hit NOGROUP nor skip new candles. Historical backfill on
// the original key is lost once. Returns the number of streams migrated.
// Must run before the writers start.
func (w *Writer) MigrateLegacyTFStreams(ctx context.Context) (int, error) {
	migrated := 0
	var cursor uint64
	for {
		keys, next, err := w.client.ScanType(ctx, cursor, "candle:*", 500, "stream").Result()
		if err != nil {
			return migrated, fmt.Errorf("scan TF streams: %w", err)
		}
		for _, key := range keys {
			m := tfStreamKeyRe.FindStringSubmatch(key)
			if m == nil || m[1] == "1" {
				continue
			}
			ok, err := w.migrateLegacyTFStream(ctx, key)
			if err != nil {
				return migrated, err
			}
			if ok {
				migrated++
			}
		}
		cursor = next
		if cursor == 0 {
			return migrated, nil
		}
	}
}

// migrateLegacyTFStream migrates key if its top entry has an auto ID.
func (w *Writer) migrateLegacyTFStream(ctx context.Context, key string) (bool, error) {
	msgs, err := w.client.XRevRangeN(ctx, key, "+", "-", 1).Result()
	if err != nil {
		return false, fmt.Errorf("top of %s: %w", key, err)
	}
	if len(msgs) == 0 {
		return false, nil
	}
	top, _, err := w.streamTop(ctx, key)
	if err != nil {
		log.Printf("[redis] migrate: %v (skipped)", err)
		return false, nil
	}
	if msgs[0].ID == strconv.FormatInt(top.UnixMilli(), 10)+"-0" {
		return false, nil // already on explicit IDs
	}

	groups, err := w.streamGroupNames(ctx, key)
	if err != nil {
		return false, err
	}
	legacyKey := key + ":legacy:" + strconv.FormatInt(time.Now().UnixMilli(), 10)
	pipe := w.client.TxPipeline()
	pipe.RenameNX(ctx, key, legacyKey)
	for _, g := range groups {
		pipe.XGroupCreateMkStream(ctx, key, g, "0")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("migrate %s: %w", key, err)
	}
	log.Printf("[redis] 🔧 migrated legacy auto-ID TF stream %s -> %s (%d consumer groups recreated; history kept on the legacy key)", key, legacyKey, len(groups))
	return true, nil
}

// streamGroupNames lists key's consumer group names. Raw XINFO GROUPS: the
// go-redis v8 helper cannot parse Redis 7's longer reply.
func (w *Writer) streamGroupNames(ctx context.Context, key string) ([]string, error) {
	res, err := w.client.Do(ctx, "XINFO", "GROUPS", key).Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return nil, nil
		}
		return nil, fmt.Errorf("groups of %s: %w", key, err)
	}
	rows, _ := res.([]interface{})
	var names []string
	for _, row := range rows {
		fields, _ := row.([]interface{})
		for i := 0; i+1 < len(fields); i += 2 {
			if k, _ := fields[i].(string); k == "name" {
				if name, ok := fields[i+1].(string); ok {
					names = append(names, name)
				}
			}
		}
	}
	return names, nil
}
