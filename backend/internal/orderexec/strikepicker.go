// Package orderexec — strikepicker.go
//
// StrikePicker dynamically resolves ATM NIFTY CE/PE strike tokens
// at market open using the Angel One SearchScrip API. This removes
// the need to manually update F&O token/symbol env vars each day.
package orderexec

import (
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"trading-systemv1/internal/underlying"
	"trading-systemv1/pkg/smartconnect"
)

var istZone = time.FixedZone("IST", 5*3600+30*60)

// getEnvInt reads an integer from environment variable with a default fallback
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return defaultVal
}


// StrikeInfo holds the resolved token and symbol for a single strike.
type StrikeInfo struct {
	Token   string // Angel One symbol token (e.g. "57710")
	Symbol  string // trading symbol (e.g. "NIFTY17MAR2623200CE")
	Strike  int64  // strike price (e.g. 23200)
	LotSize int64  // lot size (e.g. 75 for NIFTY)
}

// StrikePicker resolves ATM CE/PE strikes for one underlying's options
// (NIFTY unless built with NewStrikePickerFor).
type StrikePicker struct {
	sc         *smartconnect.SmartConnect
	profile    underlying.Profile
	exchange   string // options segment: "NFO", "BFO"
	strikeStep int64  // 50 for NIFTY, 100 for SENSEX

	mu        sync.RWMutex
	currentCE StrikeInfo
	currentPE StrikeInfo
	resolved  bool
	spotPrice int64
	resolveTS time.Time

	// Multi-leg contracts (strikepicker_legs.go).
	legCache map[string]StrikeInfo
	expiry   time.Time
	lookup   func(symbol string) (token string, lotSize int64, err error) // test hook
	// listedExpiries returns real NIFTY option expiries on or after a day
	// (instrument master); nil = InstrumentMaster().OptionExpiries. Test hook.
	listedExpiries func(from time.Time) []time.Time
	// master is the instrument master; nil = GetInstrumentMaster(). Test hook.
	master *InstrumentMaster
}

func (sp *StrikePicker) im() *InstrumentMaster {
	if sp.master != nil {
		return sp.master
	}
	return GetInstrumentMaster()
}

// NewStrikePicker creates a NIFTY picker that uses the given SmartConnect
// client.
func NewStrikePicker(sc *smartconnect.SmartConnect) *StrikePicker {
	p := underlying.Primary()
	p.FNOExchange = "NFO" // the NIFTY picker always searched NFO
	return NewStrikePickerFor(sc, p)
}

// NewStrikePickerFor creates a picker for underlying p's options.
func NewStrikePickerFor(sc *smartconnect.SmartConnect, p underlying.Profile) *StrikePicker {
	return &StrikePicker{
		sc:         sc,
		profile:    p,
		exchange:   p.FNOExchange,
		strikeStep: p.StrikeStep,
	}
}

// Profile returns the underlying this picker resolves options for.
func (sp *StrikePicker) Profile() underlying.Profile { return sp.prof() }

// prof is the picker's underlying; a zero-value picker is NIFTY.
func (sp *StrikePicker) prof() underlying.Profile {
	if sp.profile.Key == "" {
		return underlying.Primary()
	}
	return sp.profile
}

// lotFallback is the lot size used when the master has no entry. It is
// read at call time so NIFTY_LOT_SIZE changes apply as before.
func (sp *StrikePicker) lotFallback() int64 {
	return int64(underlying.Get(sp.prof().Key).LotFallback)
}

// optionSymbol returns the trading symbol of one contract. The instrument
// master is asked first, since symbol formats differ by exchange (BFO
// weeklies read "SENSEX26N0564600CE"). Without a master entry NIFTY's
// symbol is built ("NIFTY03NOV2621250PE"); other underlyings return "".
func (sp *StrikePicker) optionSymbol(expiry time.Time, strike int64, optionType string) string {
	p := sp.prof()
	if inst := sp.im().FindOption(p.OptionName, sp.exchangeSeg(), expiry, strike, optionType); inst != nil {
		return inst.Symbol
	}
	if p.Key != "NIFTY" {
		return ""
	}
	return fmt.Sprintf("NIFTY%s%d%s", strings.ToUpper(expiry.In(istZone).Format("02Jan06")), strike, optionType)
}

// exchangeSeg is the options segment, NFO for a zero-value picker.
func (sp *StrikePicker) exchangeSeg() string {
	if sp.exchange == "" {
		return "NFO"
	}
	return sp.exchange
}

// Resolved returns true if ATM strikes have been resolved for the current day.
func (sp *StrikePicker) Resolved() bool {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.resolved
}

// GetCallToken returns the current CALL strike info.
func (sp *StrikePicker) GetCallToken() StrikeInfo {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.currentCE
}

// GetPutToken returns the current PUT strike info.
func (sp *StrikePicker) GetPutToken() StrikeInfo {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.currentPE
}

// ResolveATM resolves ATM CE and PE strikes for the given spot price.
// spotPrice is in integer paise format (e.g. 2336700 = ₹23,367.00).
// Returns true if resolution was successful.
func (sp *StrikePicker) ResolveATM(spotPrice int64) bool {
	// Convert from paise to rupees for strike calculation
	spotRupees := float64(spotPrice) / 100.0
	atm := int64(math.Round(spotRupees/float64(sp.strikeStep))) * sp.strikeStep

	log.Printf("[strikepicker] spot=%.2f → ATM strike=%d", spotRupees, atm)

	var ceToken, peToken string
	var ceLotSize, peLotSize int64
	var ceSymbol, peSymbol string
	var err error

	expiries := sp.candidateExpiries(time.Now())

	for _, expiry := range expiries {
		exp, perr := time.ParseInLocation("02Jan06", expiry, istZone)
		if perr != nil {
			continue
		}
		ceSymbol = sp.optionSymbol(exp, atm, "CE")
		peSymbol = sp.optionSymbol(exp, atm, "PE")
		if ceSymbol == "" || peSymbol == "" {
			err = fmt.Errorf("%s %d not listed in instrument master", expiry, atm)
			continue
		}

		// Delay between expiry attempts to avoid Angel One rate limits
		time.Sleep(2 * time.Second)

		ceToken, ceLotSize, err = sp.searchTokenWithRetry(ceSymbol)
		if err != nil {
			log.Printf("[strikepicker] ⚠️  expiry %s CE search failed (%v)... trying next", expiry, err)
			if isHardRateLimit(err) {
				log.Printf("[strikepicker] 🛑 rate-limited by Angel One — aborting strike resolution")
				break
			}
			continue
		}

		// 2s gap between CE and PE to stay under Angel One's rate limit
		time.Sleep(2 * time.Second)

		peToken, peLotSize, err = sp.searchTokenWithRetry(peSymbol)
		if err != nil {
			log.Printf("[strikepicker] ⚠️  expiry %s PE search failed (%v)... trying next", expiry, err)
			if isHardRateLimit(err) {
				log.Printf("[strikepicker] 🛑 rate-limited by Angel One — aborting strike resolution")
				break
			}
			continue
		}

		// Success!
		break
	}

	if ceToken == "" || peToken == "" {
		log.Printf("[strikepicker] ❌ could not resolve ATM options across any candidate expiries")
		return false
	}

	sp.mu.Lock()
	sp.currentCE = StrikeInfo{Token: ceToken, Symbol: ceSymbol, Strike: atm, LotSize: ceLotSize}
	sp.currentPE = StrikeInfo{Token: peToken, Symbol: peSymbol, Strike: atm, LotSize: peLotSize}
	sp.resolved = true
	sp.spotPrice = spotPrice
	sp.resolveTS = time.Now()
	sp.mu.Unlock()

	log.Printf("[strikepicker] ✅ resolved ATM=%d: CE=%s (token=%s, lot=%d), PE=%s (token=%s, lot=%d)",
		atm, ceSymbol, ceToken, ceLotSize, peSymbol, peToken, peLotSize)

	return true
}

// Reset clears the current resolution (e.g. for a new trading day).
func (sp *StrikePicker) Reset() {
	sp.mu.Lock()
	sp.resolved = false
	sp.currentCE = StrikeInfo{}
	sp.currentPE = StrikeInfo{}
	sp.spotPrice = 0
	sp.legCache = nil
	sp.expiry = time.Time{}
	sp.mu.Unlock()
	log.Println("[strikepicker] reset for new trading day")
}

// candidateExpiries returns a prioritized list of expiry strings (e.g. "07APR26")
// for the nearest weekly expiry. It skips today's date and past dates so the
// system never picks same-day expiry contracts (massive theta decay). When the
// current week's candidates all fail (e.g. expiry was today or shifted), it
// falls through to next-week candidates.
func (sp *StrikePicker) candidateExpiries(now time.Time) []string {
	now = now.In(istZone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, istZone)

	// Underlyings other than NIFTY use the master's listed expiries (never
	// today) when available.
	if sp.prof().Key != "NIFTY" {
		if listed := sp.upcomingListedExpiries(now); len(listed) > 0 {
			var out []string
			for _, e := range listed {
				if e.After(today) {
					out = append(out, strings.ToUpper(e.Format("02Jan06")))
				}
				if len(out) == 2 {
					break
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}

	// Normal expiry weekday: Tuesday for NIFTY.
	daysUntilTues := (int(sp.prof().ExpiryWeekday) - int(today.Weekday()) + 7) % 7
	if daysUntilTues == 0 {
		// On Tuesday (normal expiry day) today's contract is never traded, so
		// aim at next week. Keeping this week would leave only Wednesday and
		// Thursday, which never exist when Tuesday is expiry, and each miss
		// costs a broker SearchScrip call.
		daysUntilTues = 7
	}

	// Try: Tuesday (0), Monday (-1), Wednesday (+1), Thursday (+2 — old NIFTY expiry)
	// Covers all practical holiday-shifted scenarios with minimal API calls.
	offsets := []int{0, -1, 1, 2}

	var expiries []string

	// Current week candidates — skip today and past dates (never trade same-day expiry)
	for _, off := range offsets {
		dt := today.AddDate(0, 0, daysUntilTues+off)
		if !dt.After(today) {
			continue
		}
		expiries = append(expiries, strings.ToUpper(dt.Format("02Jan06")))
	}

	// Next week candidates as fallback (when current week's expiry was today/past)
	for _, off := range offsets {
		dt := today.AddDate(0, 0, daysUntilTues+7+off)
		expiries = append(expiries, strings.ToUpper(dt.Format("02Jan06")))
	}

	return expiries
}

// isTransientError returns true for errors that are likely rate-limit
// responses (Angel One returns HTML when throttled → JSON parse fails).
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "parse JSON") ||
		strings.Contains(msg, "invalid character") ||
		strings.Contains(msg, "unexpected end of JSON")
}

// isHardRateLimit returns true for explicit 403 / rate-limit rejections.
func isHardRateLimit(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "exceeding access rate") || strings.Contains(msg, "403")
}

// searchTokenWithRetry wraps searchToken with exponential backoff
// for transient rate-limit errors (HTML responses).
func (sp *StrikePicker) searchTokenWithRetry(symbol string) (token string, lotSize int64, err error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		token, lotSize, err := sp.searchToken(symbol)
		if err == nil {
			return token, lotSize, nil
		}
		lastErr = err

		// Only retry on transient errors (rate-limit HTML responses)
		if !isTransientError(err) {
			return "", 0, err
		}

		backoff := time.Duration(2<<uint(attempt)) * time.Second // 2s, 4s, 8s
		log.Printf("[strikepicker] ⏳ rate-limited on %s, retrying in %s (attempt %d/3)", symbol, backoff, attempt+1)
		time.Sleep(backoff)
	}
	return "", 0, lastErr
}

// searchToken calls SearchScrip and returns the token and lot size for the given symbol.
func (sp *StrikePicker) searchToken(symbol string) (token string, lotSize int64, err error) {
	res, err := sp.sc.SearchScrip(sp.exchangeSeg(), symbol)
	if err != nil {
		return "", 0, fmt.Errorf("SearchScrip(%s): %w", symbol, err)
	}

	// Parse response: {"data": [{"symboltoken": "57710", "tradingsymbol": "...", ...}]}
	data, ok := res["data"]
	if !ok {
		return "", 0, fmt.Errorf("SearchScrip(%s): no 'data' in response", symbol)
	}

	dataSlice, ok := data.([]interface{})
	if !ok || len(dataSlice) == 0 {
		return "", 0, fmt.Errorf("SearchScrip(%s): empty data array", symbol)
	}

	first, ok := dataSlice[0].(map[string]interface{})
	if !ok {
		return "", 0, fmt.Errorf("SearchScrip(%s): unexpected data format", symbol)
	}

	token, ok = first["symboltoken"].(string)
	if !ok || token == "" {
		return "", 0, fmt.Errorf("SearchScrip(%s): no symboltoken found", symbol)
	}

	// Fetch lot size from instrument master file
	im := GetInstrumentMaster()
	lotSize, err = im.GetLotSize(symbol)
	if err != nil {
		// Fallback to environment variable if instrument master lookup fails
		log.Printf("[strikepicker] ⚠️  failed to get lot size from instrument master for %s: %v (using fallback)", symbol, err)
		lotSize = sp.lotFallback()
	} else {
		log.Printf("[strikepicker] 📊 fetched lot size for %s: %d", symbol, lotSize)
	}

	return token, lotSize, nil
}
