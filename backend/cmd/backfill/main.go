// Command backfill fills missing 1m index candles (and the timeframes built
// from them) for every underlying from Angel One's historical API, into the
// candles database. docker-entrypoint.sh runs it before the services start,
// so a new index (SENSEX) begins with its past days and downtime gaps are
// closed. It always exits 0: missing history must not stop trading.
//
//	BACKFILL_DAYS=30 go run ./cmd/backfill
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"trading-systemv1/config"
	"trading-systemv1/internal/backfill"
	"trading-systemv1/internal/markethours"
	"trading-systemv1/internal/store/sqlite"
	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	if strings.EqualFold(config.GetEnv("BACKFILL_ENABLED", "true"), "false") {
		log.Println("[backfill] disabled (BACKFILL_ENABLED=false)")
		return
	}
	if strings.EqualFold(config.GetEnv("STAGING_MODE", "false"), "true") {
		log.Println("[backfill] staging: simulated feed, nothing to backfill")
		return
	}
	cfg := config.Load()
	if cfg.AngelAPIKey == "" || cfg.AngelClientCode == "" || cfg.AngelPassword == "" || cfg.AngelTOTPSecret == "" {
		log.Println("[backfill] no Angel credentials; skipped")
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	markethours.InitHolidays(markethours.GetConfigDir())

	path := config.GetEnv("SQLITE_PATH", "data/candles.db")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	w, err := sqlite.New(sqlite.WriterConfig{DBPath: path}) // schema
	if err != nil {
		log.Printf("[backfill] ❌ open %s: %v", path, err)
		return
	}
	w.Close()
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		log.Printf("[backfill] ❌ open %s: %v", path, err)
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	code, err := totp.GenerateCode(cfg.AngelTOTPSecret, time.Now())
	if err != nil {
		log.Printf("[backfill] ❌ TOTP: %v", err)
		return
	}
	sc := smartconnect.NewSmartConnect(smartconnect.Config{APIKey: cfg.AngelAPIKey})
	if _, err := sc.GenerateSession(cfg.AngelClientCode, cfg.AngelPassword, code); err != nil {
		log.Printf("[backfill] ❌ Angel login: %v", err)
		return
	}

	bcfg := backfill.Config{
		Days:  config.GetEnvInt("BACKFILL_DAYS", 30),
		TFs:   config.ParseTFString(config.GetEnv("ENABLED_TFS", "60,120,180,300,3600")),
		Delay: time.Second, // getCandleData allows 3/s; other services share the account
	}
	if _, err := backfill.Run(ctx, bcfg, sc, db); err != nil {
		log.Printf("[backfill] ❌ %v", err)
	}
}
