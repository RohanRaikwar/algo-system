package analyst

import (
	"fmt"
	"strconv"
	"strings"

	"trading-systemv1/config"
)

// Config holds all env-parsed configuration for the market analyst service.
type Config struct {
	RedisAddr          string
	RedisPassword      string
	ConsumerGroup      string
	ConsumerName       string
	EnabledTFs         []int
	SubscribeTokenKeys []string // "exchange:token" keys
	HTTPAddr           string
}

// LoadConfig reads all environment variables and returns a Config.
func LoadConfig() Config {
	enabledTFs := parseTFs(config.GetEnv("ENABLED_TFS", "60"))
	tokenKeys := parseTokenKeys(config.GetEnv("ANALYST_SUBSCRIBE_TOKENS", "NSE:99926000"))

	return Config{
		RedisAddr:          config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:      config.GetEnv("REDIS_PASSWORD", ""),
		ConsumerGroup:      config.GetEnv("CONSUMER_GROUP", "analyst"),
		ConsumerName:       config.GetEnv("CONSUMER_NAME", "worker-1"),
		EnabledTFs:         enabledTFs,
		SubscribeTokenKeys: tokenKeys,
		HTTPAddr:           config.GetEnv("ANALYST_HTTP_ADDR", ":9098"),
	}
}

// Validate checks configuration for logical consistency.
func (c Config) Validate() error {
	if len(c.EnabledTFs) == 0 {
		return fmt.Errorf("ENABLED_TFS must contain at least one timeframe")
	}
	if c.ConsumerGroup == "" {
		return fmt.Errorf("CONSUMER_GROUP cannot be empty")
	}
	if c.ConsumerName == "" {
		return fmt.Errorf("CONSUMER_NAME cannot be empty")
	}
	return nil
}

// parseTFs parses a comma-separated TF string like "60,300" into []int.
func parseTFs(s string) []int {
	var tfs []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tf, err := strconv.Atoi(part)
		if err == nil && tf > 0 {
			tfs = append(tfs, tf)
		}
	}
	return tfs
}

// parseTokenKeys parses "exchange:token,exchange:token" into []string.
func parseTokenKeys(s string) []string {
	if s == "" {
		return nil
	}
	var keys []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			keys = append(keys, part)
		}
	}
	return keys
}
