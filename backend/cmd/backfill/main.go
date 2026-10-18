// Command backfill fills missing 1m index candles (and the timeframes built
// from them) for every underlying from Angel One's historical API, into the
// candles database. docker-entrypoint.sh runs it before the services start,
// so a new index (SENSEX) begins with its past days and downtime gaps are
// closed. It also builds newly enabled TFs from stored 1m rows. It always
// exits 0: missing history must not stop trading.
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

	bcfg := backfill.Config{
		Days:  config.GetEnvInt("BACKFILL_DAYS", 30),
		TFs:   config.ParseTFString(config.GetEnv("ENABLED_TFS", "60,120,180,300,600,900,1800,3600")),
		Delay: time.Second, // getCandleData allows 3/s; other services share the account
	}
	fetch(ctx, bcfg, db)

	// TFs added to ENABLED_TFS get their past days from the stored 1m rows.
	if _, err := backfill.Rebuild(ctx, bcfg, db); err != nil {
		log.Printf("[backfill] ❌ rebuild: %v", err)
	}
}

// fetch fills missing 1m candles from Angel (production only).
func fetch(ctx context.Context, bcfg backfill.Config, db *sql.DB) {
	if strings.EqualFold(config.GetEnv("STAGING_MODE", "false"), "true") {
		log.Println("[backfill] staging: simulated feed, nothing to fetch")
		return
	}
	cfg := config.Load()
	if cfg.AngelAPIKey == "" || cfg.AngelClientCode == "" || cfg.AngelPassword == "" || cfg.AngelTOTPSecret == "" {
		log.Println("[backfill] no Angel credentials; fetch skipped")
		return
	}
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
	if _, err := backfill.Run(ctx, bcfg, sc, db); err != nil {
		log.Printf("[backfill] ❌ %v", err)
	}
}
