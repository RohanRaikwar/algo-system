package config

import (
	"os"
	"strconv"
	"strings"
)

// GetEnv returns the value of an environment variable, or fallback if unset/empty.
func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetEnvInt returns the integer value of an environment variable, or fallback on error.
func GetEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// GetEnvInt64 returns the int64 value of an environment variable, or fallback on error.
func GetEnvInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// GetEnvBool returns true if the environment variable is "true" (case-insensitive).
func GetEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		if fallback {
			return true
		}
		return false
	}
	return strings.EqualFold(v, "true")
}

// angelExchangeType maps exchange names to Angel One WS exchange_type
// codes. Inverse of exchangeTypeToName in internal/marketdata/ws/ingest.go;
// keep the two in sync.
var angelExchangeType = map[string]int{
	"NSE": 1, "NFO": 2, "BSE": 3, "BFO": 4, "MCX": 5, "NCX": 7, "CDE": 13,
}

// indexInstrument parses INDEX_TOKEN: "EXCH:TOKEN" (e.g. "BSE:99919000")
// or a bare token, which means NSE. Default NIFTY 50, "NSE:99926000". An
// unknown exchange falls back to NSE so the services still start.
func indexInstrument() (exchange, token string) {
	v := strings.TrimSpace(GetEnv("INDEX_TOKEN", "NSE:99926000"))
	exchange, token = "NSE", v
	if ex, tok, ok := strings.Cut(v, ":"); ok {
		ex = strings.ToUpper(strings.TrimSpace(ex))
		if _, known := angelExchangeType[ex]; known {
			exchange = ex
		}
		token = strings.TrimSpace(tok)
	}
	return exchange, token
}

// IndexToken is the instrument token of the traded index (INDEX_TOKEN,
// NIFTY 50 by default). It is the default for every per-service token
// list: SUBSCRIBE_TOKENS ("<type>:<token>"), the STRAT_/ANALYST_ subscribe
// lists and EXITWATCH_INDEX_KEYS ("<EXCH>:<token>"), and CLOSE_REF_TOKEN.
// Each of those still overrides it when set.
func IndexToken() string { _, t := indexInstrument(); return t }

// IndexExchange is the index's exchange name, e.g. "NSE" or "BSE".
func IndexExchange() string { e, _ := indexInstrument(); return e }

// IndexSubscribeTokens is the SUBSCRIBE_TOKENS default in Angel's
// "<exchange_type>:<token>" form, e.g. "1:99926000" or "3:99919000".
func IndexSubscribeTokens() string {
	e, t := indexInstrument()
	return strconv.Itoa(angelExchangeType[e]) + ":" + t
}

// IndexKey is the "<EXCH>:<token>" default for EXCH:TOKEN lists.
func IndexKey() string { e, t := indexInstrument(); return e + ":" + t }
