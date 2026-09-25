package config

import (
	"fmt"
	"log"
	"strconv"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Angel One credentials
	AngelAPIKey     string
	AngelClientCode string
	AngelPassword   string
	AngelTOTPSecret string

	// Infrastructure
	RedisAddr     string
	RedisPassword string
	SQLitePath    string
	MetricsAddr   string

	// Subscription
	SubscribeTokens string

	// Dynamic Timeframes (comma-separated seconds, e.g. "60,300,900")
	EnabledTFs string
}

// Load reads configuration from environment variables with sensible defaults.
// Angel One credentials are loaded but NOT validated here — call Validate() to check.
func Load() *Config {
	return &Config{
		AngelAPIKey:     GetEnv("ANGEL_API_KEY", ""),
		AngelClientCode: GetEnv("ANGEL_CLIENT_CODE", ""),
		AngelPassword:   GetEnv("ANGEL_PASSWORD", ""),
		AngelTOTPSecret: GetEnv("ANGEL_TOTP_SECRET", ""),

		RedisAddr:     GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: GetEnv("REDIS_PASSWORD", ""),
		SQLitePath:    GetEnv("SQLITE_PATH", "data/candles.db"),
		MetricsAddr:   GetEnv("METRICS_ADDR", ":9090"),

		// Default: NIFTY 50 on NSE_CM
		SubscribeTokens: GetEnv("SUBSCRIBE_TOKENS", "1:99926000"),

		// Default TFs: 1m, 5m, 15m
		EnabledTFs: GetEnv("ENABLED_TFS", "60,120,180,300,3600"),
	}
}

// Validate checks that all required fields are set.
// Returns an error listing every missing field, or nil if all are present.
func (c *Config) Validate() error {
	var missing []string
	if c.AngelAPIKey == "" {
		missing = append(missing, "ANGEL_API_KEY")
	}
	if c.AngelClientCode == "" {
		missing = append(missing, "ANGEL_CLIENT_CODE")
	}
	if c.AngelPassword == "" {
		missing = append(missing, "ANGEL_PASSWORD")
	}
	if c.AngelTOTPSecret == "" {
		missing = append(missing, "ANGEL_TOTP_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ParseTFs parses the EnabledTFs string into a slice of timeframe durations in seconds.
func (c *Config) ParseTFs() []int {
	return ParseTFString(c.EnabledTFs)
}

// ParseTFString parses a comma-separated string of TF durations in seconds.
func ParseTFString(s string) []int {
	parts := strings.Split(s, ",")
	tfs := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			log.Printf("[config] skipping invalid TF value: %q", p)
			continue
		}
		tfs = append(tfs, n)
	}
	return tfs
}
