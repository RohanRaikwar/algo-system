package exitwatch

import (
	"fmt"
	"strings"

	"trading-systemv1/config"
)

// Config holds env-parsed configuration for the exitwatch service.
type Config struct {
	RedisAddr     string
	RedisPassword string
	HTTPAddr      string
	DBPath        string   // SQLite file for decisions + 1Hz features
	JournalPath   string   // signal journal (same schema as stratengine's) for the LOG tab
	ParamsPath    string   // optional JSON tuning file; defaults when missing
	IndexKeys     []string // "EXCH:TOKEN" of indexes whose 1m ATR is tracked
	// AutoExit: strategies whose EXIT decision closes the position (paper
	// strategies only; NIFTY50_FNO is always refused).
	AutoExit []string
}

// LoadConfig reads all environment variables and returns a Config.
func LoadConfig() Config {
	var keys []string
	for _, k := range strings.Split(config.GetEnv("EXITWATCH_INDEX_KEYS", config.IndexKey()), ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	var auto []string
	for _, s := range strings.Split(config.GetEnv("EXITWATCH_AUTO_EXIT", ""), ",") {
		if s = strings.TrimSpace(s); s != "" {
			auto = append(auto, s)
		}
	}
	return Config{
		RedisAddr:     config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: config.GetEnv("REDIS_PASSWORD", ""),
		HTTPAddr:      config.GetEnv("EXITWATCH_HTTP_ADDR", ":9099"),
		DBPath:        config.GetEnv("EXITWATCH_DB", "data/exitwatch.db"),
		ParamsPath:    config.GetEnv("EXITWATCH_PARAMS", "config/exitwatch.json"),
		JournalPath:   config.GetEnv("EXITWATCH_JOURNAL_PATH", "data/exitwatch_signals.db"),
		IndexKeys:     keys,
		AutoExit:      auto,
	}
}

// Validate checks configuration for logical consistency.
func (c Config) Validate() error {
	if c.DBPath == "" {
		return fmt.Errorf("EXITWATCH_DB cannot be empty")
	}
	if c.JournalPath == "" {
		return fmt.Errorf("EXITWATCH_JOURNAL_PATH cannot be empty")
	}
	for _, k := range c.IndexKeys {
		if !strings.Contains(k, ":") {
			return fmt.Errorf("EXITWATCH_INDEX_KEYS entry %q must be EXCH:TOKEN", k)
		}
	}
	return nil
}
