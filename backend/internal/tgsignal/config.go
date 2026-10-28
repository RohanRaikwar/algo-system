package tgsignal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trading-systemv1/config"
)

// Config holds env-parsed configuration for the tgsignal service.
type Config struct {
	RedisAddr     string
	RedisPassword string
	APIID         int    // TG_API_ID, from my.telegram.org
	APIHash       string // TG_API_HASH
	Phone         string // TG_PHONE, for tglogin only
	SessionPath   string // TG_SESSION_PATH, written by tglogin
	// SessionB64 seeds SessionPath on a fresh disk (production): the
	// session file as base64 (tglogin -export). An existing file wins, so
	// the volume's copy is reused across deploys.
	SessionB64 string
	// ChatIDs are the groups/channels whose calls are traded. Bot API
	// style ids (-1001234567890) and bare ids (1234567890) both work.
	ChatIDs []int64
	// MaxAge drops messages older than this, so calls recovered after a
	// restart or reconnect are never traded late.
	MaxAge time.Duration
	// JournalPath is the review log (SQLite): every message and verdict.
	JournalPath string

	// LLM reader (TG_LLM=on): any OpenAI-compatible chat API.
	LLM        bool
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string
	LLMReason  string // reasoning_effort for reasoning models, "" = none
	// LLM budget and concurrency (TG_LLM_TPM, TG_LLM_RPM,
	// TG_LLM_CONCURRENCY). 0 TPM/RPM = no budget.
	LLMTPM, LLMRPM, LLMWorkers int
	// LLMCacheTTL reuses the reading of an identical prompt (same text,
	// same context) this long; 0 = off.
	LLMCacheTTL time.Duration
	// LLMExtra are more LLM APIs or keys (TG_LLM_2_*, TG_LLM_3_*, …, and
	// the older TG_LLM_FALLBACK_*). Each has its own budget; the
	// scheduler sends a read to the first one with budget left, so they
	// add up. A missing base URL or model takes the primary's.
	LLMExtra []LLMEndpoint
	// DefaultSLPct gives a call without a stop ("Stoploss : PAID") a stop
	// this % below its entry. 0 = such calls are refused.
	DefaultSLPct int64
}

// LoadConfig reads all environment variables and returns a Config.
func LoadConfig() Config {
	var ids []int64
	for _, s := range strings.Split(config.GetEnv("TG_CHAT_IDS", ""), ",") {
		if id, ok := normalizeChatID(s); ok {
			ids = append(ids, id)
		}
	}
	maxAge, err := time.ParseDuration(config.GetEnv("TG_MAX_AGE", "60s"))
	if err != nil || maxAge <= 0 {
		maxAge = 60 * time.Second
	}
	return Config{
		RedisAddr:     config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: config.GetEnv("REDIS_PASSWORD", ""),
		APIID:         config.GetEnvInt("TG_API_ID", 0),
		APIHash:       config.GetEnv("TG_API_HASH", ""),
		Phone:         config.GetEnv("TG_PHONE", ""),
		SessionPath:   config.GetEnv("TG_SESSION_PATH", "data/tg.session"),
		SessionB64:    config.GetEnv("TG_SESSION_B64", ""),
		ChatIDs:       ids,
		MaxAge:        maxAge,
		JournalPath:   config.GetEnv("TG_JOURNAL_PATH", "data/tgsignal.db"),
		LLM:           strings.EqualFold(config.GetEnv("TG_LLM", "off"), "on"),
		LLMBaseURL:    config.GetEnv("TG_LLM_BASE_URL", "https://api.groq.com/openai/v1"),
		LLMAPIKey:     config.GetEnv("TG_LLM_API_KEY", ""),
		LLMModel:      config.GetEnv("TG_LLM_MODEL", ""),
		LLMReason:     config.GetEnv("TG_LLM_REASONING", ""),
		LLMTPM:        config.GetEnvInt("TG_LLM_TPM", 8000),
		LLMRPM:        config.GetEnvInt("TG_LLM_RPM", 30),
		LLMWorkers:    config.GetEnvInt("TG_LLM_CONCURRENCY", 4),
		LLMCacheTTL:   10 * time.Minute,

		LLMExtra:     loadLLMExtra(),
		DefaultSLPct: int64(config.GetEnvInt("TG_DEFAULT_SL_PCT", 0)),
	}
}

// Validate checks the service can connect and knows which chats to read.
func (c Config) Validate() error {
	switch {
	case c.APIID == 0 || c.APIHash == "":
		return fmt.Errorf("TG_API_ID and TG_API_HASH are required (my.telegram.org)")
	case c.SessionPath == "":
		return fmt.Errorf("TG_SESSION_PATH cannot be empty")
	case len(c.ChatIDs) == 0:
		return fmt.Errorf("TG_CHAT_IDS is empty (go run ./cmd/tools/tglogin -list shows ids)")
	case c.DefaultSLPct < 0 || c.DefaultSLPct >= 100:
		return fmt.Errorf("TG_DEFAULT_SL_PCT %d must be 0-99", c.DefaultSLPct)
	case c.LLM && (c.LLMAPIKey == "" || c.LLMModel == ""):
		return fmt.Errorf("TG_LLM=on needs TG_LLM_API_KEY and TG_LLM_MODEL")
	}
	for _, e := range c.LLMExtra {
		if e.APIKey == "" {
			return fmt.Errorf("%s_API_KEY is empty", e.Env)
		}
	}
	return nil
}

// LLMEndpoint is one extra LLM API or key.
type LLMEndpoint struct {
	Env     string // env prefix, e.g. TG_LLM_2
	BaseURL string // "" = TG_LLM_BASE_URL
	APIKey  string
	Model   string // "" = TG_LLM_MODEL
	TPM     int    // 0 = TG_LLM_TPM
	RPM     int    // 0 = TG_LLM_RPM
}

// llmExtraMax is the highest TG_LLM_<n>_ prefix read.
const llmExtraMax = 9

// loadLLMExtra reads TG_LLM_FALLBACK_* then TG_LLM_2_* … TG_LLM_9_*; an
// endpoint with neither key nor base URL is skipped.
func loadLLMExtra() []LLMEndpoint {
	prefixes := []string{"TG_LLM_FALLBACK"}
	for n := 2; n <= llmExtraMax; n++ {
		prefixes = append(prefixes, fmt.Sprintf("TG_LLM_%d", n))
	}
	var out []LLMEndpoint
	for _, p := range prefixes {
		e := LLMEndpoint{
			Env:     p,
			BaseURL: config.GetEnv(p+"_BASE_URL", ""),
			APIKey:  config.GetEnv(p+"_API_KEY", ""),
			Model:   config.GetEnv(p+"_MODEL", ""),
			TPM:     config.GetEnvInt(p+"_TPM", 0),
			RPM:     config.GetEnvInt(p+"_RPM", 0),
		}
		if e.APIKey != "" || e.BaseURL != "" {
			out = append(out, e)
		}
	}
	return out
}

// withDefaults fills an extra endpoint's blanks from the primary.
func (e LLMEndpoint) withDefaults(c Config) LLMEndpoint {
	if e.BaseURL == "" {
		e.BaseURL = c.LLMBaseURL
	}
	if e.Model == "" {
		e.Model = c.LLMModel
	}
	if e.TPM == 0 {
		e.TPM = c.LLMTPM
	}
	if e.RPM == 0 {
		e.RPM = c.LLMRPM
	}
	return e
}

// normalizeChatID turns a Bot API id ("-1001234567890", "-123") or a bare
// MTProto id into the bare id carried by message peers.
func normalizeChatID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "-100")
	s = strings.TrimPrefix(s, "-")
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}

// SeedSession writes the session from SessionB64 when no session file
// exists. It reports whether it wrote one.
func (c Config) SeedSession() (bool, error) {
	if c.SessionB64 == "" {
		return false, nil
	}
	if _, err := os.Stat(c.SessionPath); err == nil {
		return false, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.SessionB64))
	if err != nil {
		return false, fmt.Errorf("TG_SESSION_B64: %w", err)
	}
	if !json.Valid(raw) {
		return false, fmt.Errorf("TG_SESSION_B64 is not a session file (tglogin -export)")
	}
	if err := os.MkdirAll(filepath.Dir(c.SessionPath), 0o700); err != nil {
		return false, err
	}
	return true, os.WriteFile(c.SessionPath, raw, 0o600)
}
