package mdengine

import (
	"fmt"
	"log"
	"os"
	"strings"

	"trading-systemv1/config"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

// Config holds all configuration for the market data engine service.
type Config struct {
	// ── Mode ──
	StagingMode bool

	// ── Infrastructure ──
	RedisAddr     string
	RedisPassword string
	SQLitePath    string
	MetricsAddr   string

	// ── Subscription ──
	TokenList    []smartconnect.TokenListEntry // parsed production tokens
	EnabledTFs   []int                         // timeframe durations in seconds
	CandleTokens map[string]bool               // when non-empty, only these tokens get candle aggregation

	// CloseRefToken is the one instrument whose price the smart market-close
	// detector watches (default NIFTY 50 index, 99926000).
	CloseRefToken string

	// ── Angel One (production only) ──
	AngelAPIKey     string
	AngelClientCode string
	AngelPassword   string
	AngelTOTPSecret string

	// ── Staging ──
	SimWSURL string
}

// LoadConfig reads configuration from environment variables.
// In staging mode, Angel One credentials are not required.
func LoadConfig() Config {
	stagingMode := strings.EqualFold(os.Getenv("STAGING_MODE"), "true")

	c := Config{
		StagingMode:   stagingMode,
		RedisAddr:     config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: config.GetEnv("REDIS_PASSWORD", ""),
		SQLitePath:    config.GetEnv("SQLITE_PATH", "data/candles.db"),
		MetricsAddr:   config.GetEnv("METRICS_ADDR", ":9090"),
		SimWSURL:      config.GetEnv("SIM_WS_URL", "ws://localhost:9001/ws"),
		CandleTokens:  parseCandleTokens(config.GetEnv("CANDLE_TOKENS", "")),
		CloseRefToken: config.GetEnv("CLOSE_REF_TOKEN", "99926000"),
	}

	if stagingMode {
		log.Println("[mdengine] *** STAGING MODE — using tickserver WS instead of Angel One ***")
		c.EnabledTFs = config.ParseTFString(config.GetEnv("ENABLED_TFS", "60,120,180,300,3600"))
	} else {
		// Load production config (requires Angel One env vars)
		cfg := config.Load()
		if err := cfg.Validate(); err != nil {
			log.Fatalf("[mdengine] %v", err)
		}
		c.AngelAPIKey = cfg.AngelAPIKey
		c.AngelClientCode = cfg.AngelClientCode
		c.AngelPassword = cfg.AngelPassword
		c.AngelTOTPSecret = cfg.AngelTOTPSecret
		c.RedisAddr = cfg.RedisAddr
		c.RedisPassword = cfg.RedisPassword
		c.SQLitePath = cfg.SQLitePath
		c.MetricsAddr = cfg.MetricsAddr
		c.EnabledTFs = cfg.ParseTFs()
		c.TokenList = parseTokenList(cfg.SubscribeTokens)
		log.Printf("[mdengine] subscribing to %d token groups", len(c.TokenList))
	}

	log.Printf("[mdengine] enabled TFs: %v seconds", c.EnabledTFs)
	return c
}

// Validate checks the config for logical consistency.
func (c *Config) Validate() error {
	if !c.StagingMode {
		if c.AngelAPIKey == "" || c.AngelClientCode == "" {
			return fmt.Errorf("production mode requires Angel One credentials")
		}
	}
	if len(c.EnabledTFs) == 0 {
		return fmt.Errorf("at least one timeframe must be enabled")
	}
	return nil
}
