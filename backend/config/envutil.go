package config

import (
	"os"
	"strconv"
	"strings"

	"trading-systemv1/internal/underlying"
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

// IndexToken is the instrument token of the primary index (INDEX_TOKEN,
// NIFTY 50 by default). It drives the market-close detector and warmup;
// with UNDERLYINGS it and the extra indexes make up every service's
// subscribe list (IndexSubscribeTokens, IndexKeys).
func IndexToken() string { _, t := indexInstrument(); return t }

// IndexExchange is the index's exchange name, e.g. "NSE" or "BSE".
func IndexExchange() string { e, _ := indexInstrument(); return e }

// IndexSubscribeTokens is what mdengine subscribes, in Angel's
// "<exchange_type>:<token>" form, e.g. "1:99926000" or "3:99919000". It
// lists INDEX_TOKEN first, then every extra underlying in UNDERLYINGS.
func IndexSubscribeTokens() string {
	e, t := indexInstrument()
	out := []string{strconv.Itoa(angelExchangeType[e]) + ":" + t}
	for _, p := range extraUnderlyings() {
		out = append(out, p.IndexSubscribeToken())
	}
	return strings.Join(out, ",")
}

// IndexKey is the "<EXCH>:<token>" key of INDEX_TOKEN, the primary index.
func IndexKey() string { e, t := indexInstrument(); return e + ":" + t }

// IndexKeys is the "<EXCH>:<token>" list default: INDEX_TOKEN, then every
// extra underlying in UNDERLYINGS.
func IndexKeys() string {
	out := []string{IndexKey()}
	for _, p := range extraUnderlyings() {
		out = append(out, p.IndexKey())
	}
	return strings.Join(out, ",")
}

// IndexKeysEnv is the "<EXCH>:<token>" list in env var name (default
// IndexKeys) with every extra underlying's index key added when missing.
func IndexKeysEnv(name string) string {
	list := GetEnv(name, IndexKeys())
	for _, p := range extraUnderlyings() {
		list = appendMissing(list, p.IndexKey())
	}
	return list
}

// ExtraIndexTokens are the bare index tokens of the extra underlyings.
func ExtraIndexTokens() []string {
	var out []string
	for _, p := range extraUnderlyings() {
		out = append(out, p.IndexToken)
	}
	return out
}

// appendMissing adds item to a comma list unless already in it.
func appendMissing(list, item string) string {
	for _, v := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(v), item) {
			return list
		}
	}
	if strings.TrimSpace(list) == "" {
		return item
	}
	return list + "," + item
}

// extraUnderlyings are the underlyings UNDERLYINGS adds beyond NIFTY (the
// primary, which INDEX_TOKEN names), minus any equal to INDEX_TOKEN.
func extraUnderlyings() []underlying.Profile {
	primary := IndexKey()
	var out []underlying.Profile
	for _, p := range underlying.All()[1:] {
		if !strings.EqualFold(p.IndexKey(), primary) {
			out = append(out, p)
		}
	}
	return out
}
