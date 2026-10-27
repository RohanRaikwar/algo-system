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
		DefaultSLPct:  int64(config.GetEnvInt("TG_DEFAULT_SL_PCT", 0)),
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
	return nil
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
