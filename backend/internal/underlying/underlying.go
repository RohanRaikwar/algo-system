// Package underlying describes each index the system trades options on:
// its index token, its option contracts' exchange and naming, and the
// fallbacks used when the instrument master cannot answer. NIFTY is the
// primary underlying; others are added with UNDERLYINGS (e.g.
// "NIFTY,SENSEX").
//
// Contract facts (lot size, strike step, expiry weekday) change over time
// and are read from the instrument master where possible. The values here
// are only fallbacks; each can be overridden with <KEY>_<FIELD> env vars.
//
// The package reads the environment directly and imports nothing internal,
// so config and every service can depend on it.
package underlying

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Profile is one tradable underlying.
type Profile struct {
	Key           string // "NIFTY", "SENSEX"
	IndexExchange string // "NSE", "BSE"
	IndexToken    string // "99926000", "99919000"
	IndexExchType int    // Angel WS exchange_type of the index: 1 NSE, 3 BSE

	// OptionName is the instrument master Name, the OptionGreek "name"
	// and the trading-symbol prefix of the options ("NIFTY", "SENSEX").
	OptionName  string
	FNOExchange string // "NFO", "BFO"
	FNOExchType int    // Angel WS exchange_type of the options: 2 NFO, 4 BFO

	StrikeStep    int64        // points between listed strikes (fallback)
	LotFallback   int          // lot size when the master has no entry
	FreezeQty     int          // max qty per order
	ExpiryWeekday time.Weekday // weekly expiry guess when the master has none

	StrategyName string // SR strategy name for this underlying

	// ScaleNum/ScaleDen convert a NIFTY-sized point parameter (in index
	// paise) to this underlying: paise*Num/Den. Integers only.
	ScaleNum, ScaleDen int64
}

// IndexKey is the "<EXCH>:<token>" key of the index, e.g. "BSE:99919000".
func (p Profile) IndexKey() string { return p.IndexExchange + ":" + p.IndexToken }

// IndexSubscribeToken is the index in Angel's "<type>:<token>" form.
func (p Profile) IndexSubscribeToken() string {
	return strconv.Itoa(p.IndexExchType) + ":" + p.IndexToken
}

// Scale converts a NIFTY-sized amount of index paise to this underlying.
func (p Profile) Scale(paise int64) int64 {
	if p.ScaleDen == 0 {
		return paise
	}
	return paise * p.ScaleNum / p.ScaleDen
}

// builtin holds the defaults. NIFTY's values match what the code used
// before profiles existed and must not change.
var builtin = map[string]Profile{
	"NIFTY": {
		Key: "NIFTY", IndexExchange: "NSE", IndexToken: "99926000", IndexExchType: 1,
		OptionName: "NIFTY", FNOExchange: "NFO", FNOExchType: 2,
		StrikeStep: 50, LotFallback: 65, FreezeQty: 1800, ExpiryWeekday: time.Tuesday,
		StrategyName: "NIFTY50_SR", ScaleNum: 1, ScaleDen: 1,
	},
	"SENSEX": {
		Key: "SENSEX", IndexExchange: "BSE", IndexToken: "99919000", IndexExchType: 3,
		OptionName: "SENSEX", FNOExchange: "BFO", FNOExchType: 4,
		StrikeStep: 100, LotFallback: 20, FreezeQty: 1000, ExpiryWeekday: time.Thursday,
		StrategyName: "SENSEX_SR", ScaleNum: 33, ScaleDen: 10,
	},
}

// All returns the configured underlyings, NIFTY first: every index listed
// in INDEX_TOKEN after the first ("NSE:99926000,BSE:99919000" adds SENSEX)
// plus the keys in UNDERLYINGS (older form, "NIFTY,SENSEX"). Unknown
// entries are skipped.
func All() []Profile {
	out := []Profile{Get("NIFTY")}
	seen := map[string]bool{"NIFTY": true}
	add := func(key string) {
		if _, ok := builtin[key]; ok && !seen[key] {
			seen[key] = true
			out = append(out, Get(key))
		}
	}
	entries := strings.Split(os.Getenv("INDEX_TOKEN"), ",")
	for i, e := range entries {
		if i == 0 {
			continue // the primary index
		}
		if key, ok := keyOfIndex(e); ok {
			add(key)
		}
	}
	for _, k := range strings.Split(os.Getenv("UNDERLYINGS"), ",") {
		add(strings.ToUpper(strings.TrimSpace(k)))
	}
	return out
}

// keyOfIndex finds the built-in profile of an "EXCH:TOKEN" entry (a bare
// token means NSE).
func keyOfIndex(entry string) (string, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", false
	}
	ex, tok, ok := strings.Cut(entry, ":")
	if !ok {
		ex, tok = "NSE", entry
	}
	ex, tok = strings.ToUpper(strings.TrimSpace(ex)), strings.TrimSpace(tok)
	for key, p := range builtin {
		if p.IndexExchange == ex && p.IndexToken == tok {
			return key, true
		}
	}
	return "", false
}

// Primary is the NIFTY profile.
func Primary() Profile { return Get("NIFTY") }

// Get returns the profile for key with env overrides applied. An unknown
// key returns NIFTY.
func Get(key string) Profile {
	p, ok := builtin[strings.ToUpper(key)]
	if !ok {
		p = builtin["NIFTY"]
	}
	k := p.Key
	if k == "NIFTY" {
		// Names that predate profiles keep working.
		p.LotFallback = envInt("NIFTY_LOT_SIZE", p.LotFallback)
		p.FreezeQty = envInt("STRAT_FREEZE_QTY", p.FreezeQty)
		if v := strings.TrimSpace(os.Getenv("STRAT_FNO_EXCHANGE")); v != "" {
			p.FNOExchange = v
		}
	}
	p.StrikeStep = int64(envInt(k+"_STRIKE_STEP", int(p.StrikeStep)))
	p.LotFallback = envInt(k+"_LOT_SIZE", p.LotFallback)
	p.FreezeQty = envInt(k+"_FREEZE_QTY", p.FreezeQty)
	if v := strings.TrimSpace(os.Getenv(k + "_EXPIRY_WEEKDAY")); v != "" {
		if wd, ok := parseWeekday(v); ok {
			p.ExpiryWeekday = wd
		}
	}
	if v := strings.TrimSpace(os.Getenv(k + "_SCALE")); v != "" {
		if num, den, ok := parseScale(v); ok {
			p.ScaleNum, p.ScaleDen = num, den
		}
	}
	return p
}

// ByStrategy finds the configured profile whose SR strategy is name.
func ByStrategy(name string) (Profile, bool) {
	for _, p := range All() {
		if p.StrategyName == name {
			return p, true
		}
	}
	return Profile{}, false
}

// ByIndexKey finds the configured profile of an "<EXCH>:<token>" key.
func ByIndexKey(key string) (Profile, bool) {
	for _, p := range All() {
		if strings.EqualFold(p.IndexKey(), key) {
			return p, true
		}
	}
	return Profile{}, false
}

// ByIndexToken finds the configured profile of a bare index token.
func ByIndexToken(token string) (Profile, bool) {
	for _, p := range All() {
		if p.IndexToken == token {
			return p, true
		}
	}
	return Profile{}, false
}

// ByFNOExchange finds the configured profile trading options on ex.
func ByFNOExchange(ex string) (Profile, bool) {
	for _, p := range All() {
		if strings.EqualFold(p.FNOExchange, ex) {
			return p, true
		}
	}
	return Profile{}, false
}

func envInt(key string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		return n
	}
	return fallback
}

func parseWeekday(s string) (time.Weekday, bool) {
	for d := time.Sunday; d <= time.Saturday; d++ {
		if strings.EqualFold(s, d.String()) || strings.EqualFold(s, d.String()[:3]) {
			return d, true
		}
	}
	return 0, false
}

// parseScale reads "33/10" or a whole number "3".
func parseScale(s string) (num, den int64, ok bool) {
	a, b, hasDen := strings.Cut(s, "/")
	num, err := strconv.ParseInt(strings.TrimSpace(a), 10, 64)
	if err != nil || num <= 0 {
		return 0, 0, false
	}
	den = 1
	if hasDen {
		if den, err = strconv.ParseInt(strings.TrimSpace(b), 10, 64); err != nil || den <= 0 {
			return 0, 0, false
		}
	}
	return num, den, true
}

// String is for logs.
func (p Profile) String() string {
	return fmt.Sprintf("%s(index %s, options %s on %s, step %d, lot %d, freeze %d, expiry %s, scale %d/%d)",
		p.Key, p.IndexKey(), p.OptionName, p.FNOExchange, p.StrikeStep, p.LotFallback, p.FreezeQty,
		p.ExpiryWeekday, p.ScaleNum, p.ScaleDen)
}
