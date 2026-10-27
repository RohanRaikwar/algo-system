// Package tgsignal reads option calls from Telegram group messages and
// publishes them on Redis (model.TGSignalChannel) for stratengine.
package tgsignal

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"trading-systemv1/internal/model"
)

// ErrNotACall is returned for messages that are not a buy call (chat,
// updates like "target done", exits).
var ErrNotACall = errors.New("not a buy call")

// ErrNoSL is returned, with the parsed call, for a call that is complete
// except for its stop ("Stoploss : PAID"). The reader may give it a
// default stop (TG_DEFAULT_SL_PCT).
var ErrNoSL = errors.New("no stop loss")

var (
	reBuy        = regexp.MustCompile(`\bBUY\b`)
	reUnderlying = regexp.MustCompile(`\b(BANKNIFTY|FINNIFTY|MIDCPNIFTY|NIFTY|SENSEX|BANKEX)\b`)
	// "24500 CE" or "PE 22750".
	reContract = regexp.MustCompile(`\b(\d{4,6})\s*(CE|PE|CALL|PUT)\b|\b(CE|PE|CALL|PUT)\s*(\d{4,6})\b`)
	reTrigger  = regexp.MustCompile(`\b(?:ENTRY|ABOVE|NEAR|AROUND|BUY\s+AT)\b|@`)
	reSL       = regexp.MustCompile(`\b(?:SL|STOP\s*LOSS|STOPLOSS)\b`)
	reTarget   = regexp.MustCompile(`\b(?:TARGETS?|TGTS?)\b`)
	// "Target 1 ⛔️ 60": a numbered target line, one price each.
	reTargetN = regexp.MustCompile(`\b(?:TARGETS?|TGTS?)\s*\d\b[^\d\n]*(\d+(?:\.\d+)?)`)
	// "ENTRY PRICE ➖ 50" with targets is a call even without the word BUY.
	reEntry = regexp.MustCompile(`\bENTRY\b`)
	// "SL 15 point" / "SL 15 pts": a distance below the entry, not a price.
	reSLPoints = regexp.MustCompile(`\b(?:SL|STOP\s*LOSS|STOPLOSS)\b\s*[:=\-]?\s*₹?\s*(\d+(?:\.\d+)?)\s*(?:POINTS?|PTS?|POINT)\b`)
	reOrdinal  = regexp.MustCompile(`\b\d+\s*(?:ST|ND|RD|TH)\b`)
	reNumber   = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

// supported are the underlyings stratengine can trade.
var supported = map[string]bool{"NIFTY": true, "SENSEX": true}

// Parse reads one message. It returns ErrNotACall for text that is not a
// buy call, and another error for a call that cannot be traded (missing
// stop, unsupported index).
func Parse(text string) (model.TGSignal, error) {
	up := strings.ToUpper(text)
	var sig model.TGSignal
	if !reBuy.MatchString(up) && !(reEntry.MatchString(up) && reTarget.MatchString(up)) {
		return sig, ErrNotACall
	}
	c := reContract.FindStringSubmatch(up)
	if c == nil {
		return sig, ErrNotACall
	}
	if n := len(reContract.FindAllString(up, -1)); n > 1 {
		return sig, fmt.Errorf("%d contracts in one message", n)
	}
	u := reUnderlying.FindStringSubmatch(up)
	if u == nil {
		return sig, fmt.Errorf("no index named")
	}
	if !supported[u[1]] {
		return sig, fmt.Errorf("%s not supported", u[1])
	}
	sig.Underlying = u[1]
	strike, opt := c[1], c[2]
	if strike == "" {
		strike, opt = c[4], c[3]
	}
	sig.Strike, _ = strconv.ParseInt(strike, 10, 64)
	sig.OptionType = "CE"
	if opt == "PE" || opt == "PUT" {
		sig.OptionType = "PE"
	}

	// Prices come after the contract, so the strike is never read as one.
	rest := up[strings.Index(up, c[0])+len(c[0]):]
	if n := numbersAfter(rest, reTrigger); len(n) > 0 {
		sig.TriggerPaise = n[0]
	}
	if m := reSLPoints.FindStringSubmatch(rest); m != nil {
		if sig.TriggerPaise > 0 {
			sig.SLPaise = sig.TriggerPaise - toPaise(m[1])
		}
	} else if n := numbersAfter(rest, reSL); len(n) > 0 {
		sig.SLPaise = n[0]
	}
	if m := reTargetN.FindAllStringSubmatch(rest, -1); len(m) > 0 {
		for _, t := range m {
			sig.Targets = append(sig.Targets, toPaise(t[1]))
		}
	} else {
		sig.Targets = numbersAfter(rest, reTarget)
	}
	sig.Text = text
	if sig.SLPaise == 0 {
		// Complete but for the stop: ErrNoSL, else the real problem.
		withSL := sig
		withSL.SLPaise = 1
		if err := withSL.Validate(); err != nil {
			return sig, err
		}
		return sig, ErrNoSL
	}
	if err := sig.Validate(); err != nil {
		return sig, err
	}
	return sig, nil
}

// numbersAfter returns the prices (paise) on the line after the first
// match of key, up to the next keyword: "Entry: Near Above 191" → 191,
// "Target:1st: ₹202 , 2ND:221" → 202, 221.
func numbersAfter(text string, key *regexp.Regexp) []int64 {
	loc := key.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	line := text[loc[1]:]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	for _, other := range []*regexp.Regexp{reTrigger, reSL, reTarget} {
		if other == key {
			continue
		}
		if l := other.FindStringIndex(line); l != nil {
			line = line[:l[0]]
		}
	}
	line = reOrdinal.ReplaceAllString(line, " ")
	var out []int64
	for _, n := range reNumber.FindAllString(line, -1) {
		out = append(out, toPaise(n))
	}
	return out
}

// toPaise converts "120" or "120.5" to paise without a float.
func toPaise(s string) int64 {
	whole, frac, _ := strings.Cut(s, ".")
	r, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0
	}
	r *= 100
	frac = (frac + "00")[:2]
	f, _ := strconv.ParseInt(frac, 10, 64)
	return r + f
}
