package mdengine

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"trading-systemv1/internal/heartbeat"

	"trading-systemv1/internal/marketdata/agg"
	"trading-systemv1/internal/marketdata/bus"
	"trading-systemv1/internal/marketdata/tfbuilder"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/metrics"
	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
	sqlitestore "trading-systemv1/internal/store/sqlite"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

// Service is the top-level orchestrator for the market data engine.
// It wires the full pipeline: ticks → 1s candles → TF candles → Redis/SQLite.
type Service struct {
	cfg Config

	// Infrastructure
	prom        *metrics.Metrics
	ctr         pipelineCounters // pre-resolved hot-path metrics (see initPipelineCounters)
	health      *metrics.HealthStatus
	metricsSrv  *metrics.Server
	sqlWriter   *sqlitestore.Writer
	redisWriter *redisstore.Writer
	sqlReader   *sqlitestore.Reader
	redisClient *goredis.Client // for PubSub command listener

	// Pipeline channels
	tickCh           chan model.Tick
	candleCh         chan model.Candle
	tfCandleCh       chan model.TFCandle // forming TF snapshots (live preview)
	tfFinalCh        chan model.TFCandle // final TF candles (TF builder blocks, never drops)
	redisTFCandleCh  chan model.TFCandle
	sqliteTFCandleCh chan model.TFCandle
	tickPubCh        chan model.Tick // tick router -> Redis tick publisher (nil without Redis)

	// Dynamic FNO token subscription channel
	dynamicSubCh chan []smartconnect.TokenListEntry

	// Dynamic tokens subscribed so far, re-subscribed after a re-login.
	dynMu     sync.Mutex
	dynTokens []smartconnect.TokenListEntry

	// Pipeline components
	fanout     *bus.FanOut
	tfBuilder  *tfbuilder.Builder
	aggregator *agg.Aggregator

	// tfFlushReq asks the TF builder input goroutine to finalize the session
	// once the 1s candles already queued to it are processed (see flushSession).
	tfFlushReq chan chan struct{}

	// Stale-feed watchdog (see watchdog.go).
	lastTickNano   atomic.Int64 // local receive time of the newest tick (UnixNano)
	feedMu         sync.Mutex
	feedReconnect  func(reason string) // live session's forced reconnect; nil when none
	feedLastFrame  func() time.Time    // live socket's last received frame; nil when none
	staleForced    int                 // consecutive forced re-logins without a tick
	staleLastAlert time.Time           // last stale-feed alert logged (rate limit)
}

// New creates a new market data engine Service.
func New(cfg Config) (*Service, error) {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[mdengine] starting...")

	// Load holidays from NSE API / cache / fallback
	configDir := markethours.GetConfigDir()
	markethours.InitHolidays(configDir)
	markethours.StartBackgroundRefresher(context.Background(), configDir)

	svc := &Service{
		cfg:              cfg,
		tickCh:           make(chan model.Tick, 10000),
		candleCh:         make(chan model.Candle, 5000),
		tfCandleCh:       make(chan model.TFCandle, 5000),
		tfFinalCh:        make(chan model.TFCandle, tfFinalQueueLen),
		redisTFCandleCh:  make(chan model.TFCandle, tfSinkQueueLen),
		sqliteTFCandleCh: make(chan model.TFCandle, tfSinkQueueLen),
		dynamicSubCh:     make(chan []smartconnect.TokenListEntry, 10),
		tfFlushReq:       make(chan chan struct{}),
	}

	// Redis client for dynamic token subscription commands
	svc.redisClient = goredis.NewClient(&goredis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})

	// Metrics & health
	svc.prom = metrics.NewMetrics()
	svc.health = metrics.NewHealthStatus()
	svc.health.SetEnabledTFs(cfg.EnabledTFs)
	svc.metricsSrv = metrics.NewServer(cfg.MetricsAddr, svc.health)
	svc.metricsSrv.Start()

	// SQLite writer
	os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o755)
	sqlWriter, err := sqlitestore.New(sqlitestore.WriterConfig{DBPath: cfg.SQLitePath})
	if err != nil {
		return nil, err
	}
	svc.sqlWriter = sqlWriter
	svc.health.SetSQLiteOK(true)
	log.Println("[mdengine] sqlite writer ready")

	// Redis writer
	redisWriter, err := redisstore.New(redisstore.WriterConfig{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	if err != nil {
		// Keep a writer anyway: the client reconnects by itself and final TF
		// candles are buffered until Redis answers, so Redis coming back
		// later needs no mdengine restart.
		log.Printf("[mdengine] WARNING: redis not reachable at startup: %v — writers will retry until it is", err)
		redisWriter = redisstore.NewUnchecked(redisstore.WriterConfig{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
		})
		svc.health.SetRedisConnected(false)
	} else {
		svc.health.SetRedisConnected(true)
		log.Println("[mdengine] redis writer ready")
	}
	svc.redisWriter = redisWriter

	// Final TF candles Redis missed (outage, restart) are restored into
	// the streams from SQLite, the durable copy.
	if sqlReader, rerr := sqlitestore.NewReader(cfg.SQLitePath); rerr != nil {
		log.Printf("[mdengine] WARNING: sqlite reader for TF stream restore unavailable: %v", rerr)
	} else {
		svc.sqlReader = sqlReader
		svc.redisWriter.TFHistory = svc.recentTFHistory
	}

	return svc, nil
}

// Run starts the full pipeline and blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) error {
	// Start liveness checks
	if s.redisWriter != nil {
		s.health.StartLivenessChecker(ctx, s.redisWriter.Client(), s.sqlWriter.DB(), 10*time.Second)
	} else {
		s.health.StartLivenessChecker(ctx, nil, s.sqlWriter.DB(), 10*time.Second)
	}

	// One-time, idempotent: move auto-ID TF streams aside so explicit
	// candle-TS stream IDs take effect (must run before the writers start).
	if s.redisWriter != nil {
		migCtx, migCancel := context.WithTimeout(ctx, 30*time.Second)
		if n, err := s.redisWriter.MigrateLegacyTFStreams(migCtx); err != nil {
			log.Printf("[mdengine] ⚠️  legacy TF stream migration: %v (migrated %d)", err, n)
		} else if n > 0 {
			log.Printf("[mdengine] migrated %d legacy auto-ID TF streams", n)
		}
		migCancel()
	}

	// Wire the pipeline
	s.setupPipeline(ctx)

	// Start heartbeat publisher
	if s.redisWriter != nil {
		go heartbeat.NewPublisher("mdengine", s.redisWriter.Client()).Run(ctx)
	}

	// Stale-feed watchdog (market hours) + /healthz last tick time
	go s.runFeedWatchdog(ctx)

	// Pipeline stats for the dashboard Health page (read by api_gateway).
	go metrics.RunSnapshotPublisher(ctx, s.redisClient, metrics.SnapshotKeyMDEngine, 2*time.Second, func() any {
		snap := s.prom.PipelineSnapshot()
		if s.aggregator != nil {
			snap.WatermarkDelaySec = s.aggregator.WatermarkDelay().Seconds()
		}
		snap.UpdatedAt = metrics.Stamp()
		return snap
	})

	// Start dynamic FNO token subscription listener
	go s.listenForDynamicSubscriptions(ctx)

	// Start WS lifecycle
	if s.cfg.StagingMode {
		s.runStagingSession(ctx)
	} else {
		s.runProductionSession(ctx)
	}

	// Block until context cancelled
	<-ctx.Done()

	// Graceful shutdown
	s.shutdown()
	return nil
}

// shutdown flushes buffers and closes connections.
func (s *Service) shutdown() {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	s.metricsSrv.Stop(shutdownCtx)

	if s.redisWriter != nil {
		s.redisWriter.Close()
	}
	if s.sqlReader != nil {
		s.sqlReader.Close()
	}
	if s.redisClient != nil {
		s.redisClient.Close()
	}
	s.sqlWriter.Close()

	log.Println("[mdengine] shutdown complete.")
}

// listenForDynamicSubscriptions subscribes to Redis PubSub channel "cmd:subscribe_token"
// and forwards parsed token lists to dynamicSubCh. This allows stratengine to dynamically
// add FNO tokens to the WebSocket feed after ATM strike resolution.
func (s *Service) listenForDynamicSubscriptions(ctx context.Context) {
	if s.redisClient == nil {
		return
	}

	pubsub := s.redisClient.Subscribe(ctx, "cmd:subscribe_token")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}

			// Parse: {"exchange_type": 2, "tokens": ["57710", "57709"]}
			var cmd struct {
				ExchangeType int      `json:"exchange_type"`
				Tokens       []string `json:"tokens"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				log.Printf("[mdengine] ⚠️  invalid subscribe_token command: %v", err)
				continue
			}

			if len(cmd.Tokens) == 0 {
				continue
			}

			entry := smartconnect.TokenListEntry{
				ExchangeType: cmd.ExchangeType,
				Tokens:       cmd.Tokens,
			}

			log.Printf("[mdengine] 📡 received dynamic subscribe command: exchange=%d tokens=%v",
				cmd.ExchangeType, cmd.Tokens)

			select {
			case s.dynamicSubCh <- []smartconnect.TokenListEntry{entry}:
			default:
				log.Println("[mdengine] ⚠️  dynamicSubCh full, dropping subscribe command")
			}
		}
	}
}

// tfHistoryWindow is how far back TF streams are restored from SQLite: the
// span Redis keeps (streams are trimmed to ~3h of candles).
const tfHistoryWindow = 3 * time.Hour

// recentTFHistory returns the final TF candles of the last tfHistoryWindow
// from SQLite for every enabled TF, oldest first within each stream.
func (s *Service) recentTFHistory(ctx context.Context) ([]model.TFCandle, error) {
	after := time.Now().Add(-tfHistoryWindow).Unix()
	var out []model.TFCandle
	for _, tf := range s.cfg.EnabledTFs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candles, err := s.sqlReader.ReadAllTFCandles(tf, after)
		if err != nil {
			return nil, err
		}
		out = append(out, candles...)
	}
	return out, nil
}
