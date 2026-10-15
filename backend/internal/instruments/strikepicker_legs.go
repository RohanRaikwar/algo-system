package instruments

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ResolveStrike resolves one option contract of the picker's underlying at an arbitrary strike
// (index points) for the nearest weekly expiry, for multi-leg baskets. It
// tries the offline instrument master first and falls back to SearchScrip.
// Results are cached per symbol for the day (cleared by Reset).
func (sp *StrikePicker) ResolveStrike(now time.Time, strike int64, optionType string) (StrikeInfo, error) {
	optionType = strings.ToUpper(optionType)
	if optionType != "CE" && optionType != "PE" {
		return StrikeInfo{}, fmt.Errorf("option type %q: want CE or PE", optionType)
	}
	if strike <= 0 || strike%sp.strikeStep != 0 {
		return StrikeInfo{}, fmt.Errorf("strike %d is not a multiple of %d", strike, sp.strikeStep)
	}

	var lastErr error
	for _, expiry := range sp.candidateExpiries(now) {
		exp, err := time.ParseInLocation("02Jan06", expiry, istZone)
		if err != nil {
			continue
		}
		symbol := sp.optionSymbol(exp, strike, optionType)
		if symbol == "" {
			lastErr = fmt.Errorf("%s %d%s not listed in instrument master", expiry, strike, optionType)
			continue
		}
		sp.mu.RLock()
		cached, ok := sp.legCache[symbol]
		sp.mu.RUnlock()
		if ok {
			return cached, nil
		}
		token, lot, err := sp.lookupSymbol(symbol)
		if err != nil {
			lastErr = err
			if isHardRateLimit(err) {
				break
			}
			continue
		}
		info := StrikeInfo{Token: token, Symbol: symbol, Strike: strike, LotSize: lot}
		sp.mu.Lock()
		if sp.legCache == nil {
			sp.legCache = make(map[string]StrikeInfo)
		}
		sp.legCache[symbol] = info
		if exp, err := time.ParseInLocation("02Jan06", expiry, istZone); err == nil {
			sp.expiry = exp
		}
		sp.mu.Unlock()
		return info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate expiry")
	}
	return StrikeInfo{}, fmt.Errorf("resolve %d%s: %w", strike, optionType, lastErr)
}

// CurrentExpiry returns the expiry of the last resolved contract (zero if
// none yet). Strategies use it for days-to-expiry.
func (sp *StrikePicker) CurrentExpiry() time.Time {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.expiry
}

// NextExpiry returns the nearest weekly expiry the picker would trade
// (never today), without any broker call.
func (sp *StrikePicker) NextExpiry(now time.Time) time.Time {
	if exp := sp.CurrentExpiry(); !exp.IsZero() && exp.After(now.In(istZone)) {
		return exp
	}
	for _, e := range sp.candidateExpiries(now) {
		if exp, err := time.ParseInLocation("02Jan06", e, istZone); err == nil {
			return exp
		}
	}
	return time.Time{}
}

func (sp *StrikePicker) lookupSymbol(symbol string) (string, int64, error) {
	if sp.lookup != nil {
		return sp.lookup(symbol)
	}
	if token, lot, ok := sp.lookupOffline(symbol); ok {
		if lot <= 0 {
			lot = sp.lotFallback()
		}
		return token, lot, nil
	}
	if sp.sc == nil {
		return "", 0, fmt.Errorf("%s not in instrument master and no broker session", symbol)
	}
	return sp.searchTokenWithRetry(symbol)
}

// lookupOffline finds symbol in the test hook or the instrument master,
// never the broker. lot is 0 when the master has none.
func (sp *StrikePicker) lookupOffline(symbol string) (string, int64, bool) {
	if sp.lookup != nil {
		token, lot, err := sp.lookup(symbol)
		return token, lot, err == nil && token != ""
	}
	inst, err := GetInstrumentMaster().GetInstrument(symbol)
	if err != nil || inst.Token == "" {
		return "", 0, false
	}
	lot, _ := strconv.ParseInt(strings.TrimSpace(inst.LotSize), 10, 64)
	return inst.Token, lot, true
}

// ResolveStrikeOn resolves one option contract on a given expiry (a
// strategy that skips the nearest expiry). Unlike ResolveStrike it does not
// change CurrentExpiry. Results are cached with ResolveStrike's.
func (sp *StrikePicker) ResolveStrikeOn(expiry time.Time, strike int64, optionType string) (StrikeInfo, error) {
	optionType = strings.ToUpper(optionType)
	if optionType != "CE" && optionType != "PE" {
		return StrikeInfo{}, fmt.Errorf("option type %q: want CE or PE", optionType)
	}
	if strike <= 0 || strike%sp.strikeStep != 0 {
		return StrikeInfo{}, fmt.Errorf("strike %d is not a multiple of %d", strike, sp.strikeStep)
	}
	symbol := sp.optionSymbol(expiry, strike, optionType)
	if symbol == "" {
		return StrikeInfo{}, fmt.Errorf("resolve %d%s %s: not listed in instrument master", strike, optionType, expiry.Format("02Jan06"))
	}
	sp.mu.RLock()
	cached, ok := sp.legCache[symbol]
	sp.mu.RUnlock()
	if ok {
		return cached, nil
	}
	token, lot, err := sp.lookupSymbol(symbol)
	if err != nil {
		return StrikeInfo{}, fmt.Errorf("resolve %s: %w", symbol, err)
	}
	info := StrikeInfo{Token: token, Symbol: symbol, Strike: strike, LotSize: lot}
	sp.mu.Lock()
	if sp.legCache == nil {
		sp.legCache = make(map[string]StrikeInfo)
	}
	sp.legCache[symbol] = info
	sp.mu.Unlock()
	return info, nil
}

// LookupOn is ResolveStrikeOn without the SearchScrip fallback: the offline
// instrument master (or the test hook) only. Used where a broker call is not
// allowed (the option picker's universe; SearchScrip is limited to 1/s).
func (sp *StrikePicker) LookupOn(expiry time.Time, strike int64, optionType string) (StrikeInfo, bool) {
	optionType = strings.ToUpper(optionType)
	symbol := sp.optionSymbol(expiry, strike, optionType)
	if symbol == "" {
		return StrikeInfo{}, false
	}
	sp.mu.RLock()
	cached, ok := sp.legCache[symbol]
	sp.mu.RUnlock()
	if ok {
		return cached, true
	}
	token, lot, ok := sp.lookupOffline(symbol)
	if !ok {
		return StrikeInfo{}, false
	}
	info := StrikeInfo{Token: token, Symbol: symbol, Strike: strike, LotSize: lot}
	sp.mu.Lock()
	if sp.legCache == nil {
		sp.legCache = make(map[string]StrikeInfo)
	}
	sp.legCache[symbol] = info
	sp.mu.Unlock()
	return info, true
}

// LookupNearest is ResolveStrike without the SearchScrip fallback: the
// nearest candidate expiry listed offline. Like ResolveStrike it updates
// CurrentExpiry. For the signal path, where a broker call is not allowed.
func (sp *StrikePicker) LookupNearest(now time.Time, strike int64, optionType string) (StrikeInfo, bool) {
	if strike <= 0 || sp.strikeStep <= 0 || strike%sp.strikeStep != 0 {
		return StrikeInfo{}, false
	}
	for _, expiry := range sp.candidateExpiries(now) {
		exp, err := time.ParseInLocation("02Jan06", expiry, istZone)
		if err != nil {
			continue
		}
		if info, ok := sp.LookupOn(exp, strike, optionType); ok {
			sp.mu.Lock()
			sp.expiry = exp
			sp.mu.Unlock()
			return info, true
		}
	}
	return StrikeInfo{}, false
}
