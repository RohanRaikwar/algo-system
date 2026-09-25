// cmd/histdata downloads historical candle data from Angel One SmartAPI
// and stores it into a separate SQLite database (data/historical.db) for backtesting.
//
// Usage:
//
//	source ../../.env && go run ./cmd/histdata --token=99926000 --from=2026-03-01 --to=2026-03-07
//	go run ./cmd/histdata --help
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pquerna/otp/totp"

	"trading-systemv1/pkg/smartconnect"
)

// ── Interval → TF seconds mapping ──

var intervalToTF = map[string]int{
	"ONE_MINUTE":     60,
	"THREE_MINUTE":   180,
	"FIVE_MINUTE":    300,
	"TEN_MINUTE":     600,
	"FIFTEEN_MINUTE": 900,
	"THIRTY_MINUTE":  1800,
	"ONE_HOUR":       3600,
	"ONE_DAY":        86400,
}

// SmartAPI limits the max candles per request. We chunk by day for minute intervals
// and by 30 days for daily intervals.
var intervalMaxDays = map[string]int{
	"ONE_MINUTE":     1, // ~375 candles/day
	"THREE_MINUTE":   3,
	"FIVE_MINUTE":    5,
	"TEN_MINUTE":     10,
	"FIFTEEN_MINUTE": 15,
	"THIRTY_MINUTE":  30,
	"ONE_HOUR":       30,
	"ONE_DAY":        365,
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	// ── CLI Flags ──
	exchange := flag.String("exchange", "NSE", "Exchange: NSE, BSE, NFO, MCX")
	token := flag.String("token", "99926000", "Symbol token (e.g. 99926000 for NIFTY 50)")
	interval := flag.String("interval", "ONE_MINUTE", "Candle interval: ONE_MINUTE, THREE_MINUTE, FIVE_MINUTE, TEN_MINUTE, FIFTEEN_MINUTE, THIRTY_MINUTE, ONE_HOUR, ONE_DAY")
	fromDate := flag.String("from", "", "Start date YYYY-MM-DD (required)")
	toDate := flag.String("to", "", "End date YYYY-MM-DD (default: today)")
	dbPath := flag.String("db", "data/historical.db", "Path to historical SQLite database")
	delay := flag.Duration("delay", 350*time.Millisecond, "Delay between API calls for rate limiting")
	flag.Parse()

	if *fromDate == "" {
		fmt.Println("Usage: histdata --from=YYYY-MM-DD [--to=YYYY-MM-DD] [--token=99926000] [--interval=ONE_MINUTE] [--exchange=NSE] [--db=data/historical.db]")
		os.Exit(1)
	}

	*interval = strings.ToUpper(*interval)
	tf, ok := intervalToTF[*interval]
	if !ok {
		log.Fatalf("[histdata] unknown interval %q. Valid: ONE_MINUTE, THREE_MINUTE, FIVE_MINUTE, TEN_MINUTE, FIFTEEN_MINUTE, THIRTY_MINUTE, ONE_HOUR, ONE_DAY", *interval)
	}

	from, err := time.Parse("2006-01-02", *fromDate)
	if err != nil {
		log.Fatalf("[histdata] invalid --from date %q: %v", *fromDate, err)
	}

	to := time.Now()
	if *toDate != "" {
		to, err = time.Parse("2006-01-02", *toDate)
		if err != nil {
			log.Fatalf("[histdata] invalid --to date %q: %v", *toDate, err)
		}
	}

	if from.After(to) {
		log.Fatal("[histdata] --from date is after --to date")
	}

	// ── Angel One Login ──
	apiKey := mustEnv("ANGEL_API_KEY")
	clientCode := mustEnv("ANGEL_CLIENT_CODE")
	password := mustEnv("ANGEL_PASSWORD")
	totpSecret := mustEnv("ANGEL_TOTP_SECRET")

	totpCode, err := totp.GenerateCode(totpSecret, time.Now())
	if err != nil {
		log.Fatalf("[histdata] TOTP generation failed: %v", err)
	}

	sc := smartconnect.NewSmartConnect(smartconnect.Config{APIKey: apiKey})
	_, err = sc.GenerateSession(clientCode, password, totpCode)
	if err != nil {
		log.Fatalf("[histdata] login failed: %v", err)
	}
	log.Println("[histdata] ✅ logged in to Angel One SmartAPI")

	// ── Open separate historical SQLite DB ──
	db, err := openHistDB(*dbPath)
	if err != nil {
		log.Fatalf("[histdata] sqlite open failed: %v", err)
	}
	defer db.Close()

	// ── Download loop: chunk date range ──
	chunkDays := intervalMaxDays[*interval]
	totalCandles := 0
	totalRequests := 0

	fmt.Println()
	fmt.Printf("╔══════════════════════════════════════════════════╗\n")
	fmt.Printf("║  HISTORICAL DATA DOWNLOAD                       ║\n")
	fmt.Printf("╠══════════════════════════════════════════════════╣\n")
	fmt.Printf("║  Exchange:  %-36s  ║\n", *exchange)
	fmt.Printf("║  Token:     %-36s  ║\n", *token)
	fmt.Printf("║  Interval:  %-36s  ║\n", *interval)
	fmt.Printf("║  From:      %-36s  ║\n", from.Format("2006-01-02"))
	fmt.Printf("║  To:        %-36s  ║\n", to.Format("2006-01-02"))
	fmt.Printf("║  DB:        %-36s  ║\n", *dbPath)
	fmt.Printf("╚══════════════════════════════════════════════════╝\n")
	fmt.Println()

	cursor := from
	for cursor.Before(to) || cursor.Equal(to) {
		chunkEnd := cursor.AddDate(0, 0, chunkDays-1)
		if chunkEnd.After(to) {
			chunkEnd = to
		}

		// SmartAPI date format: "YYYY-MM-DD HH:MM"
		fromStr := cursor.Format("2006-01-02") + " 09:00"
		toStr := chunkEnd.Format("2006-01-02") + " 15:30"

		params := map[string]any{
			"exchange":    *exchange,
			"symboltoken": *token,
			"interval":    *interval,
			"fromdate":    fromStr,
			"todate":      toStr,
		}

		resp, err := sc.GetCandleData(params)
		totalRequests++
		if err != nil {
			log.Printf("[histdata] ⚠️  API error for %s → %s: %v (skipping)", fromStr, toStr, err)
			cursor = chunkEnd.AddDate(0, 0, 1)
			time.Sleep(*delay)
			continue
		}

		candles := parseCandleResponse(resp)
		if len(candles) > 0 {
			inserted, err := insertCandles(db, *exchange, *token, tf, candles)
			if err != nil {
				log.Printf("[histdata] ⚠️  DB insert error: %v", err)
			} else {
				totalCandles += inserted
				log.Printf("[histdata] %s → %s: %d candles stored", fromStr, toStr, inserted)
			}
		} else {
			log.Printf("[histdata] %s → %s: no data (market holiday or off-hours)", fromStr, toStr)
		}

		cursor = chunkEnd.AddDate(0, 0, 1)
		if cursor.Before(to) || cursor.Equal(to) {
			time.Sleep(*delay) // rate limit
		}
	}

	// ── Summary ──
	fmt.Println()
	fmt.Printf("╔══════════════════════════════════════════════════╗\n")
	fmt.Printf("║  DOWNLOAD COMPLETE                              ║\n")
	fmt.Printf("╠══════════════════════════════════════════════════╣\n")
	fmt.Printf("║  Total candles stored: %-25d  ║\n", totalCandles)
	fmt.Printf("║  API requests made:    %-25d  ║\n", totalRequests)
	fmt.Printf("║  Database:             %-25s  ║\n", *dbPath)
	fmt.Printf("╚══════════════════════════════════════════════════╝\n")

	// Logout
	_ = logoutQuietly(sc, clientCode)
}

// ── SQLite for historical data (separate DB) ──

func openHistDB(dbPath string) (*sql.DB, error) {
	// Ensure parent directory exists
	if dir := dirOf(dbPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS historical_candles (
			exchange   TEXT    NOT NULL,
			token      TEXT    NOT NULL,
			tf         INTEGER NOT NULL,
			ts         INTEGER NOT NULL,
			open       REAL    NOT NULL,
			high       REAL    NOT NULL,
			low        REAL    NOT NULL,
			close      REAL    NOT NULL,
			volume     INTEGER NOT NULL,
			PRIMARY KEY (exchange, token, tf, ts)
		);

		CREATE INDEX IF NOT EXISTS idx_hist_ts ON historical_candles (exchange, token, tf, ts);
	`)
	if err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	log.Printf("[histdata] opened historical DB at %s", dbPath)
	return db, nil
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}

// ── Parse SmartAPI response ──
// Response format: {"data": [[timestamp, open, high, low, close, volume], ...]}

type candleRow struct {
	TS     time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

func parseCandleResponse(resp map[string]any) []candleRow {
	data, ok := resp["data"]
	if !ok || data == nil {
		return nil
	}

	rows, ok := data.([]any)
	if !ok {
		return nil
	}

	var result []candleRow
	for _, row := range rows {
		arr, ok := row.([]any)
		if !ok || len(arr) < 6 {
			continue
		}

		// arr[0] = timestamp string "2021-02-08T09:15:00+05:30"
		tsStr, ok := arr[0].(string)
		if !ok {
			continue
		}

		ts, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			// Try alternate format
			ts, err = time.Parse("2006-01-02T15:04:05", tsStr)
			if err != nil {
				continue
			}
		}

		c := candleRow{
			TS:     ts,
			Open:   toFloat(arr[1]),
			High:   toFloat(arr[2]),
			Low:    toFloat(arr[3]),
			Close:  toFloat(arr[4]),
			Volume: toInt64(arr[5]),
		}
		result = append(result, c)
	}

	return result
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		f := 0.0
		fmt.Sscanf(t, "%f", &f)
		return f
	default:
		return 0
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(math.Round(t))
	case int:
		return int64(t)
	case int64:
		return t
	case string:
		var n int64
		fmt.Sscanf(t, "%d", &n)
		return n
	default:
		return 0
	}
}

// ── Insert candles into SQLite ──

func insertCandles(db *sql.DB, exchange, token string, tf int, candles []candleRow) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO historical_candles (exchange, token, tf, ts, open, high, low, close, volume)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	count := 0
	for _, c := range candles {
		_, err := stmt.Exec(exchange, token, tf, c.TS.Unix(), c.Open, c.High, c.Low, c.Close, c.Volume)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		count++
	}

	return count, tx.Commit()
}

// ── Helpers ──

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[histdata] required env var %s not set. Source your .env first: source ../../.env", key)
	}
	return v
}

func logoutQuietly(sc *smartconnect.SmartConnect, clientCode string) error {
	_, err := sc.TerminateSession(clientCode)
	if err != nil {
		log.Printf("[histdata] logout warning: %v", err)
	}
	return err
}
