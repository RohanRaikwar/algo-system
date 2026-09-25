package indengine

import (
	"context"
	"log"
	"strconv"
	"time"

	"trading-systemv1/internal/indicator"
)

// snapshotLoop periodically saves engine state to Redis and SQLite.
func (svc *Service) snapshotLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(svc.cfg.SnapshotIntervalS) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var snap *indicator.EngineSnapshot
			var err error
			if oerr := svc.onEngine(ctx, func() { snap, err = svc.snapshotLocked() }); oerr != nil {
				return
			}
			if err != nil {
				log.Printf("[indengine] snapshot error: %v", err)
				continue
			}

			// Save to Redis
			if err := svc.redisReader.WriteSnapshot(ctx, svc.cfg.SnapshotKey, snap); err != nil {
				log.Printf("[indengine] redis snapshot write error: %v", err)
			}

			// Save to SQLite
			if svc.sqlWriter != nil {
				if err := svc.sqlWriter.SaveSnapshot(snap); err != nil {
					log.Printf("[indengine] sqlite snapshot write error: %v", err)
				}
			}

			log.Printf("[indengine] ✅ checkpoint saved (%d tokens)", len(snap.Tokens))
		}
	}
}

// snapshotLocked captures the engine state together with the per-stream
// checkpoint of the last entry actually processed, so on restart the delta
// replay resumes exactly where this state ends — not at the consumer
// group's delivery offset, which runs ahead of candles still queued.
// Owner only.
func (svc *Service) snapshotLocked() (*indicator.EngineSnapshot, error) {
	snap, err := indicator.SnapshotEngine(svc.engine, svc.getLastStreamID())
	if err != nil {
		return nil, err
	}
	if len(svc.lastProcessed) > 0 {
		snap.StreamIDs = make(map[string]string, len(svc.lastProcessed))
		for k, v := range svc.lastProcessed {
			snap.StreamIDs[k] = v
		}
		snap.Version = 2
	}
	return snap, nil
}

// getLastStreamID returns a time-based stream ID marker for snapshots.
func (svc *Service) getLastStreamID() string {
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + "-0"
}
