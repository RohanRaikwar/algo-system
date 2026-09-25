package indengine

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"trading-systemv1/internal/heartbeat"
	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
	sqlitestore "trading-systemv1/internal/store/sqlite"
)

// Service is the top-level orchestrator for the indicator engine.
// It wires all dependencies, manages lifecycle, and coordinates goroutines.
type Service struct {
	cfg Config

	engine      *indicator.Engine
	redisReader *redisstore.Reader
	redisWriter *redisstore.Writer
	sqlReader   *sqlitestore.Reader
	sqlWriter   *sqlitestore.Writer
	prom        *metrics.Metrics

	streams    []string
	tfCandleCh chan model.TFCandle

	// The engine is owned by one goroutine: Run during startup, then
	// processLoop. Everything else (snapshot, reload) runs on the owner
	// through cmds; see onEngine.
	cmds     chan func()
	loopDone chan struct{}

	// lastProcessed is the ID of the last entry processed per stream —
	// the restart checkpoint. Owner only.
	lastProcessed map[string]string

	// restored is the snapshot the engine was restored from (nil = cold).
	restored *indicator.EngineSnapshot
}

// New creates a new Service from the given Config.
// It connects to Redis and SQLite and restores the indicator engine.
func New(cfg Config) (*Service, error) {
	svc := &Service{
		cfg:           cfg,
		prom:          metrics.NewMetrics(),
		tfCandleCh:    make(chan model.TFCandle, 5000),
		cmds:          make(chan func()),
		loopDone:      make(chan struct{}),
		lastProcessed: make(map[string]string),
	}

	// ---- Connect to Redis ----
	var err error
	svc.redisReader, err = redisstore.NewReader(redisstore.ReaderConfig{
		Addr:          cfg.RedisAddr,
		Password:      cfg.RedisPassword,
		ConsumerGroup: cfg.ConsumerGroup,
		ConsumerName:  cfg.ConsumerName,
	})
	if err != nil {
		return nil, err
	}

	svc.redisWriter, err = redisstore.New(redisstore.WriterConfig{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	if err != nil {
		svc.redisReader.Close()
		return nil, err
	}

	// ---- Open SQLite ----
	svc.sqlReader, err = sqlitestore.NewReader(cfg.SQLitePath)
	if err != nil {
		log.Printf("[indengine] WARNING: sqlite reader init failed: %v (continuing without SQLite backfill)", err)
	}

	os.MkdirAll("data", 0o755)
	svc.sqlWriter, err = sqlitestore.New(sqlitestore.WriterConfig{DBPath: cfg.SQLitePath})
	if err != nil {
		log.Printf("[indengine] WARNING: sqlite writer init failed: %v", err)
	}

	return svc, nil
}

// Run starts all subsystems and blocks until ctx is cancelled.
func (svc *Service) Run(ctx context.Context) error {
	cfg := svc.cfg
	log.Println("[indengine] starting Indicator Engine microservice...")

	// ---- Restore engine from snapshot ----
	if err := svc.restoreEngine(ctx); err != nil {
		return err
	}

	// ---- Discover / build streams ----
	svc.streams = svc.buildStreams(ctx)
	log.Printf("[indengine] consuming from %d streams: %v", len(svc.streams), svc.streams)

	// ---- Backfill from Redis streams (only if no snapshot — cold start) ----
	// If a snapshot exists, replayDelta catches up from its checkpoint.
	// Either way a candle an indicator has already applied is skipped
	// (Engine.Process), so overlap with the SQLite warm-up is harmless.
	if !hasReplayCheckpoint(svc.restored) {
		svc.backfillFromRedis(ctx)
	} else {
		log.Printf("[indengine] snapshot exists — skipping full backfill (delta replay will catch up)")
	}

	// ---- Replay delta from snapshot ----
	svc.replayDelta(ctx)

	// ---- Hand the engine to its owner ----
	// processLoop starts before anything feeds tfCandleCh (pending recovery
	// included), so a large backlog cannot block startup on a full channel.
	go svc.processLoop(ctx)

	// ---- Ensure consumer groups ----
	if len(svc.streams) > 0 {
		if err := svc.redisReader.EnsureConsumerGroup(ctx, svc.streams); err != nil {
			log.Printf("[indengine] WARNING: consumer group setup: %v", err)
		}
	}

	// ---- Recover pending messages ----
	if len(svc.streams) > 0 {
		if err := svc.redisReader.RecoverPending(ctx, svc.streams, svc.tfCandleCh); err != nil {
			log.Printf("[indengine] pending recovery error: %v", err)
		}
	}

	// ---- Start subsystems ----
	go heartbeat.NewPublisher("indengine", svc.redisWriter.Client()).Run(ctx)
	go metrics.RunSnapshotPublisher(ctx, svc.redisWriter.Client(), metrics.SnapshotKeyIndEngine, 5*time.Second, func() any {
		snap := svc.prom.IndicatorSnapshot()
		snap.UpdatedAt = metrics.Stamp()
		return snap
	})
	svc.startPELReclaimer(ctx)
	svc.startConsumer(ctx)
	go svc.peekLoop(ctx)
	go svc.snapshotLoop(ctx)
	svc.startHTTP(ctx)
	svc.startConfigSubscriber(ctx)

	// ---- Startup banner ----
	log.Println("[indengine] ╔════════════════════════════════════════════════════════╗")
	log.Println("[indengine] ║  Indicator Engine (MS2) Active                        ║")
	log.Println("[indengine] ║                                                       ║")
	log.Println("[indengine] ║  [Redis Streams] → [Indicators] → [Redis Publish]     ║")
	log.Printf("[indengine] ║  Snapshot checkpoint every %ds                      ║", cfg.SnapshotIntervalS)
	log.Printf("[indengine] ║  TFs: %v                                   ║", cfg.EnabledTFs)
	log.Println("[indengine] ╚════════════════════════════════════════════════════════╝")
	log.Println("[indengine] ✅ all systems running. Press Ctrl+C to stop.")

	// Block until context cancelled
	<-ctx.Done()

	// ---- Graceful shutdown ----
	svc.shutdown()
	return nil
}

// shutdown saves final snapshot and closes connections.
func (svc *Service) shutdown() {
	log.Println("[indengine] shutdown signal received, saving final snapshot...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutCancel()
	// processLoop has stopped with ctx; the engine is ours again.
	<-svc.loopDone
	finalSnap, err := svc.snapshotLocked()
	if err == nil {
		if svc.redisReader != nil {
			svc.redisReader.WriteSnapshot(shutCtx, svc.cfg.SnapshotKey, finalSnap)
		}
		if svc.sqlWriter != nil {
			svc.sqlWriter.SaveSnapshot(finalSnap)
		}
		log.Println("[indengine] final snapshot saved")
	}

	if svc.sqlReader != nil {
		svc.sqlReader.Close()
	}
	if svc.sqlWriter != nil {
		svc.sqlWriter.Close()
	}
	svc.redisWriter.Close()
	svc.redisReader.Close()

	log.Println("[indengine] shutdown complete.")
}

// restoreEngine restores the indicator engine from Redis or SQLite snapshot,
// then backfills from SQLite for cold indicators.
func (svc *Service) restoreEngine(ctx context.Context) error {
	restorer := indicator.NewRestorer(svc.cfg.IndicatorConfigs)

	// Try Redis snapshot first
	snap, err := svc.redisReader.ReadSnapshot(ctx, svc.cfg.SnapshotKey)
	if err != nil {
		log.Printf("[indengine] redis snapshot read error: %v", err)
	}

	// Fallback to SQLite
	if snap == nil && svc.sqlReader != nil {
		snap, err = svc.sqlReader.ReadLatestSnapshot()
		if err != nil {
			log.Printf("[indengine] sqlite snapshot read error: %v", err)
		}
	}

	svc.engine, err = restorer.RestoreFromSnap(snap)
	if err != nil {
		return err
	}
	svc.restored = snap
	if snap != nil {
		for stream, id := range snap.StreamIDs {
			if isValidStreamID(id) {
				svc.lastProcessed[stream] = id
			}
		}
	}

	// Backfill from SQLite to warm up cold indicators. Runs after a restore
	// too: restored indicators skip candles they have already applied, and
	// any the snapshot lacks (new in config) warm up from these.
	if svc.sqlReader != nil {
		backfilled := restorer.BackfillFromSQLite(svc.engine, svc.sqlReader, func(results []model.IndicatorResult) {
			svc.redisWriter.WriteIndicatorBatch(ctx, results)
		})
		if backfilled > 0 {
			log.Printf("[indengine] warmed up indicators with %d historical candles (results written to Redis)", backfilled)
		}
	}

	return nil
}

// buildStreams discovers or constructs the Redis stream names to consume.
func (svc *Service) buildStreams(ctx context.Context) []string {
	var streams []string
	for _, tf := range svc.cfg.EnabledTFs {
		if len(svc.cfg.SubscribeTokenKeys) > 0 {
			for _, tk := range svc.cfg.SubscribeTokenKeys {
				streams = append(streams, "candle:"+strconv.Itoa(tf)+"s:"+tk)
			}
		} else {
			discovered := svc.redisReader.DiscoverTFStreams(ctx, []int{tf}, svc.cfg.SubscribeTokenKeys)
			streams = append(streams, discovered...)
		}
	}
	return streams
}

// backfillFromRedis replays all historical candles from Redis streams through the engine.
func (svc *Service) backfillFromRedis(ctx context.Context) {
	backfillCh := make(chan model.TFCandle, 5000)
	go func() {
		for _, stream := range svc.streams {
			_, err := svc.redisReader.ReplayFromID(ctx, stream, "0", backfillCh)
			if err != nil {
				log.Printf("[indengine] backfill error on %s: %v", stream, err)
			}
		}
		close(backfillCh)
	}()

	backfillCount := 0
	for tfc := range backfillCh {
		if !tfc.Forming {
			svc.apply(ctx, tfc)
			backfillCount++
		}
	}
	if backfillCount > 0 {
		log.Printf("[indengine] ✅ backfilled %d candles from Redis streams (indicator results written)", backfillCount)
	} else {
		log.Println("[indengine] no candles in Redis streams to backfill from")
	}
}

// replayDelta replays candles since the restored snapshot's checkpoint
// (the last entry processed per stream) to catch up on missed data.
func (svc *Service) replayDelta(ctx context.Context) {
	snap := svc.restored
	if snap == nil {
		return
	}

	if len(svc.streams) == 0 {
		return
	}

	if !hasReplayCheckpoint(snap) {
		log.Printf("[indengine] skipping delta replay: no valid stream checkpoint in snapshot")
		return
	}

	if len(snap.StreamIDs) > 0 {
		log.Printf("[indengine] replaying delta from per-stream checkpoints (%d streams)", len(snap.StreamIDs))
	} else {
		log.Printf("[indengine] replaying delta from stream ID: %s", snap.StreamID)
	}

	replayCh := make(chan model.TFCandle, 5000)
	go func() {
		for _, stream := range svc.streams {
			// A stream the checkpoint doesn't cover (none of its entries
			// processed yet, or new) replays from the start; v1 snapshots
			// only had one legacy ID.
			startID := "0"
			if snap.Version < 2 {
				startID = snap.StreamID
			}
			if id, ok := snap.StreamIDs[stream]; ok && id != "" {
				startID = id
			}
			if !isValidStreamID(startID) {
				log.Printf("[indengine] skipping delta replay for %s: invalid stream ID %q", stream, startID)
				continue
			}

			_, err := svc.redisReader.ReplayFromID(ctx, stream, startID, replayCh)
			if err != nil {
				log.Printf("[indengine] replay error on %s: %v", stream, err)
			}
		}
		close(replayCh)
	}()

	deltaCount := 0
	for tfc := range replayCh {
		if !tfc.Forming {
			svc.apply(ctx, tfc)
			deltaCount++
		}
	}
	log.Printf("[indengine] ✅ replayed %d delta candles (results written to Redis)", deltaCount)
}

// apply runs one candle through the engine, writes its indicator results
// and, for a finalized candle read from a stream, advances that stream's
// checkpoint. Owner only.
func (svc *Service) apply(ctx context.Context, tfc model.TFCandle) []model.IndicatorResult {
	var results []model.IndicatorResult
	if tfc.Forming {
		results = svc.engine.ProcessPeek(tfc)
	} else {
		results = svc.engine.Process(tfc)
		if tfc.StreamID != "" {
			stream := tfc.StreamKey()
			if streamIDLess(svc.lastProcessed[stream], tfc.StreamID) {
				svc.lastProcessed[stream] = tfc.StreamID
			}
		}
	}
	if len(results) > 0 && svc.redisWriter != nil {
		svc.redisWriter.WriteIndicatorBatch(ctx, results)
	}
	return results
}

// streamIDLess reports whether Redis stream ID a sorts before b ("" sorts
// first). IDs are "<ms>-<seq>".
func streamIDLess(a, b string) bool {
	if a == "" {
		return b != ""
	}
	am, as := splitStreamID(a)
	bm, bs := splitStreamID(b)
	if am != bm {
		return am < bm
	}
	return as < bs
}

func splitStreamID(id string) (uint64, uint64) {
	ms, seq, _ := strings.Cut(id, "-")
	m, _ := strconv.ParseUint(ms, 10, 64)
	s, _ := strconv.ParseUint(seq, 10, 64)
	return m, s
}

// isValidStreamID checks if a string looks like a valid Redis stream ID (e.g. "1234567890123-0").
func isValidStreamID(id string) bool {
	return strings.Contains(id, "-")
}

func hasReplayCheckpoint(snap *indicator.EngineSnapshot) bool {
	if snap == nil {
		return false
	}
	// v2 snapshots should use per-stream offsets; ignore legacy StreamID fallback.
	if snap.Version >= 2 {
		for _, id := range snap.StreamIDs {
			if isValidStreamID(id) {
				return true
			}
		}
		return false
	}
	if isValidStreamID(snap.StreamID) {
		return true
	}
	for _, id := range snap.StreamIDs {
		if isValidStreamID(id) {
			return true
		}
	}
	return false
}
