package stratengine

import (
	"fmt"
	"os"
	"strconv"
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
	Qty int64 // default quantity per trade

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
	EODExitTime string // "HH:MM" IST: auto-exit all positions and stop entries (default 15:05)

	// ── Signal-Time Option Automation ──
	// ── Range-market strategies (NIFTY_RANGE_MARKET_GUIDE.md), paper only ──
	RangeEnabled    bool // NIFTY50_RANGE: S1 mean reversion + S2 breakout
	RangeICEnabled  bool // NIFTY50_RANGE_IC: iron condor (multi-leg paper path)
	RangeDeltaGuard bool // check the entry strike's delta via OptionGreek; refuse if out of band or unavailable
	// Checks that run with the delta guard (they need the option chain). 0 = off.
	RangeMinLiquidity int64   // min max(volume, OI) of any contract traded
	RangeMaxBuyIV     float64 // max IV % for a bought option
	RangeMinSellIV    float64 // min average IV % of the condor's short legs
	RangeCostMultiple int64   // expected option gain at target ≥ this × round-trip slippage

	// ── NIFTY50_SR: regime-aware support/resistance, paper only ──
	SREnabled       bool
	SRMaxDayLossPts int64   // stop entries after this many index paise lost today, 0 = off
	SRDeltaMin      float64 // bought strike |delta| band, from the live chain
	SRDeltaMax      float64
	SRMaxThetaPct   float64 // option picker's SR intent only: |theta| per day ≤ this % of premium, 0 = off
	// SR strike selection: theta over PickHoldMinutes ≤ this % of |delta| ×
	// target move, 0 = off. SRViewTargetMove (index paise) is the target
	// for the live strike view, which has no signal.
	SRThetaMaxGainPct float64
	SRViewTargetMove  int64
	SRMaxGamma        float64 // gamma cap within SRGammaDTE days of expiry, 0 = off
	SRGammaDTE        int
	SRMinDTE          int // buy the nearest expiry at least this many days out (2 = Monday skips Tuesday)

	// ── Global option picker (internal/optionpicker) ──
	PickerMode       string        // off | shadow (log decisions only) | on (picker chooses the contract)
	PickMaxSpreadPct float64       // (ask−bid)/mid × 100 ceiling
	PickMaxQuoteAge  time.Duration // bid/ask older than this is not tradable
	PickMaxChainAge  time.Duration // greeks snapshot older than this refuses selection
	PickRank         string        // delta (closest to band middle) | return (highest expected return on premium)
	PickHoldMinutes  float64       // expected holding time for the return rank's decay term
	// RANGE's picker intent has no theta/gamma cap by default (0 = off);
	// unlike SR, RANGE's strike rule already targets a delta band, so a
	// cap is opt-in via these envs.
	RangePickMaxThetaPct float64
	RangePickMaxGamma    float64

	PaperSlippageBps      int64 // paper fills cross the spread: LTP ± max(LTP×bps/10000, min)
	PaperSlippageMinPaise int64
	WarmupDays            int    // calendar days of 1m history replayed at startup
	SQLitePath            string // candle store for warmup

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
		EODExitTime:    config.GetEnv("STRAT_EOD_EXIT_TIME", "15:05"),
		// Range-market strategies
		RangeEnabled:          config.GetEnvBool("STRAT_RANGE_ENABLED", true),
		RangeICEnabled:        config.GetEnvBool("STRAT_RANGE_IC_ENABLED", true),
		RangeDeltaGuard:       config.GetEnvBool("STRAT_RANGE_DELTA_GUARD", true),
		RangeMinLiquidity:     config.GetEnvInt64("STRAT_RANGE_MIN_LIQUIDITY", 5000),
		RangeMaxBuyIV:         getEnvFloat("STRAT_RANGE_MAX_BUY_IV", 25),
		RangeMinSellIV:        getEnvFloat("STRAT_RANGE_MIN_SELL_IV", 11),
		RangeCostMultiple:     config.GetEnvInt64("STRAT_RANGE_COST_MULTIPLE", 3),
		SREnabled:             config.GetEnvBool("STRAT_SR_ENABLED", true),
		SRMaxDayLossPts:       config.GetEnvInt64("STRAT_SR_MAX_DAY_LOSS_PAISE", 6000),
		SRDeltaMin:            getEnvFloat("STRAT_SR_DELTA_MIN", 0.45),
		SRDeltaMax:            getEnvFloat("STRAT_SR_DELTA_MAX", 0.60),
		SRMaxThetaPct:         getEnvFloat("STRAT_SR_MAX_THETA_PCT", 8),
		SRThetaMaxGainPct:     getEnvFloat("STRAT_SR_THETA_MAX_GAIN_PCT", 25),
		SRViewTargetMove:      config.GetEnvInt64("STRAT_SR_VIEW_TARGET_PAISE", 3000),
		SRMaxGamma:            getEnvFloat("STRAT_SR_MAX_GAMMA", 0.005),
		SRGammaDTE:            config.GetEnvInt("STRAT_SR_GAMMA_DTE", 1),
		SRMinDTE:              config.GetEnvInt("STRAT_SR_MIN_DTE", 2),
		PickerMode:            config.GetEnv("STRAT_PICKER_MODE", "shadow"),
		PickMaxSpreadPct:      getEnvFloat("STRAT_PICK_MAX_SPREAD_PCT", 2),
		PickMaxQuoteAge:       getEnvDuration("STRAT_PICK_MAX_QUOTE_AGE", 3*time.Second),
		PickMaxChainAge:       getEnvDuration("STRAT_PICK_MAX_CHAIN_AGE", 2*time.Minute),
		PickRank:              config.GetEnv("STRAT_PICK_RANK", "delta"),
		PickHoldMinutes:       getEnvFloat("STRAT_PICK_HOLD_MIN", 60),
		RangePickMaxThetaPct:  getEnvFloat("STRAT_RANGE_PICK_MAX_THETA_PCT", 0),
		RangePickMaxGamma:     getEnvFloat("STRAT_RANGE_PICK_MAX_GAMMA", 0),
		PaperSlippageBps:      config.GetEnvInt64("STRAT_PAPER_SLIPPAGE_BPS", 50),
		PaperSlippageMinPaise: config.GetEnvInt64("STRAT_PAPER_SLIPPAGE_MIN_PAISE", 50),
		WarmupDays:            config.GetEnvInt("STRAT_RANGE_WARMUP_DAYS", 5),
		SQLitePath:            config.GetEnv("SQLITE_PATH", "data/candles.db"),
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
	switch c.PickerMode {
	case "", "off", "shadow", "on":
	default:
		return fmt.Errorf("STRAT_PICKER_MODE=%q: want off, shadow or on", c.PickerMode)
	}
	switch c.PickRank {
	case "", "delta", "return":
	default:
		return fmt.Errorf("STRAT_PICK_RANK=%q: want delta or return", c.PickRank)
	}
	if len(c.SubscribeTokenKeys) == 0 {
		return fmt.Errorf("STRAT_SUBSCRIBE_TOKENS is required")
	}
	return nil
}

// 15:05: after the strategies' own 15:00 exits, before NSE's closing
// auction session (15:00-15:30) gets going.
const defaultEODExitHour, defaultEODExitMin = 15, 5

// parseHHMM parses a 24-hour "HH:MM" time of day.
func parseHHMM(v string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return 0, 0, fmt.Errorf("want HH:MM, got %q", v)
	}
	return t.Hour(), t.Minute(), nil
}

func getEnvFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
