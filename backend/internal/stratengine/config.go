package stratengine

import (
	"fmt"
	"strings"
	"time"

	"trading-systemv1/config"
)

// Config holds configuration for the strategy engine service.
type Config struct {
	// Redis connection
	RedisAddr     string
	RedisPassword string

	// Consumer group settings for Redis Streams
	ConsumerGroup string
	ConsumerName  string

	// Tokens to subscribe to (format: "exchange:token,exchange:token,...")
	SubscribeTokenKeys []string

	// Snapshot persistence
	SnapshotKey       string // Redis key for strategy snapshot
	SnapshotIntervalS int    // seconds between snapshots

	// Signal journal
	JournalPath string // SQLite path for signal journal

	// Strategy parameters
	ActiveStrategy string // e.g. "nifty50_10pts"
	Qty            int64  // default quantity per trade

	// Operational
	NotifyWebhook string // webhook URL for signal notifications
	KillSwitch    bool   // global kill-switch: block new entries, allow exits
	MetricsAddr   string // Prometheus /metrics listen address ("" disables)

	// PEL reclamation
	PELIntervalS int
	PELMinIdleMs int64

	// ── FNO Order Execution ──
	LiveOrders     bool   // true = place real orders, false = dry-run (log only)
	CallFNOToken   string // Angel One symbol token for CALL option (empty = dynamic strikes only)
	CallFNOSymbol  string // trading symbol (e.g. "NIFTY05MAR2523000CE")
	PutFNOToken    string // Angel One symbol token for PUT option  (empty = dynamic strikes only)
	PutFNOSymbol   string // trading symbol (e.g. "NIFTY05MAR2523000PE")
	FNOExchange    string // exchange ("NFO")
	FNOOrderType   string // "MARKET" or "LIMIT"
	FNOProductType string // "CARRYFORWARD" or "INTRADAY"

	// ── Angel One SmartAPI ──
	AngelAPIKey   string
	AngelClientID string
	AngelPassword string
	AngelTOTP     string

	// ── Dynamic Strike Selection ──
	DynamicStrikes bool // true = auto-resolve ATM CE/PE at market open

	// ── End of day ──
	EODExitTime string // "HH:MM" IST: auto-exit all positions and stop entries (default 15:20)

	// ── Signal-Time Option Automation ──
	OptionAutomation bool   // true = choose expiry/strike per BUY signal using greeks
	OptionHoldType   string // intraday / carry
	ExpectedIVMove   string // rise / neutral / fall
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() Config {
	cfg := Config{
		RedisAddr:         config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     config.GetEnv("REDIS_PASSWORD", ""),
		ConsumerGroup:     config.GetEnv("STRAT_CONSUMER_GROUP", "stratengine"),
		ConsumerName:      config.GetEnv("STRAT_CONSUMER_NAME", "strat-1"),
		SnapshotKey:       config.GetEnv("STRAT_SNAPSHOT_KEY", "snapshot:stratengine"),
		SnapshotIntervalS: config.GetEnvInt("STRAT_SNAPSHOT_INTERVAL_S", 30),
		JournalPath:       config.GetEnv("STRAT_JOURNAL_PATH", "data/signals.db"),
		ActiveStrategy:    config.GetEnv("STRAT_ACTIVE_STRATEGY", "nifty50_10pts"),
		Qty:               int64(config.GetEnvInt("STRAT_QTY", 1)),
		NotifyWebhook:     config.GetEnv("STRAT_NOTIFY_WEBHOOK", ""),
		KillSwitch:        config.GetEnvBool("STRAT_KILL_SWITCH", false),
		MetricsAddr:       config.GetEnv("STRAT_METRICS_ADDR", ":9096"),
		PELIntervalS:      config.GetEnvInt("STRAT_PEL_INTERVAL_S", 60),
		PELMinIdleMs:      config.GetEnvInt64("STRAT_PEL_MIN_IDLE_MS", 30000),

		// FNO
		LiveOrders:     config.GetEnvBool("STRAT_LIVE_ORDERS", false),
		CallFNOToken:   config.GetEnv("STRAT_CALL_FNO_TOKEN", ""),
		CallFNOSymbol:  config.GetEnv("STRAT_CALL_FNO_SYMBOL", ""),
		PutFNOToken:    config.GetEnv("STRAT_PUT_FNO_TOKEN", ""),
		PutFNOSymbol:   config.GetEnv("STRAT_PUT_FNO_SYMBOL", ""),
		FNOExchange:    config.GetEnv("STRAT_FNO_EXCHANGE", "NFO"),
		FNOOrderType:   config.GetEnv("STRAT_FNO_ORDER_TYPE", "MARKET"),
		FNOProductType: config.GetEnv("STRAT_FNO_PRODUCT_TYPE", "CARRYFORWARD"),

		// Angel One
		AngelAPIKey:   config.GetEnv("ANGEL_API_KEY", ""),
		AngelClientID: config.GetEnv("ANGEL_CLIENT_CODE", ""),
		AngelPassword: config.GetEnv("ANGEL_PASSWORD", ""),
		AngelTOTP:     config.GetEnv("ANGEL_TOTP_SECRET", ""),

		// Dynamic strikes
		DynamicStrikes: config.GetEnvBool("STRAT_DYNAMIC_STRIKES", false),
		EODExitTime:    config.GetEnv("STRAT_EOD_EXIT_TIME", "15:20"),
		// Signal-time option automation
		OptionAutomation: config.GetEnvBool("STRAT_OPTION_AUTOMATION", false),
		OptionHoldType:   config.GetEnv("STRAT_OPTION_HOLD_TYPE", "intraday"),
		ExpectedIVMove:   config.GetEnv("STRAT_EXPECTED_IV_MOVE", "neutral"),
	}

	// Parse token keys
	if tokens := config.GetEnv("STRAT_SUBSCRIBE_TOKENS", ""); tokens != "" {
		for _, t := range strings.Split(tokens, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				cfg.SubscribeTokenKeys = append(cfg.SubscribeTokenKeys, t)
			}
		}
	}

	return cfg
}

// Validate checks the config for logical consistency.
func (c *Config) Validate() error {
	if c.LiveOrders {
		if c.AngelAPIKey == "" || c.AngelClientID == "" || c.AngelPassword == "" || c.AngelTOTP == "" {
			return fmt.Errorf("STRAT_LIVE_ORDERS=true requires all Angel One credentials (ANGEL_API_KEY, ANGEL_CLIENT_CODE, ANGEL_PASSWORD, ANGEL_TOTP_SECRET)")
		}
	}
	// DynamicStrikes works in both live and paper-trading modes.
	// In paper mode (LiveOrders=false), a standalone SmartConnect session
	// is created using Angel credentials for ATM strike resolution.
	if c.DynamicStrikes {
		if c.AngelAPIKey == "" || c.AngelClientID == "" || c.AngelPassword == "" || c.AngelTOTP == "" {
			return fmt.Errorf("STRAT_DYNAMIC_STRIKES=true requires Angel One credentials (ANGEL_API_KEY, ANGEL_CLIENT_CODE, ANGEL_PASSWORD, ANGEL_TOTP_SECRET)")
		}
	}
	if !c.DynamicStrikes && (c.CallFNOToken == "" || c.PutFNOToken == "") {
		return fmt.Errorf("STRAT_DYNAMIC_STRIKES=false requires STRAT_CALL_FNO_TOKEN and STRAT_PUT_FNO_TOKEN")
	}
	if c.OptionAutomation && !c.DynamicStrikes {
		return fmt.Errorf("STRAT_OPTION_AUTOMATION=true requires STRAT_DYNAMIC_STRIKES=true")
	}
	if c.EODExitTime != "" {
		h, m, err := parseHHMM(c.EODExitTime)
		if err != nil {
			return fmt.Errorf("STRAT_EOD_EXIT_TIME: %w", err)
		}
		if h*60+m >= eodMarketCloseHour*60+eodMarketCloseMin {
			return fmt.Errorf("STRAT_EOD_EXIT_TIME=%s must be before the 15:30 market close", c.EODExitTime)
		}
	}
	if len(c.SubscribeTokenKeys) == 0 {
		return fmt.Errorf("STRAT_SUBSCRIBE_TOKENS is required")
	}
	return nil
}

const defaultEODExitHour, defaultEODExitMin = 15, 20

// parseHHMM parses a 24-hour "HH:MM" time of day.
func parseHHMM(v string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return 0, 0, fmt.Errorf("want HH:MM, got %q", v)
	}
	return t.Hour(), t.Minute(), nil
}
