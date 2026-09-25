package indengine

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"trading-systemv1/config"
	"trading-systemv1/internal/indicator"
)

// Config holds all env-parsed configuration for the indicator engine service.
type Config struct {
	RedisAddr          string
	RedisPassword      string
	SQLitePath         string
	ConsumerGroup      string
	ConsumerName       string
	EnabledTFs         []int
	SnapshotIntervalS  int
	SubscribeTokenKeys []string // "exchange:token" keys
	SnapshotKey        string
	HTTPAddr           string
	PELIntervalS       int
	PELMinIdleMs       int64
	IndicatorConfigs   []indicator.TFIndicatorConfig
}

// LoadConfig reads all environment variables and returns a Config.
func LoadConfig() Config {
	enabledTFs := config.ParseTFString(config.GetEnv("ENABLED_TFS", "60,120,180,300,3600"))
	indConfigs := BuildIndicatorConfigs(enabledTFs)
	tokenKeys := config.ParseSubscribeTokenKeys(config.GetEnv("SUBSCRIBE_TOKENS", ""))

	return Config{
		RedisAddr:          config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:      config.GetEnv("REDIS_PASSWORD", ""),
		SQLitePath:         config.GetEnv("SQLITE_PATH", "data/candles.db"),
		ConsumerGroup:      config.GetEnv("CONSUMER_GROUP", "indengine"),
		ConsumerName:       config.GetEnv("CONSUMER_NAME", "worker-1"),
		EnabledTFs:         enabledTFs,
		SnapshotIntervalS:  config.GetEnvInt("SNAPSHOT_INTERVAL_SEC", 30),
		SubscribeTokenKeys: tokenKeys,
		SnapshotKey:        config.GetEnv("SNAPSHOT_KEY", "ind:snapshot:engine"),
		HTTPAddr:           config.GetEnv("INDENGINE_HTTP_ADDR", ":9095"),
		PELIntervalS:       config.GetEnvInt("PEL_RECLAIM_INTERVAL_SEC", 30),
		PELMinIdleMs:       config.GetEnvInt64("PEL_MIN_IDLE_MS", 60000),
		IndicatorConfigs:   indConfigs,
	}
}

// Validate checks configuration for logical consistency.
func (c Config) Validate() error {
	if len(c.EnabledTFs) == 0 {
		return fmt.Errorf("ENABLED_TFS must contain at least one timeframe")
	}
	if c.SnapshotIntervalS <= 0 {
		return fmt.Errorf("SNAPSHOT_INTERVAL_SEC must be > 0")
	}
	if c.PELIntervalS <= 0 {
		return fmt.Errorf("PEL_RECLAIM_INTERVAL_SEC must be > 0")
	}
	if c.ConsumerGroup == "" {
		return fmt.Errorf("CONSUMER_GROUP cannot be empty")
	}
	if c.ConsumerName == "" {
		return fmt.Errorf("CONSUMER_NAME cannot be empty")
	}
	return nil
}

// BuildIndicatorConfigs creates indicator configurations per TF from the
// INDICATOR_CONFIGS env var.  Format: "TYPE:PERIOD,TYPE:PERIOD,..."
// Example: "EMA:6,EMA:9,EMA:21"
// If the env var is empty, sensible defaults are used.
func BuildIndicatorConfigs(tfs []int) []indicator.TFIndicatorConfig {
	indSpecs := ParseIndicatorSpecs(config.GetEnv("INDICATOR_CONFIGS", ""))
	configs := make([]indicator.TFIndicatorConfig, len(tfs))
	for i, tf := range tfs {
		configs[i] = indicator.TFIndicatorConfig{
			TF:         tf,
			Indicators: indSpecs,
		}
	}
	return configs
}

// ParseIndicatorSpecs parses "TYPE:PERIOD,..." into []IndicatorConfig.
// Returns defaults if input is empty.
func ParseIndicatorSpecs(s string) []indicator.IndicatorConfig {
	if s == "" {
		return []indicator.IndicatorConfig{
			{Type: "EMA", Period: 6},
			{Type: "EMA", Period: 9},
			{Type: "EMA", Period: 21},
		}
	}

	var configs []indicator.IndicatorConfig
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		tokens := strings.SplitN(part, ":", 2)
		if len(tokens) != 2 {
			continue
		}
		typ := strings.ToUpper(strings.TrimSpace(tokens[0]))
		period, err := strconv.Atoi(strings.TrimSpace(tokens[1]))
		if err != nil || period <= 0 {
			log.Printf("[indengine] skipping invalid indicator spec: %q", part)
			continue
		}
		configs = append(configs, indicator.IndicatorConfig{Type: typ, Period: period})
	}
	if len(configs) == 0 {
		log.Println("[indengine] WARNING: no valid indicators parsed, using defaults")
		return ParseIndicatorSpecs("")
	}
	log.Printf("[indengine] loaded %d indicator specs from INDICATOR_CONFIGS", len(configs))
	return configs
}
