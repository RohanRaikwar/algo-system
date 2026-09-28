package orderexec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ResolveStrike resolves one NIFTY option contract at an arbitrary strike
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
		symbol := fmt.Sprintf("NIFTY%s%d%s", expiry, strike, optionType)
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
	if inst, err := GetInstrumentMaster().GetInstrument(symbol); err == nil && inst.Token != "" {
		lot, _ := strconv.ParseInt(strings.TrimSpace(inst.LotSize), 10, 64)
		if lot <= 0 {
			lot = int64(getEnvInt("NIFTY_LOT_SIZE", 65))
		}
		return inst.Token, lot, nil
	}
	if sp.sc == nil {
		return "", 0, fmt.Errorf("%s not in instrument master and no broker session", symbol)
	}
	return sp.searchTokenWithRetry(symbol)
}
