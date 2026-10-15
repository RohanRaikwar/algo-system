package archiver

import (
	"fmt"
	"strings"
	"time"

	"trading-systemv1/config"
)

// Config holds env-parsed configuration for the archiver service.
type Config struct {
	Enabled bool

	MegaEmail    string
	MegaPassword string
	MegaRootDir  string // top-level MEGA folder; archives land in <root>/YYYY/MM/DD/

	TmpDir       string // export/gzip scratch, deleted after each upload
	ManifestPath string // SQLite record of what has been uploaded

	// RunAt is the IST wall-clock time ("HH:MM") of the daily run. It must
	// fall after the 15:30 close so mdengine's final candle flush is done.
	RunAt string

	CandlesPath    string   // SQLITE_PATH: candles_1s, candles_tf
	ExitwatchPath  string   // EXITWATCH_DB: decisions, features_1hz
	JournalPaths   []string // signal journals: archived per day, never pruned
	OrdersPath     string   // STRAT_ORDER_JOURNAL_PATH: archived per day, never pruned
	ExitPolicyPath string   // STRAT_EXITPOLICY_DB: archived per day, never pruned

	// Local retention in calendar days, counted back from the archived day.
	// Rows older than this are deleted only once their day is in MEGA.
	Keep1sDays       int // candles_1s: SUBSCRIBE_TOKENS (index) only; nothing reads it back
	KeepTFDays       int // candles_tf: stratengine warmup + mdengine restore read it
	KeepFeaturesDays int // exitwatch features_1hz

	// WarmupDays mirrors STRAT_RANGE_WARMUP_DAYS so Validate can refuse a
	// candles_tf window that would starve stratengine's startup warmup.
	WarmupDays int

	// Cold reads: chart history older than the local window is fetched
	// from MEGA day files into ColdCacheDir, capped at ColdCacheMB. A
	// cached day not read for ColdCacheTTL is deleted.
	ColdCacheDir string
	ColdCacheMB  int
	ColdCacheTTL time.Duration
	ColdMaxDays  int // how far back a chart page may walk
}

// LoadConfig reads all environment variables and returns a Config.
func LoadConfig() Config {
	var journals []string
	for _, kv := range [][2]string{
		{"STRAT_JOURNAL_PATH", "data/signals.db"},
		{"STRAT_IND_JOURNAL_PATH", "data/signals_ind.db"},
		{"EXITWATCH_JOURNAL_PATH", "data/exitwatch_signals.db"},
	} {
		if p := strings.TrimSpace(config.GetEnv(kv[0], kv[1])); p != "" {
			journals = append(journals, p)
		}
	}
	return Config{
		Enabled:          strings.EqualFold(config.GetEnv("ARCHIVE_ENABLED", "false"), "true"),
		MegaEmail:        config.GetEnv("MEGA_EMAIL", ""),
		MegaPassword:     config.GetEnv("MEGA_PASSWORD", ""),
		MegaRootDir:      config.GetEnv("MEGA_ROOT_DIR", "algo-archive"),
		TmpDir:           config.GetEnv("ARCHIVE_TMP_DIR", "data/archive_tmp"),
		ManifestPath:     config.GetEnv("ARCHIVE_MANIFEST_DB", "data/archive_manifest.db"),
		RunAt:            config.GetEnv("ARCHIVE_RUN_AT", "16:00"),
		CandlesPath:      config.GetEnv("SQLITE_PATH", "data/candles.db"),
		ExitwatchPath:    config.GetEnv("EXITWATCH_DB", "data/exitwatch.db"),
		JournalPaths:     journals,
		OrdersPath:       config.GetEnv("STRAT_ORDER_JOURNAL_PATH", "data/orders.db"),
		ExitPolicyPath:   config.GetEnv("STRAT_EXITPOLICY_DB", "data/exitpolicy.db"),
		Keep1sDays:       config.GetEnvInt("ARCHIVE_KEEP_1S_DAYS", 10),
		KeepTFDays:       config.GetEnvInt("ARCHIVE_KEEP_TF_DAYS", 10),
		KeepFeaturesDays: config.GetEnvInt("ARCHIVE_KEEP_FEATURES_DAYS", 10),
		WarmupDays:       config.GetEnvInt("STRAT_RANGE_WARMUP_DAYS", 5),
		ColdCacheDir:     config.GetEnv("ARCHIVE_COLD_CACHE_DIR", "data/archive_cache"),
		ColdCacheMB:      config.GetEnvInt("ARCHIVE_COLD_CACHE_MB", 300),
		ColdCacheTTL:     time.Duration(config.GetEnvInt("ARCHIVE_COLD_CACHE_TTL_MIN", 10)) * time.Minute,
		ColdMaxDays:      config.GetEnvInt("ARCHIVE_COLD_MAX_DAYS", 365),
	}
}

// Validate checks configuration for logical consistency. A disabled
// archiver accepts any config: it never touches data.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.MegaEmail == "" || c.MegaPassword == "" {
		return fmt.Errorf("ARCHIVE_ENABLED=true requires MEGA_EMAIL and MEGA_PASSWORD")
	}
	if strings.Trim(c.MegaRootDir, "/") == "" {
		return fmt.Errorf("MEGA_ROOT_DIR cannot be empty")
	}
	if c.TmpDir == "" || c.ManifestPath == "" {
		return fmt.Errorf("ARCHIVE_TMP_DIR and ARCHIVE_MANIFEST_DB cannot be empty")
	}
	h, m, err := c.runAt()
	if err != nil {
		return err
	}
	if h*60+m < 15*60+30 {
		return fmt.Errorf("ARCHIVE_RUN_AT %q must be after the 15:30 close", c.RunAt)
	}
	if c.Keep1sDays < 0 || c.KeepFeaturesDays < 0 {
		return fmt.Errorf("ARCHIVE_KEEP_* days cannot be negative")
	}
	if c.KeepTFDays <= c.WarmupDays {
		return fmt.Errorf("ARCHIVE_KEEP_TF_DAYS=%d must exceed STRAT_RANGE_WARMUP_DAYS=%d", c.KeepTFDays, c.WarmupDays)
	}
	return nil
}

// runAt parses RunAt as HH:MM.
func (c Config) runAt() (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(c.RunAt))
	if err != nil {
		return 0, 0, fmt.Errorf("ARCHIVE_RUN_AT %q must be HH:MM", c.RunAt)
	}
	return t.Hour(), t.Minute(), nil
}
