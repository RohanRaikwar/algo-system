package mdengine

import (
	"context"
	"log"
	"strconv"
	"time"

	"trading-systemv1/internal/marketdata/agg"
	"trading-systemv1/internal/marketdata/bus"
	"trading-systemv1/internal/marketdata/tfbuilder"
	"trading-systemv1/internal/model"
	redisstore "trading-systemv1/internal/store/redis"
)

// Pipeline drop stage labels (mdengine_pipeline_drops_total{stage}).
const (
	stageWSTick    = "ws_tick"    // WS ingest -> tickCh
	stageAggTick   = "agg_tick"   // tick router -> aggregator
	stageTFForming = "tf_forming" // forming TF candle -> Redis pubsub
	stageTFRedis   = "tf_redis"   // final TF candle -> Redis writer queue (full)
	stageTFSQLite  = "tf_sqlite"  // final TF candle -> SQLite writer queue (full)
	stageTickPub   = "tick_pub"   // tick router -> Redis tick publisher (oldest evicted)
)

const (
	// tickPubQueueLen bounds ticks waiting for the Redis publisher.
	tickPubQueueLen = 4096
	// tickPubMaxBatch caps ticks per publish pipeline.
	tickPubMaxBatch = 256

	// tfFinalQueueLen bounds final TF candles between the TF builder and the
	// router. The builder blocks (never drops) on it; the router never blocks.
	tfFinalQueueLen = 5000
	// tfSinkQueueLen bounds final TF candles waiting for each writer (Redis,
	// SQLite). Sized for a long single-sink stall (finals are ~tokens×TFs
	// per TF period); overflow drops for that sink only and is counted.
	tfSinkQueueLen = 50000
)

// tickPublisher is the narrow Redis surface the tick publisher needs.
type tickPublisher interface {
	PublishTicks(ctx context.Context, ticks []model.Tick) error
}

// counter and observer are the narrow metric surfaces the hot path needs.
type counter interface{ Inc() }
type observer interface{ Observe(float64) }

// pipelineCounters holds pre-resolved metric children so the per-tick path
// does no label lookups.
type pipelineCounters struct {
	ticksTotal    counter
	e2eLatency    observer
	redisWriteDur observer

	dropWSTick    counter
	dropAggTick   counter
	dropTFForming counter
	dropTFRedis   counter
	dropTFSQLite  counter
	dropTickPub   counter

	redisFailTick counter
	redisFail1s   counter
	redisFailTF   counter

	clockSkew counter // exchange timestamps replaced by receive time
	feedStale counter // stale-feed watchdog firings
}

func (s *Service) initPipelineCounters() {
	d := s.prom.PipelineDrops
	s.ctr = pipelineCounters{
		ticksTotal:    s.prom.TicksTotal,
		e2eLatency:    s.prom.E2ELatency,
		redisWriteDur: s.prom.RedisWriteDur,
		dropWSTick:    d.WithLabelValues(stageWSTick),
		dropAggTick:   d.WithLabelValues(stageAggTick),
		dropTFForming: d.WithLabelValues(stageTFForming),
		dropTFRedis:   d.WithLabelValues(stageTFRedis),
		dropTFSQLite:  d.WithLabelValues(stageTFSQLite),
		dropTickPub:   d.WithLabelValues(stageTickPub),
		redisFailTick: s.prom.RedisWriteFailures.WithLabelValues(redisstore.OpTick),
		redisFail1s:   s.prom.RedisWriteFailures.WithLabelValues(redisstore.OpCandle1s),
		redisFailTF:   s.prom.RedisWriteFailures.WithLabelValues(redisstore.OpTFCandle),
		clockSkew:     s.prom.EventTSClamped,
		feedStale:     s.prom.FeedStaleAlerts,
	}
}

// onRedisWriteError counts a failed bounded Redis write (pre-resolved labels).
func (s *Service) onRedisWriteError(op string) {
	switch op {
	case redisstore.OpTick:
		s.ctr.redisFailTick.Inc()
	case redisstore.OpCandle1s:
		s.ctr.redisFail1s.Inc()
	case redisstore.OpTFCandle:
		s.ctr.redisFailTF.Inc()
	}
}

// recordIngest records per-tick ingest metrics (called from WS ingest).
func (s *Service) recordIngest(tick *model.Tick) {
	s.lastTickNano.Store(tick.TickTS.UnixNano()) // stale-feed watchdog / healthz
	s.ctr.ticksTotal.Inc()
	if !tick.EventTS.IsZero() {
		s.ctr.e2eLatency.Observe(tick.TickTS.Sub(tick.EventTS).Seconds())
	}
}

// routeTick forwards one tick to the aggregator and queues it for Redis
// publishing. Never blocks: Redis latency must not slow aggregation.
func (s *Service) routeTick(tick model.Tick, aggTickCh chan<- model.Tick) {
	// Only aggregate candles for tokens in the CANDLE_TOKENS set (or all if unset)
	if len(s.cfg.CandleTokens) == 0 || s.cfg.CandleTokens[tick.Token] {
		select {
		case aggTickCh <- tick:
		default:
			s.ctr.dropAggTick.Inc()
		}
	}
	// Publish ALL ticks to Redis PubSub for live stoploss + FNO LTP tracking,
	// via the dedicated publisher goroutine.
	if s.tickPubCh == nil {
		return
	}
	select {
	case s.tickPubCh <- tick:
		return
	default:
	}
	// Queue full: evict the oldest tick so the newest price wins.
	select {
	case <-s.tickPubCh:
		s.ctr.dropTickPub.Inc()
	default:
	}
	select {
	case s.tickPubCh <- tick:
	default:
		s.ctr.dropTickPub.Inc()
	}
}

// runTickPublisher drains tickPubCh and publishes ticks to Redis, batching
// whatever queued up during the previous round trip into one pipeline.
func (s *Service) runTickPublisher(ctx context.Context, pub tickPublisher) {
	batch := make([]model.Tick, 0, tickPubMaxBatch)
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-s.tickPubCh:
			batch = append(batch[:0], t)
		}
	drain:
		for len(batch) < tickPubMaxBatch {
			select {
			case t := <-s.tickPubCh:
				batch = append(batch, t)
			default:
				break drain
			}
		}
		start := time.Now()
		pub.PublishTicks(ctx, batch) // bounded; failures counted via OnWriteError
		s.ctr.redisWriteDur.Observe(time.Since(start).Seconds())
	}
}

// routeTFCandle sends a TF candle to its Redis/SQLite writers without ever
// blocking, so one stalled sink cannot hold back the other or the TF builder.
// Forming candles (live preview) drop when their channel is full; final
// candles have a large queue per sink, and an overflow drops for that sink
// only (counted).
func (s *Service) routeTFCandle(tfc model.TFCandle, redisFormingCh chan<- model.TFCandle) {
	if tfc.Forming {
		select {
		case redisFormingCh <- tfc:
		default:
			s.ctr.dropTFForming.Inc()
		}
		return
	}
	if s.redisTFCandleCh != nil {
		select {
		case s.redisTFCandleCh <- tfc:
		default:
			s.ctr.dropTFRedis.Inc()
		}
	}
	select {
	case s.sqliteTFCandleCh <- tfc:
	default:
		s.ctr.dropTFSQLite.Inc()
	}
}

// runTFBuilderInput feeds 1s candles from the fan-out into the TF builder.
func (s *Service) runTFBuilderInput(ctx context.Context, in <-chan model.Candle, buildDur observer) {
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-in:
			if !ok {
				return
			}
			start := time.Now()
			s.tfBuilder.Run1Locked(c, s.tfCandleCh)
			buildDur.Observe(time.Since(start).Seconds())
		case done := <-s.tfFlushReq:
			// Session flush: first process the 1s candles already queued
			// (the aggregator's final ones), then finalize every TF bucket.
			for n := len(in); n > 0; n-- {
				c, ok := <-in
				if !ok {
					break
				}
				s.tfBuilder.Run1Locked(c, s.tfCandleCh)
			}
			s.tfBuilder.FlushSession(s.tfCandleCh)
			close(done)
		}
	}
}

// sessionFlushTimeout bounds the market-close flush handshake.
const sessionFlushTimeout = 5 * time.Second

// flushSession finalizes all forming 1s and TF candles at market close
// (ADR-006 Contract #3). The TF flush is sequenced after the aggregator's last
// 1s candles have reached the TF builder, so the closing TF candle includes
// its tail seconds and the tail cannot re-open the bucket afterwards.
func (s *Service) flushSession(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, sessionFlushTimeout)
	defer cancel()

	s.aggregator.FlushSession(s.candleCh) // synchronous: final candles are queued on candleCh
	if err := s.fanout.Sync(ctx); err != nil {
		log.Printf("[mdengine] ⚠️  session flush: fan-out sync: %v", err)
	}
	done := make(chan struct{})
	select {
	case s.tfFlushReq <- done:
		select {
		case <-done:
		case <-ctx.Done():
			log.Printf("[mdengine] ⚠️  session flush: TF builder did not confirm: %v", ctx.Err())
		}
	case <-ctx.Done():
		log.Printf("[mdengine] ⚠️  session flush: TF builder input not running (%v) — flushing directly", ctx.Err())
		s.tfBuilder.FlushSession(s.tfCandleCh)
	}
}

// setupPipeline wires the full data pipeline:
// ticks → aggregator → 1s candles → fanout → [SQLite, Redis, TF builder] → TF candles → [Redis, SQLite]
func (s *Service) setupPipeline(ctx context.Context) {
	s.initPipelineCounters()
	if s.redisWriter != nil {
		s.redisWriter.OnWriteError = s.onRedisWriteError
		s.redisWriter.OnDuplicateTFCandle = s.prom.TFDuplicateCandles.Inc
		s.tickPubCh = make(chan model.Tick, tickPubQueueLen)
		go s.runTickPublisher(ctx, s.redisWriter)
	}

	// ── Fan-out for 1s candles (SQLite + Redis) ──
	s.fanout = bus.New(5000)
	s.fanout.OnDrop = func(subscriberIdx int) {
		s.prom.FanoutDropsTotal.WithLabelValues(strconv.Itoa(subscriberIdx)).Inc()
	}

	sqliteCandleCh := s.fanout.Subscribe()
	var redis1sCandleCh <-chan model.Candle
	if s.redisWriter != nil {
		redis1sCandleCh = s.fanout.Subscribe()
	}

	go s.fanout.Run(ctx, s.candleCh)

	// Channel saturation monitoring
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := s.fanout.ChannelStats()
				for i, st := range stats {
					if st.Cap > 0 {
						pct := float64(st.Len) / float64(st.Cap) * 100
						s.prom.ChannelSaturationPct.WithLabelValues("fanout_" + strconv.Itoa(i)).Set(pct)
					}
				}
			}
		}
	}()

	go s.sqlWriter.Run(ctx, sqliteCandleCh)
	if s.redisWriter != nil && redis1sCandleCh != nil {
		go s.redisWriter.Run(ctx, redis1sCandleCh)
	}

	// ── TF Builder (HOT PATH) ──
	s.tfBuilder = tfbuilder.New(s.cfg.EnabledTFs)
	s.tfBuilder.OnTFCandle = func(c model.TFCandle) {
		s.prom.TFCandlesTotal.WithLabelValues(strconv.Itoa(c.TF)).Inc()
	}
	s.tfBuilder.OnStaleCandle = func() {
		s.prom.StaleCandlesRejected.Inc()
	}
	s.tfBuilder.OnLateCandle = s.prom.TFLateCandles.Inc
	// Finals get their own channel: a burst of forming snapshots can never
	// crowd out (and drop) a final candle.
	s.tfBuilder.FinalOut = s.tfFinalCh
	s.tfBuilder.Done = ctx.Done()
	emitDropFinal := s.prom.TFEmitDrops.WithLabelValues("final")
	emitDropForming := s.prom.TFEmitDrops.WithLabelValues("forming")
	s.tfBuilder.OnDrop = func(final bool) {
		if final {
			emitDropFinal.Inc()
		} else {
			emitDropForming.Inc()
		}
	}
	s.health.SetTFBuilderOK(true)
	log.Printf("[mdengine] TF builder started with TFs=%v (stale tolerance=%v)", s.cfg.EnabledTFs, s.tfBuilder.StaleTolerance)

	tfBuilderIn := s.fanout.Subscribe()
	go s.runTFBuilderInput(ctx, tfBuilderIn, s.prom.TFBuildDur)

	// Timer-based flush for TF candle finalization
	go s.tfBuilder.RunWithTimer(ctx, s.tfCandleCh)

	// ── Fan out TF candles to Redis + SQLite (OFF hot path) ──
	redisFormingCh := make(chan model.TFCandle, 5000)
	if s.redisWriter == nil {
		s.redisTFCandleCh = nil // no Redis sink: finals go to SQLite only
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case tfc, ok := <-s.tfCandleCh:
				if !ok {
					return
				}
				s.routeTFCandle(tfc, redisFormingCh)
			case tfc := <-s.tfFinalCh:
				s.routeTFCandle(tfc, redisFormingCh)
			}
		}
	}()

	if s.redisWriter != nil {
		go s.redisWriter.RunTFCandles(ctx, s.redisTFCandleCh)
		go s.redisWriter.RunFormingTFCandles(ctx, redisFormingCh)
	}
	go s.sqlWriter.RunTFCandles(ctx, s.sqliteTFCandleCh)

	// ── Aggregator (1s OHLC builder) ──
	s.aggregator = agg.New()
	s.aggregator.MarketCloseGate = !s.cfg.StagingMode
	s.aggregator.OnDroppedTick = func() {
		s.prom.DroppedTicks.Inc()
	}
	s.aggregator.OnLateTick = func() {
		s.prom.LateTicks.Inc()
	}
	s.aggregator.OnCandle = func(c model.Candle) {
		s.prom.CandlesTotal.Inc()
		s.prom.CandleLag.Set(time.Since(c.TS).Seconds())
	}

	// Fan-out: each tick goes to aggregator AND Redis PubSub
	if len(s.cfg.CandleTokens) > 0 {
		log.Printf("[mdengine] CANDLE_TOKENS filter active: only %v get candle aggregation", s.cfg.CandleTokens)
	}

	aggTickCh := make(chan model.Tick, 10000)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case tick, ok := <-s.tickCh:
				if !ok {
					return
				}
				s.routeTick(tick, aggTickCh)
			}
		}
	}()
	go s.aggregator.Run(ctx, aggTickCh, s.candleCh)
	log.Println("[mdengine] pipeline ready (24/7)")
}
