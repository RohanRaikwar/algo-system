package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"

	"trading-systemv1/internal/markethours"

	_ "github.com/mattn/go-sqlite3"
)

// allowedOrigins holds the configured allowed origins, parsed from ALLOWED_ORIGINS env var.
// Default "*" allows all origins (for development). Set to comma-separated origins in production.
var allowedOrigins = parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS"))

func parseAllowedOrigins(s string) []string {
	if s == "" {
		return []string{"*"}
	}
	var origins []string
	for _, o := range strings.Split(s, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		return []string{"*"}
	}
	return origins
}

func checkOrigin(r *http.Request) bool {
	for _, o := range allowedOrigins {
		if o == "*" {
			return true
		}
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser requests
	}
	for _, o := range allowedOrigins {
		if o == origin {
			return true
		}
	}
	log.Printf("[api_gateway] rejected WS origin: %s", origin)
	return false
}

var upgrader = websocket.Upgrader{
	CheckOrigin:       checkOrigin,
	EnableCompression: true,
}

// SetCORS sets CORS headers for REST endpoints.
// If ALLOWED_ORIGINS is specific, reflect only allowed request origins.
func SetCORS(w http.ResponseWriter, r *http.Request) {
	allowAny := false
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAny = true
			break
		}
	}

	if allowAny {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		reqOrigin := strings.TrimSpace(r.Header.Get("Origin"))
		for _, o := range allowedOrigins {
			if reqOrigin != "" && reqOrigin == o {
				w.Header().Set("Access-Control-Allow-Origin", o)
				w.Header().Set("Vary", "Origin")
				break
			}
		}
	}

	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// RegisterRoutes registers all HTTP routes on the provided mux.
func RegisterRoutes(mux *http.ServeMux, hub *Hub, rdb *goredis.Client, ctx context.Context, tfs []int, tokenKeys, indicators []string, processStart time.Time, accountSvc *AccountService) {
	journalPaths := resolveSignalJournalPaths()
	dailyAnalytics := NewDailyAnalyticsService(journalPaths, nil)

	// WebSocket endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[api_gateway] ws upgrade error: %v", err)
			return
		}
		lastTS := r.URL.Query().Get("last_ts")
		hub.HandleWSRequest(conn, lastTS)
	})

	// REST: latest indicator values
	mux.HandleFunc("/api/indicators/latest", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		latest := hub.GetLatestAll()
		json.NewEncoder(w).Encode(latest)
	})

	// REST: available timeframes
	mux.HandleFunc("/api/tfs", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		tfList := make([]TFInfo, len(tfs))
		for i, tf := range tfs {
			tfList[i] = TFInfo{Seconds: tf, Label: TFLabel(tf)}
		}
		json.NewEncoder(w).Encode(tfList)
	})

	// REST: config
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tfs":        tfs,
			"tokens":     tokenKeys,
			"indicators": indicators,
		})
	})

	// REST: GET/POST /api/indicators/active
	mux.HandleFunc("/api/indicators/active", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == "POST" {
			var req ActiveConfig
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			hub.SetActiveConfig(req)
			log.Printf("[api_gateway] active config updated: %d entries", len(req.Entries))

			// Publish unique indicator specs to Redis for indengine dynamic reload
			seen := make(map[string]bool)
			var specs []string
			for _, entry := range req.Entries {
				parts := strings.SplitN(entry.Name, "_", 2)
				if len(parts) == 2 {
					spec := parts[0] + ":" + parts[1]
					if !seen[spec] {
						seen[spec] = true
						specs = append(specs, spec)
					}
				}
			}
			if len(specs) > 0 {
				payload := strings.Join(specs, ",")
				if err := rdb.Publish(ctx, "config:indicators", payload).Err(); err != nil {
					log.Printf("[api_gateway] WARNING: failed to publish config:indicators: %v", err)
				} else {
					log.Printf("[api_gateway] published indicator config to indengine: %s", payload)
				}
			}

			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// GET
		json.NewEncoder(w).Encode(hub.GetActiveConfig())
	})

	// REST: system metrics snapshot
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		m := CollectMetrics(processStart)
		if v, ok := ReadIndicatorLatency(r.Context(), rdb); ok {
			m.IndicatorMs = v
		}
		if hub.Latency != nil {
			m.LatencyP50, m.LatencyP95, m.LatencyP99 = hub.Latency.Percentiles()
			m.LatencySamples = hub.Latency.Count()
		}
		FillServiceMetrics(r.Context(), &m, rdb, hub.Heartbeats)
		json.NewEncoder(w).Encode(m)
	})

	// REST: historical candles from Redis streams
	mux.HandleFunc("/api/candles", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")

		tfStr := r.URL.Query().Get("tf")
		token := r.URL.Query().Get("token")
		limitStr := r.URL.Query().Get("limit")
		beforeStr := r.URL.Query().Get("before")

		if tfStr == "" {
			tfStr = "60"
		}
		tfVal, _ := strconv.Atoi(tfStr)
		if tfVal <= 0 {
			tfVal = 60
		}

		limit := 200
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
				limit = l
			}
		}

		if token == "" && len(tokenKeys) > 0 {
			token = tokenKeys[0]
		}

		streamKey := fmt.Sprintf("candle:%ds:%s", tfVal, token)

		upperBound := "+"
		if beforeStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			} else if t, err := time.Parse(time.RFC3339, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			}
		}

		msgs, err := rdb.XRevRangeN(ctx, streamKey, upperBound, "-", int64(limit)).Result()
		if err != nil {
			json.NewEncoder(w).Encode([]interface{}{})
			return
		}

		// Reverse to chronological order
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}

		candles := make([]CandleOut, 0, len(msgs))
		for _, msg := range msgs {
			dataStr, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}
			var c CandleOut
			if err := json.Unmarshal([]byte(dataStr), &c); err != nil {
				continue
			}
			c.TF = tfVal
			if c.TS != "" {
				candles = append(candles, c)
			}
		}

		// SQLite backfill when Redis has fewer candles than requested
		if len(candles) < limit && hub.CandleReader != nil {
			parts := strings.SplitN(token, ":", 2)
			if len(parts) == 2 {
				// Determine afterTS from 'before' param. If no 'before', afterTS=0 → all data.
				var afterTS int64
				if beforeStr != "" {
					if t, err := time.Parse(time.RFC3339Nano, beforeStr); err == nil {
						afterTS = t.Unix() - int64(tfVal*limit) - 1
					} else if t, err := time.Parse(time.RFC3339, beforeStr); err == nil {
						afterTS = t.Unix() - int64(tfVal*limit) - 1
					}
				}
				// afterTS = 0 means query all available data from SQLite

				sqlCandles, sqlErr := hub.CandleReader.ReadTFCandles(parts[0], parts[1], tfVal, afterTS)
				if sqlErr == nil && len(sqlCandles) > 0 {
					// Dedup: collect existing timestamps
					seen := make(map[string]bool, len(candles))
					for _, c := range candles {
						seen[c.TS] = true
					}
					for _, tc := range sqlCandles {
						ts := tc.TS.UTC().Format(time.RFC3339)
						if !seen[ts] {
							candles = append(candles, CandleOut{
								TS: ts, Open: float64(tc.Open), High: float64(tc.High),
								Low: float64(tc.Low), Close: float64(tc.Close),
								Volume: float64(tc.Volume), Count: float64(tc.Count),
								Token: parts[1], Exchange: parts[0], TF: tfVal,
							})
							seen[ts] = true
						}
					}
					// Re-sort and cap
					sort.Slice(candles, func(i, j int) bool { return candles[i].TS < candles[j].TS })
					if len(candles) > limit {
						candles = candles[len(candles)-limit:]
					}
				}
			}
		}

		json.NewEncoder(w).Encode(candles)
	})

	// REST: historical indicator values from Redis streams
	mux.HandleFunc("/api/indicators/history", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")

		name := r.URL.Query().Get("name")
		tfStr := r.URL.Query().Get("tf")
		token := r.URL.Query().Get("token")
		limitStr := r.URL.Query().Get("limit")

		if name == "" || tfStr == "" {
			json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		tfVal, _ := strconv.Atoi(tfStr)
		if tfVal <= 0 {
			tfVal = 60
		}
		limit := 300
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
				limit = l
			}
		}
		if token == "" && len(tokenKeys) > 0 {
			token = tokenKeys[0]
		}

		streamKey := fmt.Sprintf("ind:%s:%ds:%s", name, tfVal, token)

		upperBound := "+"
		if beforeStr := r.URL.Query().Get("before"); beforeStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			} else if t, err := time.Parse(time.RFC3339, beforeStr); err == nil {
				upperBound = fmt.Sprintf("%d-0", t.UnixMilli()-1)
			}
		}

		msgs, err := rdb.XRevRangeN(ctx, streamKey, upperBound, "-", int64(limit)).Result()
		if err != nil {
			json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		// Reverse to chronological order
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}

		points := make([]IndPoint, 0, len(msgs))
		for _, msg := range msgs {
			dataStr, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}
			var p struct {
				Value float64 `json:"value"`
				TS    string  `json:"ts"`
				Ready bool    `json:"ready"`
			}
			if err := json.Unmarshal([]byte(dataStr), &p); err != nil {
				continue
			}
			if p.Ready && p.TS != "" {
				points = append(points, IndPoint{Value: p.Value, TS: p.TS, Ready: p.Ready})
			}
		}

		json.NewEncoder(w).Encode(points)
	})

	// REST: gap backfill — returns buffered envelopes for a channel between from_seq and to_seq
	mux.HandleFunc("/api/missed", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		channel := r.URL.Query().Get("channel")
		fromStr := r.URL.Query().Get("from_seq")
		toStr := r.URL.Query().Get("to_seq")

		if channel == "" || fromStr == "" || toStr == "" {
			http.Error(w, `{"error":"channel, from_seq, and to_seq are required"}`, http.StatusBadRequest)
			return
		}

		fromSeq, err := strconv.ParseInt(fromStr, 10, 64)
		if err != nil {
			http.Error(w, `{"error":"invalid from_seq"}`, http.StatusBadRequest)
			return
		}
		toSeq, err := strconv.ParseInt(toStr, 10, 64)
		if err != nil {
			http.Error(w, `{"error":"invalid to_seq"}`, http.StatusBadRequest)
			return
		}

		if fromSeq > toSeq {
			http.Error(w, `{"error":"from_seq must be <= to_seq"}`, http.StatusBadRequest)
			return
		}

		json.NewEncoder(w).Encode(buildMissedResponse(hub, channel, fromSeq, toSeq, r.URL.Query().Get("epoch")))
	})

	// REST: strategy signals from SQLite journal
	mux.HandleFunc("/api/signals", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}
		strategyFilter := r.URL.Query().Get("strategy")

		// Read signals from ALL journal SQLite databases.
		// In staging mode only stratengine_ind runs (writes to STRAT_IND_JOURNAL_PATH),
		// while in production stratengine also runs (writes to STRAT_JOURNAL_PATH).
		// Query both and merge results.
		type signalRecord struct {
			ID          int64  `json:"id"`
			Strategy    string `json:"strategy"`
			Action      string `json:"action"`
			Side        string `json:"side"`
			MarketState string `json:"market_state"`
			Token       string `json:"token"`
			Exchange    string `json:"exchange"`
			Reason      string `json:"reason"`
			EMAValues   string `json:"ema_values"`
			Price       int64  `json:"price"`
			Qty         int64  `json:"qty"`
			LiveMode    bool   `json:"live_mode"`
			ProfitCap   bool   `json:"profit_cap"`
			CandleTS    string `json:"candle_ts"`
			CreatedAt   string `json:"created_at"`
		}

		var allRecords []signalRecord

		for _, jPath := range journalPaths {
			db, err := openSignalDB(jPath)
			if err != nil {
				continue
			}
			rows, err := querySignalRowsForAPI(db, limit, strategyFilter)
			if err != nil {
				db.Close()
				continue
			}
			for rows.Next() {
				var r signalRecord
				var liveModeInt, profitCapInt int
				if err := rows.Scan(&r.ID, &r.Strategy, &r.Action, &r.Side, &r.MarketState, &r.Token, &r.Exchange,
					&r.Reason, &r.EMAValues, &r.Price, &r.Qty, &liveModeInt, &profitCapInt, &r.CandleTS, &r.CreatedAt); err != nil {
					continue
				}
				r.LiveMode = liveModeInt == 1
				r.ProfitCap = profitCapInt == 1
				allRecords = append(allRecords, r)
			}
			rows.Close()
			db.Close()
		}

		// Sort by created_at descending and cap at limit
		sort.Slice(allRecords, func(i, j int) bool {
			return allRecords[i].CreatedAt > allRecords[j].CreatedAt
		})
		if len(allRecords) > limit {
			allRecords = allRecords[:limit]
		}
		if allRecords == nil {
			allRecords = []signalRecord{}
		}
		json.NewEncoder(w).Encode(allRecords)
	})

	// REST: strike info (resolved FNO instruments)
	mux.HandleFunc("/api/strike", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		val, err := rdb.Get(ctx, "strike:info").Result()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"resolved": false})
			return
		}
		w.Write([]byte(val))
	})

	// REST: P&L summary from stratengine
	mux.HandleFunc("/api/pnl", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		val, err := rdb.Get(ctx, "pnl:summary").Result()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"realized_pnl": 0, "total_trades": 0, "wins": 0, "losses": 0,
				"win_rate": 0, "open_positions": 0, "total_exposure": 0,
			})
			return
		}
		w.Write([]byte(val))
	})
	mux.HandleFunc("/api/pnl/daily", makeDailyPnLHandler(dailyAnalytics.DailyPnL))
	mux.HandleFunc("/api/orders/daily", makeDailyOrdersHandler(dailyAnalytics.DailyOrders))

	// REST: account balance and orders (Angel One integration)
	if accountSvc != nil {
		mux.HandleFunc("/api/account/balance", makeAccountBalanceHandler(accountSvc))
		mux.HandleFunc("/api/account/orders", makeLastOrdersHandler(accountSvc))
		mux.HandleFunc("/api/account/profile", makeUserProfileHandler(accountSvc))
		mux.HandleFunc("/api/account/session-health", makeSessionHealthHandler(accountSvc))
		log.Println("✅ Account routes registered")
	}

	// REST: comprehensive session health (all services)
	mux.HandleFunc("/api/sessions/health", makeAllSessionsHealthHandler(rdb, ctx, accountSvc))

	// REST: trading configuration
	mux.HandleFunc("/api/trading/config", makeTradingConfigHandler(rdb, ctx))

	// REST: market status (holiday detection)
	mux.HandleFunc("/api/market-status", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")

		now := time.Now()
		isOpen := markethours.IsMarketOpen(now)
		holidayName, isHoliday := markethours.TodayHoliday(now)
		nextOpen := markethours.NextOpen(now)

		status := "open"
		if !isOpen {
			status = "closed"
			if isHoliday {
				status = "holiday"
			}
		}

		nextOpenIST := nextOpen.In(markethours.IST)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        status,
			"isOpen":        isOpen,
			"isHoliday":     isHoliday,
			"holidayName":   holidayName,
			"nextOpen":      nextOpen.Format(time.RFC3339),
			"nextOpenLabel": nextOpenIST.Format("Mon 02 Jan, 3:04 PM"),
		})
	})

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")

		redisOK := true
		if err := rdb.Ping(r.Context()).Err(); err != nil {
			redisOK = false
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "ok",
			"redis":      redisOK,
			"ws_clients": hub.ClientCount(),
			"uptime_sec": int64(time.Since(processStart).Seconds()),
			"ts":         time.Now().UTC().Format(time.RFC3339Nano),
		})
	})
}

// openSignalDB opens the strategy signal journal DB (read-only).
func openSignalDB(path string) (*sql.DB, error) {
	return sql.Open("sqlite3", path+"?mode=ro&_journal=WAL")
}

func querySignalRowsForAPI(db *sql.DB, limit int, strategy string) (*sql.Rows, error) {
	// Build optional WHERE clause for strategy filtering.
	whereClause := ""
	var args []interface{}
	if strategy != "" {
		whereClause = " WHERE strategy = ?"
		args = append(args, strategy)
	}
	args = append(args, limit)

	queries := []string{
		// Latest schema: qty + market_state + live_mode + profit_cap.
		`SELECT id, strategy, action, COALESCE(side,''), COALESCE(market_state,''), token, exchange, reason,
		        COALESCE(ema_values,''), COALESCE(price,0),
		        CASE WHEN qty IS NULL OR qty <= 0 THEN 1 ELSE qty END,
		        COALESCE(live_mode,0), COALESCE(profit_cap,0),
		        candle_ts, created_at
		 FROM signals` + whereClause + `
		 ORDER BY id DESC
		 LIMIT ?`,
		// Legacy schema without market_state.
		`SELECT id, strategy, action, COALESCE(side,''), '' AS market_state, token, exchange, reason,
		        COALESCE(ema_values,''), COALESCE(price,0),
		        CASE WHEN qty IS NULL OR qty <= 0 THEN 1 ELSE qty END,
		        COALESCE(live_mode,0), COALESCE(profit_cap,0),
		        candle_ts, created_at
		 FROM signals` + whereClause + `
		 ORDER BY id DESC
		 LIMIT ?`,
		// Legacy schema without qty.
		`SELECT id, strategy, action, COALESCE(side,''), COALESCE(market_state,''), token, exchange, reason,
		        COALESCE(ema_values,''), COALESCE(price,0),
		        1 AS qty,
		        COALESCE(live_mode,0), COALESCE(profit_cap,0),
		        candle_ts, created_at
		 FROM signals` + whereClause + `
		 ORDER BY id DESC
		 LIMIT ?`,
		// Oldest known schema without qty + market_state.
		`SELECT id, strategy, action, COALESCE(side,''), '' AS market_state, token, exchange, reason,
		        COALESCE(ema_values,''), COALESCE(price,0),
		        1 AS qty,
		        COALESCE(live_mode,0), COALESCE(profit_cap,0),
		        candle_ts, created_at
		 FROM signals` + whereClause + `
		 ORDER BY id DESC
		 LIMIT ?`,
	}

	var lastErr error
	for _, q := range queries {
		rows, err := db.Query(q, args...)
		if err == nil {
			return rows, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
// TradingConfig represents the trading configuration structure
type TradingConfig struct {
	LiveOrders      bool    `json:"liveOrders"`
	Quantity        int     `json:"quantity"`
	TargetProfitPct float64 `json:"targetProfitPct"`
	HardSLPct       float64 `json:"hardSLPct"`
	TrailSLPct      float64 `json:"trailSLPct"`
	TrailStartPct   float64 `json:"trailStartPct"`
	KillSwitch      bool    `json:"killSwitch"`
	MaxDailyLoss    int     `json:"maxDailyLoss"`
	SkipFirstMinutes int    `json:"skipFirstMinutes"`
	SkipLastMinutes  int    `json:"skipLastMinutes"`
}

// makeTradingConfigHandler creates a handler for GET/POST /api/trading/config
func makeTradingConfigHandler(rdb *goredis.Client, ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		w.Header().Set("Content-Type", "application/json")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == "GET" {
			// Get current configuration from Redis
			val, err := rdb.Get(ctx, "trading:config").Result()
			if err != nil {
				// Return default configuration if not found
				defaultConfig := TradingConfig{
					LiveOrders:       false,
					Quantity:         1,
					TargetProfitPct:  1.5,
					HardSLPct:        20.0,
					TrailSLPct:       10.0,
					TrailStartPct:    5.0,
					KillSwitch:       false,
					MaxDailyLoss:     10000,
					SkipFirstMinutes: 15,
					SkipLastMinutes:  30,
				}
				json.NewEncoder(w).Encode(defaultConfig)
				return
			}
			
			var config TradingConfig
			if err := json.Unmarshal([]byte(val), &config); err != nil {
				http.Error(w, "Failed to parse configuration", http.StatusInternalServerError)
				return
			}
			
			json.NewEncoder(w).Encode(config)
			return
		}

		if r.Method == "POST" {
			var config TradingConfig
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			// Validate configuration
			if config.Quantity < 1 || config.Quantity > 10 {
				http.Error(w, "Quantity must be between 1 and 10", http.StatusBadRequest)
				return
			}
			if config.TargetProfitPct < 0.5 || config.TargetProfitPct > 10 {
				http.Error(w, "Target profit must be between 0.5% and 10%", http.StatusBadRequest)
				return
			}
			if config.HardSLPct < 5 || config.HardSLPct > 50 {
				http.Error(w, "Hard SL must be between 5% and 50%", http.StatusBadRequest)
				return
			}

			// Save configuration to Redis
			configJSON, err := json.Marshal(config)
			if err != nil {
				http.Error(w, "Failed to serialize configuration", http.StatusInternalServerError)
				return
			}

			if err := rdb.Set(ctx, "trading:config", configJSON, 0).Err(); err != nil {
				http.Error(w, "Failed to save configuration", http.StatusInternalServerError)
				return
			}

			// Publish configuration update to strategy engine
			if err := rdb.Publish(ctx, "cmd:config_update", string(configJSON)).Err(); err != nil {
				log.Printf("[api_gateway] Failed to publish config update: %v", err)
			}

			log.Printf("[api_gateway] Trading configuration updated: LiveOrders=%v, Qty=%d, TargetProfit=%.1f%%, HardSL=%.1f%%", 
				config.LiveOrders, config.Quantity, config.TargetProfitPct, config.HardSLPct)

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "Configuration saved successfully",
			})
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// makeAllSessionsHealthHandler creates HTTP handler for comprehensive session health
// Checks both API Gateway session and StratEngine session (via Redis)
func makeAllSessionsHealthHandler(rdb *goredis.Client, ctx context.Context, accountSvc *AccountService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SetCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		response := map[string]interface{}{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"sessions":  make(map[string]interface{}),
		}

		// 1. API Gateway Session Health
		if accountSvc != nil && accountSvc.sessionManager != nil {
			sessionInfo := accountSvc.sessionManager.GetSessionInfo()
			response["sessions"].(map[string]interface{})["api_gateway"] = map[string]interface{}{
				"service":              "api_gateway",
				"purpose":              "Balance, Orders, Profile",
				"healthy":              accountSvc.sessionManager.IsHealthy(),
				"valid":                sessionInfo["valid"],
				"age_minutes":          sessionInfo["age_minutes"],
				"time_until_refresh":   sessionInfo["time_until_refresh"],
				"circuit_breaker_open": sessionInfo["circuit_breaker_open"],
				"total_refreshes":      sessionInfo["total_refreshes"],
				"successful_refreshes": sessionInfo["successful_refreshes"],
				"failed_refreshes":     sessionInfo["failed_refreshes"],
				"last_refresh":         sessionInfo["last_refresh"],
			}
		} else {
			response["sessions"].(map[string]interface{})["api_gateway"] = map[string]interface{}{
				"service": "api_gateway",
				"purpose": "Balance, Orders, Profile",
				"status":  "not_configured",
				"message": "Angel One credentials not configured",
			}
		}

		// 2. StratEngine Session Health (via Redis snapshot)
		// StratEngine publishes its session health to Redis
		stratSessionKey := "session:stratengine:health"
		if val, err := rdb.Get(ctx, stratSessionKey).Result(); err == nil {
			var stratSession map[string]interface{}
			if json.Unmarshal([]byte(val), &stratSession) == nil {
				response["sessions"].(map[string]interface{})["stratengine"] = stratSession
			} else {
				response["sessions"].(map[string]interface{})["stratengine"] = map[string]interface{}{
					"service": "stratengine",
					"purpose": "Order Execution",
					"status":  "unknown",
					"message": "Failed to parse session health data",
				}
			}
		} else {
			response["sessions"].(map[string]interface{})["stratengine"] = map[string]interface{}{
				"service": "stratengine",
				"purpose": "Order Execution",
				"status":  "unknown",
				"message": "Session health data not available (service may not be running)",
			}
		}

		// 3. Overall Health Status
		allHealthy := true
		sessionCount := 0
		healthyCount := 0

		for _, sessionData := range response["sessions"].(map[string]interface{}) {
			sessionCount++
			if sessionMap, ok := sessionData.(map[string]interface{}); ok {
				if healthy, exists := sessionMap["healthy"]; exists {
					if h, ok := healthy.(bool); ok && h {
						healthyCount++
					} else {
						allHealthy = false
					}
				} else if status, exists := sessionMap["status"]; exists {
					if s, ok := status.(string); ok && s != "healthy" {
						allHealthy = false
					}
				}
			}
		}

		response["overall"] = map[string]interface{}{
			"all_healthy":   allHealthy,
			"total_sessions": sessionCount,
			"healthy_sessions": healthyCount,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}
