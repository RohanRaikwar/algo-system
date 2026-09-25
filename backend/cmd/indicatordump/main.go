// cmd/indicatordump exports 1-minute EMA6, EMA9, SMA21 values from historical DB to JSON.
//
// Usage:
//
//	go run ./cmd/indicatordump --db=data/historical.db --exchange=NSE --token=99926000 --from=2026-03-19 --to=2026-03-20 --out=results/indicator_values_1m.json
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"trading-systemv1/internal/indicator"
	"trading-systemv1/internal/model"
)

type indicatorPoint struct {
	TSUTC      string  `json:"ts_utc"`
	TSIST      string  `json:"ts_ist"`
	TSUnix     int64   `json:"ts_unix"`
	CloseRupee float64 `json:"close_rupee"`
	EMA6       float64 `json:"ema6"`
	EMA9       float64 `json:"ema9"`
	SMA21      float64 `json:"sma21"`
}

type dumpResult struct {
	Exchange string           `json:"exchange"`
	Token    string           `json:"token"`
	TF       string           `json:"tf"`
	From     string           `json:"from"`
	To       string           `json:"to"`
	Points   int              `json:"points"`
	Latest   *indicatorPoint  `json:"latest,omitempty"`
	Series   []indicatorPoint `json:"series"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	dbPath := flag.String("db", "data/historical.db", "Path to historical SQLite DB")
	exchange := flag.String("exchange", "NSE", "Exchange")
	token := flag.String("token", "99926000", "Symbol token")
	from := flag.String("from", "", "Start date YYYY-MM-DD (optional)")
	to := flag.String("to", "", "End date YYYY-MM-DD (optional)")
	out := flag.String("out", "results/indicator_values_1m.json", "Output JSON file path")
	flag.Parse()

	db, err := sql.Open("sqlite3", *dbPath+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("[indicatordump] open db: %v", err)
	}
	defer db.Close()

	query := `
		SELECT ts, close
		FROM historical_candles
		WHERE exchange = ? AND token = ? AND tf = 60`
	args := []any{*exchange, *token}

	if *from != "" {
		fromTime, err := time.Parse("2006-01-02", *from)
		if err != nil {
			log.Fatalf("[indicatordump] invalid --from %q: %v", *from, err)
		}
		query += " AND ts >= ?"
		args = append(args, fromTime.Unix())
	}
	if *to != "" {
		toTime, err := time.Parse("2006-01-02", *to)
		if err != nil {
			log.Fatalf("[indicatordump] invalid --to %q: %v", *to, err)
		}
		toTime = toTime.Add(24*time.Hour - time.Second)
		query += " AND ts <= ?"
		args = append(args, toTime.Unix())
	}

	query += " ORDER BY ts ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Fatalf("[indicatordump] query: %v", err)
	}
	defer rows.Close()

	ema6 := indicator.NewEMA(6)
	ema9 := indicator.NewEMA(9)
	sma21 := indicator.NewSMA(21)

	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		ist = time.FixedZone("IST", 5*3600+30*60)
	}

	series := make([]indicatorPoint, 0, 512)

	for rows.Next() {
		var tsUnix int64
		var closeRupee float64
		if err := rows.Scan(&tsUnix, &closeRupee); err != nil {
			log.Fatalf("[indicatordump] scan: %v", err)
		}

		closePaise := int64(math.Round(closeRupee * 100))
		candle := model.Candle{Close: closePaise}
		ema6.Update(candle)
		ema9.Update(candle)
		sma21.Update(candle)

		if !(ema6.Ready() && ema9.Ready() && sma21.Ready()) {
			continue
		}

		ts := time.Unix(tsUnix, 0).UTC()
		p := indicatorPoint{
			TSUTC:      ts.Format(time.RFC3339),
			TSIST:      ts.In(ist).Format("2006-01-02 15:04:05"),
			TSUnix:     tsUnix,
			CloseRupee: closeRupee,
			EMA6:       round2(ema6.Value() / 100.0),
			EMA9:       round2(ema9.Value() / 100.0),
			SMA21:      round2(sma21.Value() / 100.0),
		}
		series = append(series, p)
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("[indicatordump] rows: %v", err)
	}

	var latest *indicatorPoint
	if len(series) > 0 {
		last := series[len(series)-1]
		latest = &last
	}

	outData := dumpResult{
		Exchange: *exchange,
		Token:    *token,
		TF:       "1m",
		From:     *from,
		To:       *to,
		Points:   len(series),
		Latest:   latest,
		Series:   series,
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("[indicatordump] mkdir: %v", err)
	}

	b, err := json.MarshalIndent(outData, "", "  ")
	if err != nil {
		log.Fatalf("[indicatordump] marshal: %v", err)
	}

	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatalf("[indicatordump] write: %v", err)
	}

	fmt.Printf("✅ Indicator JSON written: %s (points=%d)\n", *out, len(series))
	if latest != nil {
		fmt.Printf("Latest @ %s IST -> EMA6=%.2f EMA9=%.2f SMA21=%.2f\n", latest.TSIST, latest.EMA6, latest.EMA9, latest.SMA21)
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
