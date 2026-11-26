package tgsignal

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"trading-systemv1/internal/model"
)

// Defaults fill what a call leaves out: a stop ("Stoploss : PAID") and
// targets. Points are option premium points (1 point = 100 paise).
type Defaults struct {
	// SLPoints is the stop distance below the trigger per underlying
	// (TG_DEFAULT_SL_POINTS, e.g. SENSEX=30). It wins over SLPct.
	SLPoints map[string]int64
	// SLPct is the stop % below the trigger for an underlying without
	// points (TG_DEFAULT_SL_PCT). 0 = such calls are refused.
	SLPct int64
	// TargetPoints are the targets above the trigger for a call that gives
	// none (TG_DEFAULT_TARGET_POINTS, e.g. 20,30). Empty = none.
	TargetPoints []int64
}

// stopFor is the default stop for a call on underlying with trigger, 0
// when there is none (the call is refused).
func (d Defaults) stopFor(underlying string, trigger int64) int64 {
	if pts := d.SLPoints[strings.ToUpper(underlying)]; pts > 0 {
		if sl := trigger - pts*100; sl > 0 {
			return sl
		}
		return 0
	}
	if d.SLPct > 0 {
		return trigger * (100 - d.SLPct) / 100
	}
	return 0
}

// withDefaultSL gives a regex call without a stop (ErrNoSL) the default
// stop. No default for its underlying keeps the refusal.
func withDefaultSL(rx model.TGSignal, rxErr error, d Defaults) (model.TGSignal, error) {
	if !errors.Is(rxErr, ErrNoSL) {
		return rx, rxErr
	}
	sl := d.stopFor(rx.Underlying, rx.TriggerPaise)
	if sl <= 0 {
		return rx, rxErr
	}
	rx.SLPaise, rx.SLDefault = sl, true
	return rx, rx.Validate()
}

// llmWithDefaultSL does the same for the LLM's reading of a new call, so
// both readers still have to agree on the rest.
func llmWithDefaultSL(r LLMResult, d Defaults) LLMResult {
	if r.Kind != "new_call" || toPaise(cleanPrice(r.SL)) > 0 {
		return r
	}
	trig := toPaise(cleanPrice(r.Trigger))
	if trig <= 0 {
		return r
	}
	sl := d.stopFor(normalizeUnderlying(r.Underlying), trig)
	if sl <= 0 {
		return r
	}
	r.SL, r.slDefault = fmt.Sprintf("%d.%02d", sl/100, sl%100), true
	return r
}

// withDefaultTargets gives a call without targets the default ones.
func withDefaultTargets(c model.TGSignal, d Defaults) model.TGSignal {
	if len(c.Targets) > 0 || len(d.TargetPoints) == 0 || c.TriggerPaise <= 0 {
		return c
	}
	for _, pts := range d.TargetPoints {
		c.Targets = append(c.Targets, c.TriggerPaise+pts*100)
	}
	c.TargetsDefault = true
	return c
}

// parseSLPoints reads "NIFTY=15,SENSEX=30,BANKNIFTY=30".
func parseSLPoints(s string) (map[string]int64, error) {
	out := map[string]int64{}
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "off") {
		return out, nil
	}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if !ok || err != nil || n <= 0 || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("bad entry %q (want INDEX=points)", part)
		}
		out[strings.ToUpper(strings.TrimSpace(k))] = n
	}
	return out, nil
}

// parseTargetPoints reads "20,30": ascending points above the trigger.
func parseTargetPoints(s string) ([]int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "off") {
		return nil, nil
	}
	var out []int64
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || n <= 0 || (len(out) > 0 && n <= out[len(out)-1]) {
			return nil, fmt.Errorf("bad target points %q (want ascending numbers above 0)", s)
		}
		out = append(out, n)
	}
	return out, nil
}
