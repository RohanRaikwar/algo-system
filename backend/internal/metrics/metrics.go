package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for the OHLC engine.
type Metrics struct {
	TicksTotal      prometheus.Counter
	CandlesTotal    prometheus.Counter
	WSReconnects    prometheus.Counter
	DroppedTicks    prometheus.Counter
	RedisWriteDur   prometheus.Histogram
	SQLiteCommitDur prometheus.Histogram
	CandleLag       prometheus.Gauge

	// TF resampler metrics
	TFCandlesTotal *prometheus.CounterVec
	TFBuildDur     prometheus.Histogram

	// Indicator engine metrics
	IndicatorComputeDur prometheus.Histogram
	IndicatorsTotal     prometheus.Counter

	// Ring buffer overflow
	RingBufOverflow prometheus.Counter

	// Backpressure metrics (improvement #5)
	FanoutDropsTotal     *prometheus.CounterVec // labels: subscriber
	ChannelSaturationPct *prometheus.GaugeVec   // labels: channel_name

	// Staleness metrics (improvement #2)
	StaleCandlesRejected prometheus.Counter

	// PEL reclaim metrics (improvement #1)
	PELMessagesReclaimed prometheus.Counter

	// Circuit breaker metrics (improvement #6)
	RedisCircuitBreakerState prometheus.Gauge // 0=closed, 1=open, 2=half-open
	RedisCircuitBreakerTrips prometheus.Counter
	RedisBufferedWrites      prometheus.Counter

	// End-to-end observability (improvement #8)
	E2ELatency       prometheus.Histogram // tick-to-WS-emit latency
	WatermarkDelay   prometheus.Gauge     // current watermark delay vs wall clock
	LateTicks        prometheus.Counter   // ticks dropped behind watermark
	ReorderBufferLen prometheus.Gauge     // current reorder buffer occupancy

	// Market data path health
	PipelineDrops      *prometheus.CounterVec // labels: stage (non-blocking channel sends that dropped)
	RedisWriteFailures *prometheus.CounterVec // labels: op (tick|candle_1s|tf_candle) — errors or timeouts
	FeedSeqGaps        prometheus.Counter     // feed sequence gap events
	FeedSeqMissed      prometheus.Counter     // total sequence numbers skipped
	EventTSClamped     prometheus.Counter     // implausible exchange timestamps replaced by receive time
	TFLateCandles      prometheus.Counter     // 1s candles skipped: their TF bucket was already finalized
	TFEmitDrops        *prometheus.CounterVec // labels: kind (final|forming) — TF builder output drops
	TFDuplicateCandles prometheus.Counter     // final TF candles whose stream ID already held a different payload
	FeedStaleAlerts    prometheus.Counter     // stale-feed watchdog firings (no tick during market hours)

	// Market session state (ADR-006)
	MarketState        prometheus.Gauge       // 0=closed, 1=open
	SessionTransitions *prometheus.CounterVec // labels: type=open|close|ws_disconnect

	// Order executor circuit breaker + rate limiter (per-service)
	OrderCBState    prometheus.Gauge // 0=closed, 1=open, 2=half-open
	OrderCBFailures prometheus.Gauge // consecutive failure count
	OrderRLCount    prometheus.Gauge // orders placed in current window
	OrderRLMax      prometheus.Gauge // configured max orders per window
}

// NewMetrics registers and returns all Prometheus metrics.
func NewMetrics() *Metrics {
	m := &Metrics{
		TicksTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_ticks_total",
			Help: "Total ticks received from WebSocket",
		}),
		CandlesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_candles_total",
			Help: "Total 1s candles emitted",
		}),
		WSReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_ws_reconnects_total",
			Help: "Total WebSocket reconnection attempts",
		}),
		DroppedTicks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_dropped_ticks_total",
			Help: "Ticks dropped (late or channel full)",
		}),
		RedisWriteDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_redis_write_duration_seconds",
			Help:    "Redis write latency",
			Buckets: prometheus.DefBuckets,
		}),
		SQLiteCommitDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_sqlite_commit_duration_seconds",
			Help:    "SQLite batch commit latency",
			Buckets: prometheus.DefBuckets,
		}),
		CandleLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_candle_lag_seconds",
			Help: "Lag between candle timestamp and emission time",
		}),

		// TF metrics
		TFCandlesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_tf_candles_total",
			Help: "Total TF candles emitted (by timeframe)",
		}, []string{"tf"}),
		TFBuildDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_tf_build_duration_seconds",
			Help:    "TF resampler processing latency per candle",
			Buckets: []float64{0.000001, 0.000005, 0.00001, 0.00005, 0.0001, 0.0005, 0.001},
		}),

		// Indicator metrics
		IndicatorComputeDur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_indicator_compute_duration_seconds",
			Help:    "Indicator engine compute latency per TF candle",
			Buckets: []float64{0.000001, 0.000005, 0.00001, 0.00005, 0.0001, 0.0005, 0.001},
		}),
		IndicatorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_indicators_total",
			Help: "Total indicator values computed",
		}),

		// Ring buffer
		RingBufOverflow: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_ringbuf_overflow_total",
			Help: "Ring buffer push overflows (dropped candles)",
		}),

		// Backpressure
		FanoutDropsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_fanout_drops_total",
			Help: "Candles dropped by FanOut bus per subscriber",
		}, []string{"subscriber"}),
		ChannelSaturationPct: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mdengine_channel_saturation_pct",
			Help: "Channel fill percentage (len/cap * 100)",
		}, []string{"channel_name"}),

		// Staleness
		StaleCandlesRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_stale_candles_rejected_total",
			Help: "Candles rejected by TF Builder due to staleness",
		}),

		// PEL reclaim
		PELMessagesReclaimed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "indengine_pel_messages_reclaimed_total",
			Help: "Messages reclaimed from dead consumers via XCLAIM",
		}),

		// Circuit breaker
		RedisCircuitBreakerState: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_redis_circuit_breaker_state",
			Help: "Redis circuit breaker state (0=closed, 1=open, 2=half-open)",
		}),
		RedisCircuitBreakerTrips: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_redis_circuit_breaker_trips_total",
			Help: "Times the Redis circuit breaker tripped open",
		}),
		RedisBufferedWrites: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_redis_buffered_writes_total",
			Help: "Writes buffered locally during Redis circuit breaker open state",
		}),

		// E2E observability
		E2ELatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "mdengine_e2e_latency_seconds",
			Help:    "Latency from exchange event time to local tick receipt (TickTS - EventTS)",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		}),
		WatermarkDelay: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_watermark_delay_seconds",
			Help: "Lag between wall-clock time and event-time watermark",
		}),
		LateTicks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_late_ticks_total",
			Help: "Ticks dropped because they arrived behind the event-time watermark",
		}),
		ReorderBufferLen: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_reorder_buffer_len",
			Help: "Current number of candle buckets held in the reorder buffer",
		}),

		// Market data path health
		PipelineDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_pipeline_drops_total",
			Help: "Items dropped on a full pipeline channel (by stage)",
		}, []string{"stage"}),
		RedisWriteFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_redis_write_failures_total",
			Help: "Bounded Redis writes that failed or timed out (by op; tf_candle after retries)",
		}, []string{"op"}),
		EventTSClamped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_event_ts_clamped_total",
			Help: "Ticks whose exchange timestamp was implausible (replaced by receive time) or future-dated (capped at receive time + 1s)",
		}),
		TFLateCandles: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_tf_late_candles_total",
			Help: "1s candles that arrived after their TF bucket was finalized (skipped)",
		}),
		TFEmitDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_tf_emit_drops_total",
			Help: "TF candles the TF builder could not emit (by kind: final|forming)",
		}, []string{"kind"}),
		TFDuplicateCandles: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_tf_duplicate_candles_total",
			Help: "Final TF candles not written: the bucket was already stored with a different payload",
		}),
		FeedStaleAlerts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_feed_stale_alerts_total",
			Help: "Stale-feed watchdog firings: no tick for the stale threshold during market hours (forces re-login only if the socket is also silent)",
		}),
		FeedSeqGaps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_feed_seq_gaps_total",
			Help: "Feed sequence-number gaps detected per token",
		}),
		FeedSeqMissed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mdengine_feed_seq_missed_total",
			Help: "Total feed sequence numbers skipped across all gaps",
		}),

		// Market session (ADR-006)
		MarketState: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mdengine_market_state",
			Help: "Market session state (0=closed, 1=open)",
		}),
		SessionTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mdengine_session_transitions_total",
			Help: "Market session transitions (open, close, ws_disconnect)",
		}, []string{"type"}),
	}
	m.initOrderMetrics()

	prometheus.MustRegister(
		m.TicksTotal,
		m.CandlesTotal,
		m.WSReconnects,
		m.DroppedTicks,
		m.RedisWriteDur,
		m.SQLiteCommitDur,
		m.CandleLag,
		m.TFCandlesTotal,
		m.TFBuildDur,
		m.IndicatorComputeDur,
		m.IndicatorsTotal,
		m.RingBufOverflow,
		m.FanoutDropsTotal,
		m.ChannelSaturationPct,
		m.StaleCandlesRejected,
		m.PELMessagesReclaimed,
		m.RedisCircuitBreakerState,
		m.RedisCircuitBreakerTrips,
		m.RedisBufferedWrites,
		m.E2ELatency,
		m.WatermarkDelay,
		m.LateTicks,
		m.ReorderBufferLen,
		m.PipelineDrops,
		m.RedisWriteFailures,
		m.FeedSeqGaps,
		m.FeedSeqMissed,
		m.EventTSClamped,
		m.TFLateCandles,
		m.TFEmitDrops,
		m.TFDuplicateCandles,
		m.FeedStaleAlerts,
		m.MarketState,
		m.SessionTransitions,
		m.OrderCBState,
		m.OrderCBFailures,
		m.OrderRLCount,
		m.OrderRLMax,
	)

	return m
}

// initOrderMetrics creates the order executor circuit breaker + rate limiter
// gauges (unregistered).
func (m *Metrics) initOrderMetrics() {
	m.OrderCBState = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orderexec_circuit_breaker_state",
		Help: "Order executor circuit breaker state (0=closed, 1=open, 2=half-open)",
	})
	m.OrderCBFailures = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orderexec_circuit_breaker_failures",
		Help: "Consecutive failure count on order executor circuit breaker",
	})
	m.OrderRLCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orderexec_rate_limiter_count",
		Help: "Orders placed in current rate-limiter window",
	})
	m.OrderRLMax = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orderexec_rate_limiter_max",
		Help: "Configured max orders per rate-limiter window",
	})
}

// NewOrderMetrics returns Metrics with only the orderexec_* gauges set
// (other fields nil), registered on a fresh registry together with the Go and
// process collectors. Serve it with NewServerWithRegistry so a process that
// only executes orders (stratengine) does not export zero-valued mdengine_*
// series. Unlike NewMetrics it can be called more than once.
func NewOrderMetrics() (*Metrics, *prometheus.Registry) {
	m := &Metrics{}
	m.initOrderMetrics()
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.OrderCBState,
		m.OrderCBFailures,
		m.OrderRLCount,
		m.OrderRLMax,
	)
	return m, reg
}

// HealthStatus represents the system health.
type HealthStatus struct {
	mu sync.RWMutex

	WSConnected    bool      `json:"ws_connected"`
	LastTickTime   time.Time `json:"last_tick_time"`
	FeedStale      bool      `json:"feed_stale"`
	RedisConnected bool      `json:"redis_connected"`
	SQLiteOK       bool      `json:"sqlite_ok"`
	TFBuilderOK    bool      `json:"tf_builder_ok"`
	IndicatorOK    bool      `json:"indicator_ok"`
	EnabledTFs     []int     `json:"enabled_tfs"`

	// Liveness probe results
	RedisLatencyMs  float64   `json:"redis_latency_ms"`
	SQLiteLatencyMs float64   `json:"sqlite_latency_ms"`
	LastCheckAt     time.Time `json:"last_check_at"`
	StartedAt       time.Time `json:"started_at"`
}

// NewHealthStatus returns a default health status.
func NewHealthStatus() *HealthStatus {
	return &HealthStatus{
		StartedAt: time.Now(),
	}
}

func (h *HealthStatus) SetWSConnected(v bool) {
	h.mu.Lock()
	h.WSConnected = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetLastTickTime(t time.Time) {
	h.mu.Lock()
	h.LastTickTime = t
	h.mu.Unlock()
}

// SetFeedStale marks the market feed stale (no tick during market hours for
// the watchdog threshold) or fresh again.
func (h *HealthStatus) SetFeedStale(v bool) {
	h.mu.Lock()
	h.FeedStale = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetRedisConnected(v bool) {
	h.mu.Lock()
	h.RedisConnected = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetSQLiteOK(v bool) {
	h.mu.Lock()
	h.SQLiteOK = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetTFBuilderOK(v bool) {
	h.mu.Lock()
	h.TFBuilderOK = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetIndicatorOK(v bool) {
	h.mu.Lock()
	h.IndicatorOK = v
	h.mu.Unlock()
}

func (h *HealthStatus) SetEnabledTFs(tfs []int) {
	h.mu.Lock()
	h.EnabledTFs = tfs
	h.mu.Unlock()
}

// CheckRedis pings Redis and records latency + connectivity.
func (h *HealthStatus) CheckRedis(ctx context.Context, rdb *goredis.Client) {
	start := time.Now()
	err := rdb.Ping(ctx).Err()
	latency := time.Since(start)

	h.mu.Lock()
	h.RedisConnected = err == nil
	h.RedisLatencyMs = float64(latency.Microseconds()) / 1000.0
	h.LastCheckAt = time.Now()
	h.mu.Unlock()
}

// CheckSQLite runs a trivial query and records latency + health.
func (h *HealthStatus) CheckSQLite(ctx context.Context, db *sql.DB) {
	start := time.Now()
	err := db.PingContext(ctx)
	latency := time.Since(start)

	h.mu.Lock()
	h.SQLiteOK = err == nil
	h.SQLiteLatencyMs = float64(latency.Microseconds()) / 1000.0
	h.LastCheckAt = time.Now()
	h.mu.Unlock()
}

// StartLivenessChecker runs periodic dependency checks.
func (h *HealthStatus) StartLivenessChecker(ctx context.Context, rdb *goredis.Client, sqlDB *sql.DB, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				if rdb != nil {
					h.CheckRedis(probeCtx, rdb)
				}
				if sqlDB != nil {
					h.CheckSQLite(probeCtx, sqlDB)
				}
				cancel()
			}
		}
	}()
}

// ServeHTTP handles the /healthz endpoint.
func (h *HealthStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Determine overall status
	overallStatus := "healthy"
	httpCode := http.StatusOK

	if !h.WSConnected || h.FeedStale || !h.RedisConnected || !h.SQLiteOK {
		overallStatus = "degraded"
		httpCode = http.StatusServiceUnavailable
	}
	if !h.RedisConnected && !h.SQLiteOK {
		overallStatus = "unhealthy"
	}

	// Tick age
	tickAge := ""
	if !h.LastTickTime.IsZero() {
		tickAge = time.Since(h.LastTickTime).Round(time.Millisecond).String()
	}

	status := struct {
		Status          string  `json:"status"`
		Uptime          string  `json:"uptime"`
		WSConnected     bool    `json:"ws_connected"`
		LastTickTime    string  `json:"last_tick_time"`
		TickAge         string  `json:"tick_age"`
		FeedStale       bool    `json:"feed_stale"`
		RedisConnected  bool    `json:"redis_connected"`
		RedisLatencyMs  float64 `json:"redis_latency_ms"`
		SQLiteOK        bool    `json:"sqlite_ok"`
		SQLiteLatencyMs float64 `json:"sqlite_latency_ms"`
		TFBuilderOK     bool    `json:"tf_builder_ok"`
		IndicatorOK     bool    `json:"indicator_ok"`
		EnabledTFs      []int   `json:"enabled_tfs"`
		LastCheckAt     string  `json:"last_check_at"`
	}{
		Status:          overallStatus,
		Uptime:          time.Since(h.StartedAt).Round(time.Second).String(),
		WSConnected:     h.WSConnected,
		LastTickTime:    h.LastTickTime.Format(time.RFC3339),
		TickAge:         tickAge,
		FeedStale:       h.FeedStale,
		RedisConnected:  h.RedisConnected,
		RedisLatencyMs:  h.RedisLatencyMs,
		SQLiteOK:        h.SQLiteOK,
		SQLiteLatencyMs: h.SQLiteLatencyMs,
		TFBuilderOK:     h.TFBuilderOK,
		IndicatorOK:     h.IndicatorOK,
		EnabledTFs:      h.EnabledTFs,
		LastCheckAt:     h.LastCheckAt.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	if httpCode != http.StatusOK {
		w.WriteHeader(httpCode)
	}
	json.NewEncoder(w).Encode(status)
}

// Server runs an HTTP server exposing /metrics and /healthz.
type Server struct {
	health *HealthStatus
	addr   string
	srv    *http.Server
}

// NewServer creates a metrics and health server (default registry).
func NewServer(addr string, health *HealthStatus) *Server {
	return newServer(addr, health, promhttp.Handler())
}

// NewServerWithRegistry is NewServer serving /metrics from reg only.
func NewServerWithRegistry(addr string, health *HealthStatus, reg prometheus.Gatherer) *Server {
	return newServer(addr, health, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
}

func newServer(addr string, health *HealthStatus, metricsHandler http.Handler) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsHandler)
	mux.HandleFunc("/healthz", health.ServeHTTP)

	return &Server{
		health: health,
		addr:   addr,
		srv: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Start launches the HTTP server in a goroutine.
func (s *Server) Start() {
	go func() {
		log.Printf("[metrics] server listening on %s", s.addr)
		if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("[metrics] server error: %v", err)
		}
	}()
}

// Stop gracefully shuts down the metrics server.
func (s *Server) Stop(ctx context.Context) {
	s.srv.Shutdown(ctx)
}
