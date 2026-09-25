package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/gateway"
	"trading-systemv1/internal/heartbeat"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/model"
	"trading-systemv1/internal/store/sqlite"
	"trading-systemv1/pkg/smartconnect"

	goredis "github.com/go-redis/redis/v8"
)

var processStart = time.Now()

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("[api_gateway] starting...")

	// Load holidays from NSE API / cache / fallback
	configDir := markethours.GetConfigDir()
	markethours.InitHolidays(configDir)
	markethours.StartBackgroundRefresher(context.Background(), configDir)

	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")
	listenAddr := getEnv("GATEWAY_ADDR", ":9090")
	enabledTFs := getEnv("ENABLED_TFS", "60,120,180,300,3600")
	subscribeTokens := getEnv("SUBSCRIBE_TOKENS", "1:99926000")

	// Connect to Redis
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("[api_gateway] redis connection failed: %v", err)
	}
	log.Printf("[api_gateway] redis connected at %s", redisAddr)

	// Parse config
	tfs := parseTFs(enabledTFs)
	tokenKeys := parseTokenKeys(subscribeTokens)
	indicators := parseIndicatorNames(getEnv("INDICATOR_CONFIGS", ""))

	// Open SQLite reader for candle backfill (graceful degradation if unavailable)
	var candleReader model.CandleReader
	sqlitePath := getEnv("SQLITE_PATH", "data/candles.db")
	if sr, err := sqlite.NewReader(sqlitePath); err != nil {
		log.Printf("[api_gateway] WARNING: sqlite reader unavailable (%s): %v — snapshot backfill disabled", sqlitePath, err)
	} else {
		candleReader = sr
		defer sr.Close()
		log.Printf("[api_gateway] sqlite reader opened: %s", sqlitePath)
	}

	// Hub manages all WebSocket connections
	hub := gateway.NewHub(rdb, tfs, tokenKeys, indicators, candleReader)

	// Pipeline, indicator and order stats come from snapshots the owning
	// processes write to Redis; heartbeats say which services are up.
	hbAgg := heartbeat.NewAggregator(rdb, heartbeat.KnownServices)
	hub.Heartbeats = hbAgg

	go hub.Run(ctx)
	go hub.StartIdleSweep(ctx) // evict idle per-channel seq/replay state

	// Initialize SmartConnect client for account data (optional)
	var accountSvc *gateway.AccountService
	angelAPIKey := getEnv("ANGEL_API_KEY", "")
	angelClientID := getEnv("ANGEL_CLIENT_CODE", "")
	angelPassword := getEnv("ANGEL_PASSWORD", "")
	angelTOTPSecret := getEnv("ANGEL_TOTP_SECRET", "")

	if angelAPIKey != "" && angelClientID != "" && angelPassword != "" && angelTOTPSecret != "" {
		log.Println("[api_gateway] 🔑 initializing Angel One SmartConnect with session manager...")

		// Create session manager for automatic session refresh
		sessionManager := smartconnect.NewSessionManager(angelAPIKey, angelClientID, angelPassword, angelTOTPSecret, false)
		log.Println("[api_gateway] 📦 session manager created (TTL: 30min, proactive refresh at: 20min)")

		// Login to Angel One
		if err := sessionManager.Login(); err != nil {
			log.Printf("[api_gateway] ⚠️  Angel One login failed: %v", err)
		} else {
			log.Println("[api_gateway] ✅ Angel One session established with auto-refresh")

			// Start proactive refresh background loop
			sessionManager.Start()

			// Open signal journal database for last orders
			journalPath := getEnv("STRAT_JOURNAL_PATH", "data/signal_journal.db")
			if journalDB, err := sqlite.NewReader(journalPath); err != nil {
				log.Printf("[api_gateway] ⚠️  signal journal unavailable: %v", err)
			} else {
				accountSvc = gateway.NewAccountServiceWithSessionManager(journalDB.DB(), sessionManager)
				log.Println("[api_gateway] ✅ Account service initialized with session manager")
			}
		}
	} else {
		log.Println("[api_gateway] ℹ️  Angel One credentials not configured, account endpoints disabled")
	}

	// Register all HTTP routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux, hub, rdb, ctx, tfs, tokenKeys, indicators, processStart, accountSvc)

	srv := &http.Server{Addr: listenAddr, Handler: mux}

	// Start heartbeat publisher for api_gateway itself
	go heartbeat.NewPublisher("api_gateway", rdb).Run(ctx)

	// Add /healthz/services endpoint showing all service heartbeats
	mux.HandleFunc("/healthz/services", func(w http.ResponseWriter, r *http.Request) {
		statuses := hbAgg.Status(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"services":    statuses,
			"all_healthy": hbAgg.AllHealthy(r.Context()),
		})
	})

	// Start metrics broadcast (every 2s)
	go hub.StartMetricsBroadcast(ctx, processStart)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("[api_gateway] ✅ serving at http://localhost%s", listenAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("[api_gateway] server error: %v", err)
		}
	}()

	<-sigCh
	log.Println("[api_gateway] shutting down...")
	cancel()
	srv.Shutdown(context.Background())
}

// ---- Config parsers ----

func parseTFs(s string) []int {
	var tfs []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n := 0
		for _, c := range p {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > 0 {
			tfs = append(tfs, n)
		}
	}
	return tfs
}

func parseTokenKeys(s string) []string {
	return config.ParseSubscribeTokenKeys(s)
}

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func parseIndicatorNames(s string) []string {
	defaults := []string{"EMA_6", "EMA_9", "EMA_21"}
	if s == "" {
		return defaults
	}

	var names []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		tokens := strings.SplitN(part, ":", 2)
		if len(tokens) != 2 {
			continue
		}
		typ := strings.ToUpper(strings.TrimSpace(tokens[0]))
		period := strings.TrimSpace(tokens[1])
		if typ == "" || period == "" {
			continue
		}
		names = append(names, typ+"_"+period)
	}
	if len(names) == 0 {
		return defaults
	}
	log.Printf("[api_gateway] loaded %d indicators from INDICATOR_CONFIGS", len(names))
	return names
}
