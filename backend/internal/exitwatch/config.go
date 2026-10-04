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
	ParamsPath    string   // optional JSON tuning file; defaults when missing
	IndexKeys     []string // "EXCH:TOKEN" of indexes whose 1m ATR is tracked
}

// LoadConfig reads all environment variables and returns a Config.
func LoadConfig() Config {
	var keys []string
	for _, k := range strings.Split(config.GetEnv("EXITWATCH_INDEX_KEYS", "NSE:99926000"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	return Config{
		RedisAddr:     config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: config.GetEnv("REDIS_PASSWORD", ""),
		HTTPAddr:      config.GetEnv("EXITWATCH_HTTP_ADDR", ":9099"),
		DBPath:        config.GetEnv("EXITWATCH_DB", "data/exitwatch.db"),
		ParamsPath:    config.GetEnv("EXITWATCH_PARAMS", "config/exitwatch.json"),
		IndexKeys:     keys,
	}
}

// Validate checks configuration for logical consistency.
func (c Config) Validate() error {
	if c.DBPath == "" {
		return fmt.Errorf("EXITWATCH_DB cannot be empty")
	}
	for _, k := range c.IndexKeys {
		if !strings.Contains(k, ":") {
			return fmt.Errorf("EXITWATCH_INDEX_KEYS entry %q must be EXCH:TOKEN", k)
		}
	}
	return nil
}
