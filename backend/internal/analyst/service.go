package analyst

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"trading-systemv1/internal/heartbeat"
	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
)

// Service is the top-level orchestrator for the market analyst microservice.
// It wires all dependencies, manages lifecycle, and coordinates goroutines.
type Service struct {
	cfg Config

	engine    *Engine
	publisher *Publisher

	redisReader *redisstore.Reader
	redisWriter *redisstore.Writer

	streams    []string
	tfCandleCh chan model.TFCandle
}

// New creates a new Service from the given Config.
func New(cfg Config) (*Service, error) {
	svc := &Service{
		cfg:        cfg,
		engine:     NewEngine(),
		tfCandleCh: make(chan model.TFCandle, 5000),
	}

	// ── Connect to Redis ──
	var err error
	svc.redisReader, err = redisstore.NewReader(redisstore.ReaderConfig{
		Addr:          cfg.RedisAddr,
		Password:      cfg.RedisPassword,
		ConsumerGroup: cfg.ConsumerGroup,
		ConsumerName:  cfg.ConsumerName,
	})
	if err != nil {
		return nil, fmt.Errorf("redis reader: %w", err)
	}

	svc.redisWriter, err = redisstore.New(redisstore.WriterConfig{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	if err != nil {
		svc.redisReader.Close()
		return nil, fmt.Errorf("redis writer: %w", err)
	}

	svc.publisher = NewPublisher(svc.redisWriter)

	return svc, nil
}

// Run starts all subsystems and blocks until ctx is cancelled.
func (svc *Service) Run(ctx context.Context) error {
	log.Println("[analyst] starting Market Analyst microservice...")

	// ── Discover / build streams ──
	svc.streams = svc.buildStreams(ctx)
	log.Printf("[analyst] consuming from %d streams: %v", len(svc.streams), svc.streams)

	// ── Ensure consumer groups ──
	if len(svc.streams) > 0 {
		if err := svc.redisReader.EnsureConsumerGroup(ctx, svc.streams); err != nil {
			log.Printf("[analyst] WARNING: consumer group setup: %v", err)
		}
	}

	// ── Recover pending messages ──
	if len(svc.streams) > 0 {
		if err := svc.redisReader.RecoverPending(ctx, svc.streams, svc.tfCandleCh); err != nil {
			log.Printf("[analyst] pending recovery error: %v", err)
		}
	}

	// ── Warm up internal indicators ──
	if len(svc.streams) > 0 {
		svc.warmupIndicators(ctx)
	}

	// ── Start subsystems ──
	go heartbeat.NewPublisher("analyst", svc.redisWriter.Client()).Run(ctx)
	svc.startConsumer(ctx)
	go svc.processLoop(ctx)
	svc.startHTTP(ctx)

	// ── Banner ──
	log.Println("[analyst] ╔════════════════════════════════════════════════════════════╗")
	log.Println("[analyst] ║  Market Analyst (MS-Analyst) Active                       ║")
	log.Println("[analyst] ║                                                           ║")
	log.Println("[analyst] ║  [Redis Streams] → [Analysis Engine] → [Redis Publish]    ║")
	log.Println("[analyst] ║  Detects: Market State | S/R Levels | Breakouts           ║")
	log.Printf("[analyst] ║  TFs: %v                                             ║", svc.cfg.EnabledTFs)
	log.Println("[analyst] ╚════════════════════════════════════════════════════════════╝")
	log.Println("[analyst] ✅ all systems running. Press Ctrl+C to stop.")

	// Block until context cancelled
	<-ctx.Done()

	// ── Graceful shutdown ──
	svc.shutdown()
	return nil
}

// shutdown closes connections.
func (svc *Service) shutdown() {
	log.Println("[analyst] shutdown signal received...")
	svc.redisWriter.Close()
	svc.redisReader.Close()
	log.Println("[analyst] shutdown complete.")
}

// buildStreams constructs the Redis stream names to consume.
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

// startConsumer starts the Redis stream XREADGROUP consumer.
func (svc *Service) startConsumer(ctx context.Context) {
	if len(svc.streams) == 0 {
		return
	}
	go func() {
		if err := svc.redisReader.ConsumeTFCandles(ctx, svc.streams, svc.tfCandleCh); err != nil {
			log.Printf("[analyst] consumer error: %v", err)
		}
	}()
}

// processLoop consumes TF candles and runs the analysis engine.
func (svc *Service) processLoop(ctx context.Context) {
	var lastStateLog time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case tfc, ok := <-svc.tfCandleCh:
			if !ok {
				return
			}
			if tfc.Forming {
				continue // skip forming candles — only analyse finalized bars
			}

			// Only run the indicator pipeline on TF=60 (1m) candles
			// because the internal indicator engine only has TF=60 configs.
			if tfc.TF != 60 {
				continue
			}

			// Run the full analysis pipeline
			result := svc.engine.Process(tfc)

			// Publish market state on every tick (including forming candles)
			svc.publisher.PublishState(ctx, result.State)

			// S/R levels and breakout events only on finalized bars
			if !tfc.Forming {
				svc.publisher.PublishLevels(ctx, result.Levels)

				if result.Breakout != nil {
					svc.publisher.PublishBreakout(ctx, *result.Breakout)
					log.Printf("[analyst] BREAKOUT: stage=%s level=%d dir=%s vol_ratio=%.1f adx=%.1f %s:%s",
						result.Breakout.Stage, result.Breakout.Level,
						result.Breakout.Direction, result.Breakout.VolumeRatio,
						result.Breakout.ADXValue, result.Breakout.Exchange, result.Breakout.Token)
				}

				svc.publisher.PublishSummary(ctx, result.State, result.Levels, result.Breakout)
			}

			// Periodic state log (every 60s)
			if time.Since(lastStateLog) > 60*time.Second {
				log.Printf("[analyst] state=%s confluence=%d/%d direction=%s levels=%d %s:%s",
					result.State.State, result.State.Confluence, len(result.State.Votes),
					result.State.Direction, len(result.Levels.Levels),
					tfc.Exchange, tfc.Token)
				lastStateLog = time.Now()
			}
		}
	}
}

// startHTTP starts a simple HTTP health check server.
func (svc *Service) startHTTP(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    svc.cfg.HTTPAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("[analyst] HTTP health check on %s", svc.cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[analyst] HTTP server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
}

// warmupIndicators fetches the last 50 candles from each stream and runs them through the engine
// to warm up indicators like EMA(21), ADX(14), RSI(14) before consuming live streams.
func (svc *Service) warmupIndicators(ctx context.Context) {
	for _, stream := range svc.streams {
		msgs, err := svc.redisWriter.Client().XRevRangeN(ctx, stream, "+", "-", 50).Result()
		if err != nil {
			log.Printf("[analyst] warmup error on %s: %v", stream, err)
			continue
		}

		// Reverse to chronological order
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}

		warmed := 0
		var lastResult *AnalysisResult
		for _, msg := range msgs {
			data, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}
			var tfc model.TFCandle
			if err := json.Unmarshal([]byte(data), &tfc); err == nil && !tfc.Forming {
				res := svc.engine.Process(tfc)
				lastResult = &res
				warmed++
			}
		}

		if lastResult != nil && warmed > 0 {
			svc.publisher.PublishState(ctx, lastResult.State)
			svc.publisher.PublishLevels(ctx, lastResult.Levels)
			if lastResult.Breakout != nil {
				svc.publisher.PublishBreakout(ctx, *lastResult.Breakout)
			}
			svc.publisher.PublishSummary(ctx, lastResult.State, lastResult.Levels, lastResult.Breakout)
			log.Printf("[analyst] warmed up %s with %d historical candles. state=%s levels=%d",
				stream, warmed, lastResult.State.State, len(lastResult.Levels.Levels))
		} else {
			log.Printf("[analyst] warmed up %s but no historical candles found", stream)
		}
	}
}
