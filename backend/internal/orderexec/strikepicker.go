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

// StrikePicker resolves ATM CE/PE strikes for NIFTY options.
type StrikePicker struct {
	sc         *smartconnect.SmartConnect
	exchange   string // "NFO"
	strikeStep int64  // 50 for NIFTY

	mu        sync.RWMutex
	currentCE StrikeInfo
	currentPE StrikeInfo
	resolved  bool
	spotPrice int64
	resolveTS time.Time
}

// NewStrikePicker creates a new picker that uses the given SmartConnect client.
func NewStrikePicker(sc *smartconnect.SmartConnect) *StrikePicker {
	return &StrikePicker{
		sc:         sc,
		exchange:   "NFO",
		strikeStep: 50,
	}
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
		ceSymbol = fmt.Sprintf("NIFTY%s%dCE", expiry, atm)
		peSymbol = fmt.Sprintf("NIFTY%s%dPE", expiry, atm)

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

	// Normal NIFTY expiry is Tuesday.
	daysUntilTues := (int(time.Tuesday) - int(today.Weekday()) + 7) % 7
	if daysUntilTues == 0 {
		// On Tuesday (normal expiry day): during market hours aim at next week;
		// after 15:30 IST also advance to next week.
		if now.Hour() > 15 || (now.Hour() == 15 && now.Minute() >= 30) {
			daysUntilTues = 7
		}
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
	res, err := sp.sc.SearchScrip(sp.exchange, symbol)
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
		lotSize = int64(getEnvInt("NIFTY_LOT_SIZE", 65))
	} else {
		log.Printf("[strikepicker] 📊 fetched lot size for %s: %d", symbol, lotSize)
	}

	return token, lotSize, nil
}
